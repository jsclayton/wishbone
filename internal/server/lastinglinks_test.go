package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/session"
)

// These tests cover lasting login links (docs/spec.md, Access, Lasting login
// links). Only the CLI makes one, and the CLI is a thin shell over
// loginlink.Service, so they make links with the service on the env's database
// and redeem them through the API, as App Review would.

// shownLasting is a lasting link's token as the CLI prints it: four groups of
// four characters of the alphabet, with hyphens.
var shownLasting = regexp.MustCompile(`^[23456789abcdefghijkmnpqrstuvwxyz]{4}(-[23456789abcdefghijkmnpqrstuvwxyz]{4}){3}$`)

// makeLasting makes a lasting link for a user, as `wishbone login-link
// --lasting` does, and returns the token and the link's ID.
func (e *env) makeLasting(userID string) (token string, id int64) {
	e.t.Helper()
	token, link, err := e.srv.opts.LoginLinks.CreateLasting(e.t.Context(), mustParse(e.t, userID))
	require.NoError(e.t, err)
	require.Regexp(e.t, shownLasting, token)
	return token, link.ID
}

// signedIn says whether a session token still signs its caller in.
func (e *env) signedIn(header http.Header) bool {
	e.t.Helper()
	return e.do(request{path: "/users/me", header: header}).StatusCode == http.StatusOK
}

func TestALastingLinkWorksAnyNumberOfTimesAndEachRedeemStartsASession(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	graceID, _ := e.otherUser("Grace")
	link, linkID := e.makeLasting(graceID)

	first := e.redeem(link, "", nil)
	second := e.redeem(link, "bearer", nil)
	third := e.redeem(link, "", nil)

	var tokens []string
	for _, res := range []*http.Response{first, third} {
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.NotNil(t, sessionCookie(res))
		tokens = append(tokens, sessionCookie(res).Value)
		require.Equal(t, graceID, decode[redeemBody](t, res).UserID)
	}
	require.Equal(t, http.StatusOK, second.StatusCode)
	b := decode[redeemBody](t, second)
	require.Equal(t, "Grace", b.Name)
	tokens = append(tokens, b.Token)
	require.NotEqual(t, tokens[0], tokens[1])
	require.True(t, e.signedIn(cookie(tokens[0])), "an earlier redeem's session stays")
	require.True(t, e.signedIn(cookie(tokens[1])))
	require.True(t, e.signedIn(bearer(tokens[2])))
	// Each session records the link that started it, and shows in the list.
	require.Equal(t, 3, e.rowCount("sessions", `lasting_link_id = ?`, linkID))
	require.Equal(t, 4, decode[sessionList](t, e.listSessions(graceID, cookie(adminToken))).TotalCount,
		"the three, and the session Grace had")
	require.Zero(t, e.rowCount("sessions", `user_id = ? AND lasting_link_id IS NOT NULL`, adminID))
}

func TestTheDatabaseKeepsOnlyTheHashOfALastingLinksToken(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, _ := e.signUp()
	link, _ := e.makeLasting(adminID)

	var stored string
	require.NoError(t, e.db.GetContext(t.Context(), &stored, `SELECT token_hash FROM lasting_links`))
	require.Equal(t, session.Hash(strings.ReplaceAll(link, "-", "")), stored, "the hash of its canonical form")
	require.NotContains(t, stored, strings.ReplaceAll(link, "-", ""))
}

func TestTheAdminMayHaveALastingLink(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, _ := e.signUp()
	link, _ := e.makeLasting(adminID)

	for range 2 {
		res := e.redeem(link, "bearer", nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, adminID, decode[redeemBody](t, res).UserID)
	}
}

func TestALastingLinkNeverExpires(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, _ := e.signUp()
	link, _ := e.makeLasting(adminID)

	for _, wait := range []time.Duration{7*24*time.Hour + time.Second, 400 * 24 * time.Hour, 5 * 365 * 24 * time.Hour} {
		e.clock.advance(wait)
		require.Equal(t, http.StatusOK, e.check(link, nil).StatusCode, wait)
		require.Equal(t, http.StatusOK, e.redeem(link, "", nil).StatusCode, wait)
	}
}

