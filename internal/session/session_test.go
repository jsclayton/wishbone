package session_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T) (*sqlx.DB, *session.Service, *clock, int64) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	c := &clock{t: time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)}
	uid, err := user.Create(t.Context(), st.DB(), c.t, user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 1, Day: 1}})
	require.NoError(t, err)
	return st.DB(), session.NewService(st.DB(), c.now), c, uid
}

func TestASessionIsFoundByItsTokenAndMovesForwardOnEachUse(t *testing.T) {
	db, svc, c, uid := setup(t)
	token, created, err := session.Create(t.Context(), db, c.t, uid, "test agent")
	require.NoError(t, err)

	c.t = c.t.Add(100 * 24 * time.Hour)
	got, err := svc.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, uid, got.UserID)
	require.Equal(t, c.t.Add(session.Lifetime), got.ExpiresAt)

	// 364 days after the last use is still inside the year.
	c.t = c.t.Add(364 * 24 * time.Hour)
	_, err = svc.Authenticate(t.Context(), token)
	require.NoError(t, err)
}

func TestASessionExpiresAYearAfterItsLastUse(t *testing.T) {
	db, svc, c, uid := setup(t)
	token, _, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)

	c.t = c.t.Add(session.Lifetime + time.Second)
	_, err = svc.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, session.ErrNone)
}

func TestTheDatabaseKeepsOnlyTheHashOfTheToken(t *testing.T) {
	db, _, c, uid := setup(t)
	token, _, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)

	var stored string
	require.NoError(t, db.GetContext(t.Context(), &stored, `SELECT token_hash FROM sessions`))
	require.NotEqual(t, token, stored)
	require.Equal(t, session.Hash(token), stored)
}

func TestUnknownAndEmptyTokensAreNotSessions(t *testing.T) {
	_, svc, _, _ := setup(t)
	for _, token := range []string{"", "nope"} {
		_, err := svc.Authenticate(t.Context(), token)
		require.ErrorIs(t, err, session.ErrNone)
	}
}

func TestARevokedSessionIsNotLive(t *testing.T) {
	db, svc, c, uid := setup(t)
	token, s, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)

	require.NoError(t, svc.Revoke(t.Context(), uid, s.ID, nil))

	_, err = svc.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, session.ErrNone)
	require.ErrorIs(t, svc.Revoke(t.Context(), uid, s.ID, nil), session.ErrNone, "revoking twice")
}

func TestRevokeOnlyEndsASessionOfThatUser(t *testing.T) {
	db, svc, c, uid := setup(t)
	token, s, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)

	require.ErrorIs(t, svc.Revoke(t.Context(), uid+1, s.ID, nil), session.ErrNone)
	_, err = svc.Authenticate(t.Context(), token)
	require.NoError(t, err)
}

func TestACookieIsRenewedAtMostOnceADay(t *testing.T) {
	db, svc, c, uid := setup(t)
	_, s, err := session.Create(t.Context(), db, c.t, uid, "")
	require.NoError(t, err)

	won, err := svc.ClaimCookieRenewal(t.Context(), s.ID)
	require.NoError(t, err)
	require.False(t, won, "the cookie was just sent")

	c.t = c.t.Add(session.CookieRenewInterval + time.Minute)
	won, err = svc.ClaimCookieRenewal(t.Context(), s.ID)
	require.NoError(t, err)
	require.True(t, won)
	won, err = svc.ClaimCookieRenewal(t.Context(), s.ID)
	require.NoError(t, err)
	require.False(t, won, "the second request of the day")
}
