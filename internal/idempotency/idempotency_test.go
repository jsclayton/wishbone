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
func setup(t *testing.T) (db *sqlx.DB, svc *idempotency.Service, c *clock, sess idempotency.Session) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	c = &clock{t: time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)}
	_, err = user.Create(t.Context(), st.DB(), c.t, user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 1, Day: 1}})
	require.NoError(t, err)
	return st.DB(), idempotency.NewService(st.DB(), c.now), c, newSession(t, st.DB(), c)
}

// newSession starts another session of the one user.
func newSession(t *testing.T, db *sqlx.DB, c *clock) idempotency.Session {
	t.Helper()
	var uid int64
	require.NoError(t, db.GetContext(t.Context(), &uid, `SELECT id FROM users`))
	token, sess, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)
	return idempotency.Session{ID: sess.ID, TokenHash: session.Hash(token)}
}

var created = idempotency.Answer{
	Status: http.StatusCreated,
	Header: http.Header{"Location": {"/api/v2/items/7"}, "Link": {"<a>", "<b>"}},
	Body:   []byte(`{"id":"7"}`),
}

// keep runs the first request with a key and keeps an answer for it.
func keep(t *testing.T, svc *idempotency.Service, sess idempotency.Session, key, request string, a idempotency.Answer) {
	t.Helper()
	run, answer, err := svc.Begin(t.Context(), sess, key, request)
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
	other := newSession(t, db, c)
	keep(t, svc, sid, "k", "request", created)

	run, answer, err := svc.Begin(t.Context(), other, "k", "another request")

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
	other := newSession(t, db, c)
	keep(t, svc, sid, "k", "request", created)
	keep(t, svc, other, "k", "request", created)

	_, ok, err := session.RevokeLive(t.Context(), db, c.t, sid.ID)
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
	_, _, err = session.RevokeLive(t.Context(), db, c.t, sid.ID)
	require.NoError(t, err)

	require.NoError(t, run.Keep(t.Context(), created))
	run.End()

	require.Zero(t, rows(t, db))
}

// The database gives the ID of a deleted session to the next one. A run of the
// old session keeps nothing for the new one, which has another token, and the
// new one's request with the same key is not held up by the old run.
func TestARunKeepsNothingForALaterSessionThatHasItsID(t *testing.T) {
	db, svc, c, sid := setup(t)
	run, _, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id = ?`, sid.ID)
	require.NoError(t, err)
	next := newSession(t, db, c)
	require.Equal(t, sid.ID, next.ID, "the new session has the ID of the deleted one")
	require.NotEqual(t, sid.TokenHash, next.TokenHash)

	// The new session's request runs while the old run is not over.
	theirs, answer, err := svc.Begin(t.Context(), next, "k", "request")
	require.NoError(t, err)
	require.Nil(t, answer)
	theirs.End()

	require.NoError(t, run.Keep(t.Context(), created))
	run.End()
	require.Zero(t, rows(t, db))

	_, answer, err = svc.Begin(t.Context(), next, "k", "request")
	require.NoError(t, err)
	require.Nil(t, answer, "the new session gets no answer of the old one")
}

// ForgetAll clears the answers in the table, and the epoch clears the runs
// that are in no table: one that began before the clear keeps nothing.
func TestARunThatBeganBeforeEveryAnswerWasClearedKeepsNothing(t *testing.T) {
	db, svc, _, sid := setup(t)
	keep(t, svc, sid, "kept", "request", created)
	before, _, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)

	// Another process clears the answers: it has no Service, only the database.
	require.NoError(t, idempotency.ForgetAll(t.Context(), db))
	require.Zero(t, rows(t, db))

	require.NoError(t, before.Keep(t.Context(), created))
	before.End()
	require.Zero(t, rows(t, db), "the run began before the clear")

	// A run that begins after the clear is kept, and so is one after a second clear.
	keep(t, svc, sid, "k", "request", created)
	require.Equal(t, 1, rows(t, db))
	require.NoError(t, idempotency.ForgetAll(t.Context(), db))
	keep(t, svc, sid, "k", "request", created)
	require.Equal(t, 1, rows(t, db))
}

// A clear that is rolled back clears nothing, and a run across it is kept.
func TestAClearThatIsRolledBackLeavesTheAnswersAndTheRuns(t *testing.T) {
	db, svc, _, sid := setup(t)
	keep(t, svc, sid, "kept", "request", created)
	run, _, err := svc.Begin(t.Context(), sid, "k", "request")
	require.NoError(t, err)

	tx, err := db.BeginTxx(t.Context(), nil)
	require.NoError(t, err)
	require.NoError(t, idempotency.ForgetAll(t.Context(), tx))
	require.NoError(t, tx.Rollback())

	require.NoError(t, run.Keep(t.Context(), created))
	run.End()
	require.Equal(t, 2, rows(t, db))
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

// A caller can still hold the ID of a session that is gone, when a later
// session has it. Such a caller finds nothing of the later session: no answer
// for the same request, and no reuse for another one.
func TestACallerWithTheIDOfALaterSessionFindsNoneOfItsAnswers(t *testing.T) {
	db, svc, c, old := setup(t)
	_, err := db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id = ?`, old.ID)
	require.NoError(t, err)
	next := newSession(t, db, c)
	require.Equal(t, old.ID, next.ID, "the new session has the ID of the deleted one")
	keep(t, svc, next, "k", "request", created)

	for _, request := range []string{"request", "another request"} {
		run, answer, err := svc.Begin(t.Context(), old, "k", request)
		require.NoError(t, err, request)
		require.Nil(t, answer, request)
		require.NotNil(t, run, request)
		require.NoError(t, run.Keep(t.Context(), created))
		run.End()
	}

	// The later session's answer is as it was, and is still its own.
	require.Equal(t, 1, rows(t, db))
	_, answer, err := svc.Begin(t.Context(), next, "k", "request")
	require.NoError(t, err)
	require.Equal(t, &created, answer)
}