// A lasting link's token is typed from the App Review notes, so it is taken in
// any case and with or without its hyphens.
func TestALastingLinkTakesItsTokenInAnyCaseWithOrWithoutHyphens(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 50 })
	adminID, _ := e.signUp()
	link, _ := e.makeLasting(adminID)
	bare := strings.ReplaceAll(link, "-", "")

	for _, form := range []string{link, bare, strings.ToUpper(link), strings.ToUpper(bare), strings.ToUpper(bare[:8]) + "-" + bare[8:]} {
		require.Equal(t, http.StatusOK, e.check(form, nil).StatusCode, form)
		require.Equal(t, http.StatusOK, e.redeem(form, "bearer", nil).StatusCode, form)
	}

	// One character off is another token. A character outside the alphabet, or a
	// length other than 16, is not a lasting link's token at all.
	other := []byte(bare)
	other[15] = loginlink.LastingAlphabet[(strings.IndexByte(loginlink.LastingAlphabet, other[15])+1)%32]
	for _, form := range []string{string(other), bare[:15], bare + "2", "0" + bare[1:], bare[:15] + "l", link + " "} {
		res := e.redeem(form, "", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, form)
		require.Equal(t, "login_link_invalid", readProblem(t, res).Code, form)
	}
}

func TestRevokingALastingLinkEndsTheSessionsItStartedAndNoOthers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	graceID, graceToken := e.otherUser("Grace")
	leaked, leakedID := e.makeLasting(graceID)
	kept, _ := e.makeLasting(graceID)
	adminLink, _ := e.makeLasting(adminID)
	fromLeaked := []http.Header{
		cookie(sessionCookie(e.redeem(leaked, "", nil)).Value),
		bearer(decode[redeemBody](t, e.redeem(leaked, "bearer", nil)).Token),
	}
	fromKept := bearer(decode[redeemBody](t, e.redeem(kept, "bearer", nil)).Token)
	fromOrdinary := bearer(decode[redeemBody](t, e.redeem(e.makeLink(adminToken, graceID), "bearer", nil)).Token)
	fromAdminLink := bearer(decode[redeemBody](t, e.redeem(adminLink, "bearer", nil)).Token)
	// Each of the link's sessions, and one that stays, keeps the answer to a
	// write with a key.
	listID := e.primaryListOf(graceToken).ID
	for _, h := range append([]http.Header{fromKept}, fromLeaked...) {
		h = h.Clone()
		h.Set("Idempotency-Key", "key-1")
		res := e.do(request{method: http.MethodPost, path: "/lists/" + listID + "/items", body: map[string]any{"title": "Kettle"}, header: h})
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}
	require.Equal(t, 3, e.storedKeys())

	link, ended, err := e.srv.opts.LoginLinks.RevokeLasting(t.Context(), leakedID)

	require.NoError(t, err)
	require.Equal(t, mustParse(t, graceID), link.UserID)
	require.Equal(t, 2, ended)
	for _, h := range fromLeaked {
		require.False(t, e.signedIn(h), "a session the link started ends")
	}
	for name, h := range map[string]http.Header{
		"another lasting link's": fromKept, "an ordinary link's": fromOrdinary, "the one Grace had": cookie(graceToken),
		"the admin's lasting link's": fromAdminLink, "the admin's own": cookie(adminToken),
	} {
		require.True(t, e.signedIn(h), name)
	}
	var ofLeaked int
	require.NoError(t, e.db.GetContext(t.Context(), &ofLeaked,
		`SELECT count(*) FROM idempotency_keys k JOIN sessions s ON s.id = k.session_id WHERE s.lasting_link_id = ?`, leakedID))
	require.Zero(t, ofLeaked, "the kept answers of the sessions that ended are gone")
	require.Equal(t, 1, e.storedKeys(), "the other session keeps its answer")
	for _, res := range []*http.Response{e.check(leaked, nil), e.redeem(leaked, "", nil)} {
		requireCoded(t, res, http.StatusUnauthorized, "login_link_invalid")
	}
	require.Equal(t, http.StatusOK, e.redeem(kept, "", nil).StatusCode)

	_, _, err = e.srv.opts.LoginLinks.RevokeLasting(t.Context(), leakedID)
	require.ErrorIs(t, err, loginlink.ErrNotFound, "a link is revoked once")
	_, _, err = e.srv.opts.LoginLinks.RevokeLasting(t.Context(), 999)
	require.ErrorIs(t, err, loginlink.ErrNotFound)
}

func TestTheOperatorListsTheLastingLinksThatAreNotRevoked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, _ := e.signUp()
	graceID, _ := e.otherUser("Grace")
	made := e.clock.now()
	used, usedID := e.makeLasting(graceID)
	_, revokedID := e.makeLasting(graceID)
	_, adminLinkID := e.makeLasting(adminID)
	e.clock.advance(time.Hour)
	require.Equal(t, http.StatusOK, e.redeem(used, "", nil).StatusCode)
	_, _, err := e.srv.opts.LoginLinks.RevokeLasting(t.Context(), revokedID)
	require.NoError(t, err)

	links, err := e.srv.opts.LoginLinks.ListLasting(t.Context())

	require.NoError(t, err)
	usedAt := made.Add(time.Hour)
	require.Equal(t, []loginlink.Lasting{
		{ID: usedID, UserID: mustParse(t, graceID), CreatedAt: made, LastUsedAt: &usedAt},
		{ID: adminLinkID, UserID: mustParse(t, adminID), CreatedAt: made},
	}, links)
}

