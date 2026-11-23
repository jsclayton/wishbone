package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
	"jsclayton.dev/wishbone/internal/session"
)

// These tests cover what a lasting login link's sessions make (docs/spec.md,
// Access, Lasting login links). A passkey or a login link that one of them
// makes records the link, and so does a session that such a passkey or login
// link starts, so a revoke finds all of them, at any distance.

// sessionLink is the lasting link that the session of a token records, or 0.
func (e *env) sessionLink(token string) int64 {
	e.t.Helper()
	var id *int64
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &id, `SELECT lasting_link_id FROM sessions WHERE token_hash = ?`, session.Hash(token)))
	if id == nil {
		return 0
	}
	return *id
}

// passkeyLink is the lasting link that a user's passkey of that name records,
// or 0.
func (e *env) passkeyLink(userID, name string) int64 {
	e.t.Helper()
	var id *int64
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &id,
		`SELECT lasting_link_id FROM passkeys WHERE user_id = ? AND name = ?`, mustParse(e.t, userID), name))
	if id == nil {
		return 0
	}
	return *id
}

// loginLinkRow is a login link's row, found by its token.
type loginLinkRow struct {
	LastingLinkID *int64     `db:"lasting_link_id"`
	ExpiresAt     time.Time  `db:"expires_at"`
	RedeemedAt    *time.Time `db:"redeemed_at"`
}

func (e *env) loginLinkRow(token string) loginLinkRow {
	e.t.Helper()
	var r loginLinkRow
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &r,
		`SELECT lasting_link_id, expires_at, redeemed_at FROM login_links WHERE token_hash = ?`, session.Hash(token)))
	return r
}

// signInWithPasskey signs in with the authenticator's passkey and returns the
// new session's token, or "" when the sign-in fails.
func (e *env) signInWithPasskey(a *passkeytest.Authenticator) string {
	e.t.Helper()
	res := e.passkeyLogin(a.Assert(e.t, e.loginOptions(), passkeyOrigin), nil)
	if res.StatusCode != http.StatusNoContent {
		requireCoded(e.t, res, http.StatusUnauthorized, "passkey_login_failed")
		return ""
	}
	return sessionCookie(res).Value
}

func TestAPasskeyRecordsTheLastingLinkOfTheSessionThatAddedIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.signUp()
	graceID, graceToken := e.otherUser("Grace")
	link, linkID := e.makeLasting(graceID)
	fromLink := decode[redeemBody](t, e.redeem(link, "bearer", nil)).Token
	require.Equal(t, linkID, e.sessionLink(fromLink))

	require.Equal(t, http.StatusCreated, e.registerPasskey(graceID, bearer(fromLink), passkeytest.New(t), "Reviewer").StatusCode)
	require.Equal(t, http.StatusCreated, e.registerPasskey(graceID, cookie(graceToken), passkeytest.New(t), "Own").StatusCode)

	require.Equal(t, linkID, e.passkeyLink(graceID, "Reviewer"), "one that the link's session added")
	require.Zero(t, e.passkeyLink(graceID, "Own"), "one that an ordinary session added")
}

// The link passes down the chain: a login link that the link's session makes,
// the session it starts, a passkey that session adds, the session that passkey
// starts, and a login link that one makes.
func TestWhatALastingLinksSessionsMakeRecordsTheLinkAtAnyDistance(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 100 })
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	lasting, linkID := e.makeLasting(momID)
	fromLasting := decode[redeemBody](t, e.redeem(lasting, "bearer", nil)).Token

	forEmma := e.makeLink(fromLasting, emmaID)
	require.Equal(t, &linkID, e.loginLinkRow(forEmma).LastingLinkID, "one step: a login link")
	emmaFromLink := decode[redeemBody](t, e.redeem(forEmma, "bearer", nil)).Token
	require.Equal(t, linkID, e.sessionLink(emmaFromLink), "two steps: the session it starts")
	phone := passkeytest.New(t)
	require.Equal(t, http.StatusCreated, e.registerPasskey(emmaID, bearer(emmaFromLink), phone, "Phone").StatusCode)
	require.Equal(t, linkID, e.passkeyLink(emmaID, "Phone"), "three steps: a passkey that session adds")
	emmaFromPasskey := e.signInWithPasskey(phone)
	require.Equal(t, linkID, e.sessionLink(emmaFromPasskey), "four steps: the session the passkey starts")
	again := e.makeLink(emmaFromPasskey, emmaID)
	require.Equal(t, &linkID, e.loginLinkRow(again).LastingLinkID, "five steps: a login link that session makes")

	// What an ordinary session makes records no link.
	ordinary := e.makeLink(mom, emmaID)
	require.Nil(t, e.loginLinkRow(ordinary).LastingLinkID)
	require.Zero(t, e.sessionLink(decode[redeemBody](t, e.redeem(ordinary, "bearer", nil)).Token))
	require.Nil(t, e.loginLinkRow(e.makeLink(admin, emmaID)).LastingLinkID)
}

