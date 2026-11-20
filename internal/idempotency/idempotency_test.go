package idempotency_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/idempotency"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// setup is a database with one user and one session of theirs.
func setup(t *testing.T) (db *sqlx.DB, svc *idempotency.Service, c *clock, sessionID int64) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	c = &clock{t: time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)}
	uid, err := user.Create(t.Context(), st.DB(), c.t, user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 1, Day: 1}})
	require.NoError(t, err)
	_, sess, err := session.Create(t.Context(), st.DB(), c.t, uid, "")
	require.NoError(t, err)
	return st.DB(), idempotency.NewService(st.DB(), c.now), c, sess.ID
}

var created = idempotency.Answer{
	Status: http.StatusCreated,
	Header: http.Header{"Location": {"/api/v2/items/7"}, "Link": {"<a>", "<b>"}},
	Body:   []byte(`{"id":"7"}`),
}

// keep runs the first request with a key and keeps an answer for it.
func keep(t *testing.T, svc *idempotency.Service, sessionID int64, key, request string, a idempotency.Answer) {
	t.Helper()
	run, answer, err := svc.Begin(t.Context(), sessionID, key, request)
	require.NoError(t, err)
	require.Nil(t, answer)
	require.NoError(t, run.Keep(t.Context(), a))
	run.End()
}

func rows(t *testing.T, db *sqlx.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.GetContext(t.Context(), &n, `SELECT count(*) FROM idempotency_keys`))
	return n
}

func TestARepeatGetsTheAnswerThatTheFirstRequestKept(t *testing.T) {
	_, svc, _, sid := setup(t)
	keep(t, svc, sid, "k", "request", created)

	run, answer, err := svc.Begin(t.Context(), sid, "k", "request")

	require.NoError(t, err)
	require.Nil(t, run)
	require.Equal(t, &created, answer)
}

func TestAnAnswerWithNoBodyIsKept(t *testing.T) {
	_, svc, _, sid := setup(t)
	keep(t, svc, sid, "k", "request", idempotency.Answer{Status: http.StatusNoContent, Header: http.Header{}})

	_, answer, err := svc.Begin(t.Context(), sid, "k", "request")

	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, answer.Status)
	require.Empty(t, answer.Body)
}

func TestAKeyWithAnotherRequestIsReused(t *testing.T) {
	_, svc, _, sid := setup(t)
	keep(t, svc, sid, "k", "request", created)

	_, _, err := svc.Begin(t.Context(), sid, "k", "another request")

	require.ErrorIs(t, err, idempotency.ErrReused)
}

func TestAKeyIsInUseWhileItsFirstRequestRunsAndFreeWhenThatKeptNothing(t *testing.T) {
	_, svc, _, sid := setup(t)
	run, _, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)

	_, _, err = svc.Begin(t.Context(), sid, "k", "request")
	require.ErrorIs(t, err, idempotency.ErrInUse)
	_, _, err = svc.Begin(t.Context(), sid, "k", "another request")
	require.ErrorIs(t, err, idempotency.ErrReused)

	// The first request failed, so it keeps nothing. The key is new again.
	run.End()
	again, answer, err := svc.Begin(t.Context(), sid, "k", "another request")
	require.NoError(t, err)
	require.Nil(t, answer)
	require.NotNil(t, again)
}

func TestAKeyBelongsToItsSession(t *testing.T) {
	db, svc, c, sid := setup(t)
	var uid int64
	require.NoError(t, db.GetContext(t.Context(), &uid, `SELECT user_id FROM sessions WHERE id = ?`, sid))
	_, other, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)
	keep(t, svc, sid, "k", "request", created)

	run, answer, err := svc.Begin(t.Context(), other.ID, "k", "another request")

	require.NoError(t, err)
	require.Nil(t, answer)
	require.NotNil(t, run)
}

func TestTheWindowIs24HoursFromTheFirstRequestAndOldAnswersAreDeleted(t *testing.T) {
	db, svc, c, sid := setup(t)
	// The first request takes a minute. The window starts when it arrived.
	run, _, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)
	c.t = c.t.Add(time.Minute)
	require.NoError(t, run.Keep(t.Context(), created))
	run.End()

	c.t = c.t.Add(idempotency.Window - time.Minute - time.Second)
	_, answer, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)
	require.NotNil(t, answer, "a second before the window is over")

	c.t = c.t.Add(time.Second)
	again, answer, err := svc.Begin(t.Context(), sid, "k", "another request")
	require.NoError(t, err)
	require.Nil(t, answer, "the key is forgotten")
	again.End()
	require.Equal(t, 1, rows(t, db), "nothing deletes the old answer until an answer is kept")

	keep(t, svc, sid, "another key", "request", created)
	require.Equal(t, 1, rows(t, db), "keeping an answer deleted the old one")
}

func TestTheKeysOfASessionGoWhenItIsRevokedOrDeleted(t *testing.T) {
	db, svc, c, sid := setup(t)
	var uid int64
	require.NoError(t, db.GetContext(t.Context(), &uid, `SELECT user_id FROM sessions WHERE id = ?`, sid))
	_, other, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)
	keep(t, svc, sid, "k", "request", created)
	keep(t, svc, other.ID, "k", "request", created)

	_, ok, err := session.RevokeLive(t.Context(), db, c.t, sid)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, rows(t, db), "the revoked session's key is gone, and the other session's stays")

	_, err = db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id = ?`, other.ID)
	require.NoError(t, err)
	require.Zero(t, rows(t, db))
}

func TestARequestThatEndedItsOwnSessionKeepsNothing(t *testing.T) {
	db, svc, c, sid := setup(t)
	run, _, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)
	_, _, err = session.RevokeLive(t.Context(), db, c.t, sid)
	require.NoError(t, err)

	require.NoError(t, run.Keep(t.Context(), created))
	run.End()

	require.Zero(t, rows(t, db))
}

// A key that no request asks for again is still forgotten: the sweep deletes
// the answers whose window is over, whether or not another answer is kept.
func TestTheSweepDeletesTheAnswersOver24HoursOldAndNoOthers(t *testing.T) {
	db, svc, c, sid := setup(t)
	keep(t, svc, sid, "old", "request", created)
	c.t = c.t.Add(time.Hour)
	keep(t, svc, sid, "new", "request", created)

	c.t = c.t.Add(idempotency.Window - time.Hour - time.Second)
	n, err := svc.Sweep(t.Context())
	require.NoError(t, err)
	require.Zero(t, n, "a second before the first window is over")
	require.Equal(t, 2, rows(t, db))

	c.t = c.t.Add(time.Second)
	n, err = svc.Sweep(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	var left []string
	require.NoError(t, db.SelectContext(t.Context(), &left, `SELECT key FROM idempotency_keys`))
	require.Equal(t, []string{"new"}, left)
}

// The first run is at start, so a server that was down for a day keeps no old
// answer once it is up.
func TestTheScheduleSweepsWhenItStarts(t *testing.T) {
	db, svc, c, sid := setup(t)
	keep(t, svc, sid, "old", "request", created)
	c.t = c.t.Add(idempotency.Window)

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Schedule(ctx, nil)
	}()
	require.Eventually(t, func() bool { return rows(t, db) == 0 }, 5*time.Second, 10*time.Millisecond,
		"the first run is before the first tick")
	stop()
	<-done
}