func TestALastingLinkOfADeactivatedUserDoesNotWorkUntilTheyAreReactivated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, adminToken := e.signUp()
	graceID, _ := e.otherUser("Grace")
	link, _ := e.makeLasting(graceID)
	before := decode[redeemBody](t, e.redeem(link, "bearer", nil)).Token

	e.deactivated(adminToken, graceID)

	require.False(t, e.signedIn(bearer(before)), "deactivation ends the sessions the link started, as it ends all of hers")
	for _, res := range []*http.Response{e.check(link, nil), e.redeem(link, "", nil)} {
		requireCoded(t, res, http.StatusUnauthorized, "login_link_invalid")
	}
	links, err := e.srv.opts.LoginLinks.ListLasting(t.Context())
	require.NoError(t, err)
	require.Len(t, links, 1, "the link stays, for when she is back")

	require.Equal(t, http.StatusOK, e.reactivate(adminToken, graceID).StatusCode)

	require.Equal(t, http.StatusOK, e.check(link, nil).StatusCode)
	after := decode[redeemBody](t, e.redeem(link, "bearer", nil)).Token
	require.True(t, e.signedIn(bearer(after)), "it works again")
	require.False(t, e.signedIn(bearer(before)), "the sessions that were revoked don't come back")
}

func TestLastingLinkRedeemsCountAgainstTheRedeemLimit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 2 })
	adminID, _ := e.signUp() // setup used an attempt of this minute
	link, _ := e.makeLasting(adminID)

	e.clock.advance(61 * time.Second)
	for range 2 {
		require.Equal(t, http.StatusOK, e.redeem(link, "bearer", nil).StatusCode)
	}
	res := e.redeem(link, "bearer", nil)

	requireCoded(t, res, http.StatusTooManyRequests, "rate_limited")
	e.clock.advance(61 * time.Second)
	require.Equal(t, http.StatusOK, e.redeem(link, "bearer", nil).StatusCode, "a new minute")
}

// checkFields is a check response as JSON, so a test sees a null.
type checkFields map[string]json.RawMessage

func TestCheckingALastingLinkSaysItDoesNotExpireAndDoesNotUseIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	link, _ := e.makeLasting(adminID)

	res := e.check(link, cookie(adminToken))

	require.Equal(t, http.StatusOK, res.StatusCode)
	var b checkFields
	decodeInto(t, res, &b)
	require.Equal(t, checkFields{
		"expires_at": json.RawMessage(`null`), "lasting": json.RawMessage(`true`), "for_current_user": json.RawMessage(`true`),
	}, withoutSchema(b))
	require.Zero(t, e.rowCount("lasting_links", `last_used_at IS NOT NULL`), "a check is not a use")
	_, graceToken := e.otherUser("Grace")
	require.False(t, decode[checkBody](t, e.check(link, cookie(graceToken))).ForCurrentUser)
}

// An ordinary login link is as it was: its check has an expiry, and it works
// once, for seven days. The API route makes only ordinary links.
func TestAnOrdinaryLinkIsUnchangedByLastingLinks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	once := e.makeLink(adminToken, adminID)
	require.NotRegexp(t, shownLasting, once)

	var b checkFields
	decodeInto(t, e.check(once, nil), &b)
	require.Equal(t, json.RawMessage(`false`), b["lasting"])
	require.Equal(t, `"`+e.clock.now().Add(7*24*time.Hour).Format(time.RFC3339)+`"`, string(b["expires_at"]))
	require.Equal(t, http.StatusOK, e.redeem(once, "", nil).StatusCode)
	requireCoded(t, e.redeem(once, "", nil), http.StatusUnauthorized, "login_link_invalid")

	expired := e.makeLink(adminToken, adminID)
	e.clock.advance(7*24*time.Hour + time.Second)
	requireCoded(t, e.redeem(expired, "", nil), http.StatusUnauthorized, "login_link_invalid")
	require.Zero(t, e.rowCount("sessions", `lasting_link_id IS NOT NULL`), "no session records a lasting link")
}

// withoutSchema drops huma's $schema link from a response body.
func withoutSchema(b checkFields) checkFields {
	delete(b, "$schema")
	return b
}