// leakedFamily is Mom, who has a lasting link that leaked, and her child Emma.
// The link's session added a passkey for Mom and made two login links for
// Emma. Emma redeemed one, added a passkey with that session, signed in with
// the passkey, and made a login link for herself. Mom's own session added a
// passkey and made a login link for Emma, and Emma has a session of her own:
// none of these record the link.
type leakedFamily struct {
	momID, emmaID, admin, mom, emma        string
	lasting                                string
	linkID                                 int64
	fromLasting, emmaFromLink, emmaPasskey string
	leaked, emmaPhone, momOwn              *passkeytest.Authenticator
	redeemed, unused, emmaSelf, ordinary   string
}

func (e *env) leakedFamily() leakedFamily {
	e.t.Helper()
	var f leakedFamily
	_, f.admin = e.signUp()
	f.momID, f.mom = e.otherUser("Mom")
	f.emmaID, f.emma = e.otherUser("Emma")
	e.linkParents(f.admin, f.emmaID, f.momID)
	f.lasting, f.linkID = e.makeLasting(f.momID)
	f.fromLasting = decode[redeemBody](e.t, e.redeem(f.lasting, "bearer", nil)).Token

	f.leaked, f.emmaPhone, f.momOwn = passkeytest.New(e.t), passkeytest.New(e.t), passkeytest.New(e.t)
	require.Equal(e.t, http.StatusCreated, e.registerPasskey(f.momID, bearer(f.fromLasting), f.leaked, "Leaked").StatusCode)
	f.redeemed = e.makeLink(f.fromLasting, f.emmaID)
	f.unused = e.makeLink(f.fromLasting, f.emmaID)
	f.emmaFromLink = decode[redeemBody](e.t, e.redeem(f.redeemed, "bearer", nil)).Token
	require.Equal(e.t, http.StatusCreated, e.registerPasskey(f.emmaID, bearer(f.emmaFromLink), f.emmaPhone, "Emma's phone").StatusCode)
	f.emmaPasskey = e.signInWithPasskey(f.emmaPhone)
	f.emmaSelf = e.makeLink(f.emmaPasskey, f.emmaID)

	require.Equal(e.t, http.StatusCreated, e.registerPasskey(f.momID, cookie(f.mom), f.momOwn, "Mom's own").StatusCode)
	f.ordinary = e.makeLink(f.mom, f.emmaID)
	return f
}

func TestRevokingALastingLinkRemovesWhatItsSessionsMadeAndNothingElse(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 100 })
	f := e.leakedFamily()
	redeemedBefore := e.loginLinkRow(f.redeemed)
	e.clock.advance(time.Hour)

	revoked, err := e.srv.opts.LoginLinks.RevokeLasting(t.Context(), f.linkID)

	require.NoError(t, err)
	require.Equal(t, []loginlink.Removed{
		{UserID: mustParse(t, f.momID), SessionsEnded: 1, PasskeysDeleted: []string{"Leaked"}, PasskeysLeft: 1},
		{UserID: mustParse(t, f.emmaID), SessionsEnded: 2, PasskeysDeleted: []string{"Emma's phone"}, LoginLinksEnded: 2},
	}, revoked.Users)
	require.False(t, revoked.Link.PredatesProvenance)

	// Every session that records the link ends.
	for name, h := range map[string]http.Header{
		"the link's own": bearer(f.fromLasting), "Emma's from its login link": bearer(f.emmaFromLink),
		"Emma's from the passkey": cookie(f.emmaPasskey),
	} {
		require.False(t, e.signedIn(h), name)
	}
	// Its passkeys are gone, and no longer sign anyone in.
	require.Zero(t, e.rowCount("passkeys", `lasting_link_id = ?`, f.linkID))
	require.Empty(t, e.signInWithPasskey(f.leaked), "Mom's leaked passkey")
	require.Empty(t, e.signInWithPasskey(f.emmaPhone), "Emma's passkey from the link's chain")
	// Its unused login links end, and can't be redeemed. The redeemed one is as
	// it was.
	for _, token := range []string{f.unused, f.emmaSelf} {
		requireCoded(t, e.redeem(token, "", nil), http.StatusUnauthorized, "login_link_invalid")
		row := e.loginLinkRow(token)
		require.Nil(t, row.RedeemedAt, "an ended link is not marked used")
		require.Equal(t, e.clock.now(), row.ExpiresAt.UTC(), "it expired at the revoke")
	}
	require.Equal(t, redeemedBefore, e.loginLinkRow(f.redeemed), "a redeemed link isn't touched")

	// What the users' other sessions made stays.
	require.True(t, e.signedIn(cookie(f.mom)), "Mom's own session")
	require.True(t, e.signedIn(cookie(f.emma)), "Emma's own session")
	require.NotEmpty(t, e.signInWithPasskey(f.momOwn), "Mom's own passkey")
	require.Equal(t, http.StatusOK, e.redeem(f.ordinary, "", nil).StatusCode, "the login link Mom's own session made")
}

// Every write of a revoke is in one commit: a failure at the last step leaves
// the link, its sessions, its passkeys and its login links as they were.
func TestARevokeThatFailsPartwayRemovesNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 100 })
	f := e.leakedFamily()
	_, err := e.db.ExecContext(t.Context(),
		`CREATE TRIGGER refuse_end BEFORE UPDATE OF expires_at ON login_links BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)

	_, err = e.srv.opts.LoginLinks.RevokeLasting(t.Context(), f.linkID)

	require.ErrorContains(t, err, "refused")
	require.Zero(t, e.rowCount("lasting_links", `revoked_at IS NOT NULL`))
	require.True(t, e.signedIn(bearer(f.fromLasting)))
	require.True(t, e.signedIn(bearer(f.emmaFromLink)))
	require.Equal(t, 2, e.rowCount("passkeys", `lasting_link_id = ?`, f.linkID))
	require.Equal(t, 2, e.rowCount("login_links", `lasting_link_id = ? AND redeemed_at IS NULL`, f.linkID))
	_, err = e.db.ExecContext(t.Context(), `DROP TRIGGER refuse_end`)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, e.redeem(f.lasting, "", nil).StatusCode, "the link still works")
	require.NotEmpty(t, e.signInWithPasskey(f.leaked))
}

// Deleting the user whose lasting link it was deletes the link, and clears it
// from what records it: another user's passkeys, sessions and login links stay.
func TestDeletingTheLinksUserLeavesWhatItsSessionsMadeForOthers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 100 })
	f := e.leakedFamily()

	e.deleteUser(f.momID)

	require.Zero(t, e.rowCount("lasting_links", `user_id = ?`, f.momID))
	require.True(t, e.signedIn(bearer(f.emmaFromLink)), "Emma's session from the link's chain")
	require.True(t, e.signedIn(cookie(f.emmaPasskey)))
	require.Equal(t, 1, e.rowCount("passkeys", `user_id = ?`, f.emmaID))
	require.Zero(t, e.rowCount("sessions", `lasting_link_id IS NOT NULL`))
	require.Zero(t, e.rowCount("passkeys", `lasting_link_id IS NOT NULL`))
	require.Zero(t, e.rowCount("login_links", `lasting_link_id IS NOT NULL`))
	require.NotEmpty(t, e.signInWithPasskey(f.emmaPhone), "Emma's passkey still signs her in")
	require.Equal(t, http.StatusOK, e.redeem(f.unused, "", nil).StatusCode, "and the login link for her works")
}

// A revoke that commits after a session signed in, but before its write, wins:
// nothing that records the revoked link is made.
func TestNothingRecordsALinkAfterItsRevoke(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 100 })
	f := e.leakedFamily()
	momID, emmaID := mustParse(t, f.momID), mustParse(t, f.emmaID)
	_, err := e.srv.opts.LoginLinks.RevokeLasting(t.Context(), f.linkID)
	require.NoError(t, err)

	_, _, err = e.srv.opts.LoginLinks.Create(t.Context(), emmaID, f.linkID)
	require.ErrorIs(t, err, session.ErrLastingLinkRevoked, "a login link")
	_, _, _, err = e.srv.opts.Sessions.SignIn(t.Context(), emmaID, f.linkID, 0, "ua")
	require.ErrorIs(t, err, session.ErrLastingLinkRevoked, "a passkey sign-in")
	opts, err := e.srv.opts.Passkeys.BeginRegistration(t.Context(), momID)
	require.NoError(t, err)
	_, err = e.srv.opts.Passkeys.FinishRegistration(t.Context(), momID, f.linkID, "Late",
		json.RawMessage(passkeytest.New(t).Create(t, opts, passkeyOrigin)))
	require.ErrorIs(t, err, session.ErrLastingLinkRevoked, "a passkey")
	require.Zero(t, e.rowCount("passkeys", `name = 'Late'`))
	require.Zero(t, e.rowCount("sessions", `lasting_link_id = ? AND revoked_at IS NULL`, f.linkID))
	require.Zero(t, e.rowCount("login_links", `lasting_link_id = ? AND redeemed_at IS NULL AND expires_at > ?`, f.linkID, e.clock.now()))
}
