package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const linkPrefix = "https://wish.example/login/"

// linkBody is the create response.
type linkBody struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// makeLink makes a login link for a user as the caller, and returns the token.
func (e *env) makeLink(callerToken, userID string) string {
	e.t.Helper()
	res := e.do(request{method: http.MethodPost, path: "/users/" + userID + "/login-links", header: cookie(callerToken)})
	require.Equal(e.t, http.StatusCreated, res.StatusCode)
	b := decode[linkBody](e.t, res)
	require.True(e.t, strings.HasPrefix(b.URL, linkPrefix), b.URL)
	return strings.TrimPrefix(b.URL, linkPrefix)
}

func (e *env) redeem(token, session string, header http.Header) *http.Response {
	e.t.Helper()
	body := map[string]any{"token": token}
	if session != "" {
		body["session"] = session
	}
	return e.do(request{method: http.MethodPost, path: "/login-links/redeem", body: body, header: header})
}

type redeemBody struct {
	Token           string `json:"token"`
	Name            string `json:"name"`
	Initials        string `json:"initials"`
	ReplacedSession bool   `json:"replaced_session"`
	SwitchedUser    bool   `json:"switched_user"`
}

func TestAUserMakesALoginLinkForThemselves(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()

	res := e.do(request{method: http.MethodPost, path: "/users/" + id + "/login-links", header: cookie(token)})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Empty(t, res.Header.Get("Location"), "a login link has no address")
	b := decode[linkBody](t, res)
	require.True(t, strings.HasPrefix(b.URL, linkPrefix))
	require.Equal(t, e.clock.now().Add(7*24*time.Hour), b.ExpiresAt)
	// Only the hash is stored.
	var stored string
	require.NoError(t, e.db.GetContext(t.Context(), &stored, `SELECT token_hash FROM login_links`))
	require.NotContains(t, b.URL, stored)
	require.NotEqual(t, strings.TrimPrefix(b.URL, linkPrefix), stored)
	require.Len(t, stored, 64)
}

func TestMeMakesALoginLinkToo(t *testing.T) {
	e := newEnv(t)
	_, token := e.signUp()

	res := e.do(request{method: http.MethodPost, path: "/users/me/login-links", header: cookie(token)})

	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestTheAdminMakesALoginLinkForAnyUserButAUserOnlyForThemselves(t *testing.T) {
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	otherID, otherToken := e.otherUser("Grace")

	res := e.do(request{method: http.MethodPost, path: "/users/" + otherID + "/login-links", header: cookie(adminToken)})
	require.Equal(t, http.StatusCreated, res.StatusCode)

	// A user who is not the admin can't see another user's sign-in, so it is a 404.
	res = e.do(request{method: http.MethodPost, path: "/users/" + adminID + "/login-links", header: cookie(otherToken)})
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", readProblem(t, res).Code)

	res = e.do(request{method: http.MethodPost, path: "/users/999/login-links", header: cookie(adminToken)})
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	var n int
	require.NoError(t, e.db.GetContext(t.Context(), &n, `SELECT count(*) FROM login_links`))
	require.Equal(t, 1, n)
}

func TestMakingALoginLinkNeedsASession(t *testing.T) {
	e := newEnv(t)
	id, _ := e.signUp()

	res := e.do(request{method: http.MethodPost, path: "/users/" + id + "/login-links"})

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "unauthorized", readProblem(t, res).Code)
}

func TestRedeemingALoginLinkStartsACookieSession(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	link := e.makeLink(token, id)

	res := e.redeem(link, "", nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	c := sessionCookie(res)
	require.NotNil(t, c)
	require.True(t, c.HttpOnly)
	require.True(t, c.Secure)
	require.NotEmpty(t, c.Value)
	raw := decode[map[string]any](t, res)
	require.NotContains(t, raw, "token", "a cookie session puts no token in the body")
	require.Equal(t, "Ada", raw["name"])
	require.Equal(t, "A", raw["initials"])
	require.Equal(t, false, raw["replaced_session"])
	require.Equal(t, false, raw["switched_user"])
	me := e.do(request{path: "/users/me", header: cookie(c.Value)})
	require.Equal(t, http.StatusOK, me.StatusCode)
}

func TestRedeemingALoginLinkWithABearerSessionGivesATokenAndNoCookie(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	link := e.makeLink(token, id)

	res := e.redeem(link, "bearer", http.Header{"User-Agent": {"Wishbone iOS/1.0"}})

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Nil(t, sessionCookie(res))
	require.Empty(t, res.Header.Get("Set-Cookie"))
	b := decode[redeemBody](t, res)
	require.NotEmpty(t, b.Token)
	require.Equal(t, "Ada", b.Name)
	require.Equal(t, "A", b.Initials)
	me := e.do(request{path: "/users/me", header: bearer(b.Token)})
	require.Equal(t, http.StatusOK, me.StatusCode)
	var agent string
	require.NoError(t, e.db.GetContext(t.Context(), &agent, `SELECT user_agent FROM sessions ORDER BY id DESC LIMIT 1`))
	require.Equal(t, "Wishbone iOS/1.0", agent)
}

func TestARedeemWithAnUnknownSessionKindIs422(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	link := e.makeLink(token, id)

	res := e.redeem(link, "carrier-pigeon", nil)

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
	// The link is not used up.
	require.Equal(t, http.StatusOK, e.redeem(link, "", nil).StatusCode)
}

func TestALoginLinkWorksOnce(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	link := e.makeLink(token, id)

	require.Equal(t, http.StatusOK, e.redeem(link, "", nil).StatusCode)
	res := e.redeem(link, "", nil)

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "login_link_invalid", readProblem(t, res).Code)
	require.Nil(t, sessionCookie(res))
}

func TestALoginLinkExpiresAfterSevenDays(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	link := e.makeLink(token, id)

	e.clock.advance(7*24*time.Hour + time.Second)
	res := e.redeem(link, "", nil)

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "login_link_invalid", readProblem(t, res).Code)
}

func TestAnUnknownLinkGetsTheSameAnswerAsAUsedOne(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	for _, token := range []string{"nope", "x"} {
		res := e.redeem(token, "", nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, token)
		require.Equal(t, "login_link_invalid", readProblem(t, res).Code)
	}
}

func TestRedeemingReplacesTheCallersCurrentSession(t *testing.T) {
	e := newEnv(t)
	id, adminToken := e.signUp()
	otherID, otherToken := e.otherUser("Grace")
	same := e.makeLink(adminToken, id)
	other := e.makeLink(adminToken, otherID)

	// The same user: the old cookie session is revoked.
	res := e.redeem(same, "", cookie(adminToken))
	require.Equal(t, http.StatusOK, res.StatusCode)
	b := decode[redeemBody](t, res)
	require.True(t, b.ReplacedSession)
	require.False(t, b.SwitchedUser)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(adminToken)}).StatusCode)

	// Another user: it counts as a switch.
	newAdmin := sessionCookie(res).Value
	res = e.redeem(other, "", cookie(newAdmin))
	b = decode[redeemBody](t, res)
	require.True(t, b.ReplacedSession)
	require.True(t, b.SwitchedUser)
	require.Equal(t, "Grace", b.Name)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(newAdmin)}).StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(otherToken)}).StatusCode, "the user's other sessions stay")
}

func TestRedeemingRevokesOnlyACurrentSessionOfTheSameKind(t *testing.T) {
	e := newEnv(t)
	id, cookieToken := e.signUp()
	link := func() string { return e.makeLink(cookieToken, id) }

	// A cookie redeem by a bearer caller leaves the bearer session alone, and
	// the other way round.
	bearerToken := decode[redeemBody](t, e.redeem(link(), "bearer", nil)).Token
	res := e.redeem(link(), "", bearer(bearerToken))
	require.False(t, decode[redeemBody](t, res).ReplacedSession)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: bearer(bearerToken)}).StatusCode)

	res = e.redeem(link(), "bearer", cookie(cookieToken))
	require.False(t, decode[redeemBody](t, res).ReplacedSession)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(cookieToken)}).StatusCode)

	// A bearer redeem by a bearer caller replaces it.
	res = e.redeem(link(), "bearer", bearer(bearerToken))
	b := decode[redeemBody](t, res)
	require.True(t, b.ReplacedSession)
	require.NotEqual(t, bearerToken, b.Token)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: bearer(bearerToken)}).StatusCode)
}

func TestARedeemThatFailsKeepsTheCallersSession(t *testing.T) {
	e := newEnv(t)
	_, token := e.signUp()

	res := e.redeem("nope", "", cookie(token))

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(token)}).StatusCode)
}

func TestRedeemAttemptsCountAgainstTheRedeemLimit(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 2 })
	e.signUp() // setup is not counted here: it used one attempt of the minute

	e.clock.advance(61 * time.Second)
	for range 2 {
		require.Equal(t, http.StatusUnauthorized, e.redeem("nope", "", nil).StatusCode)
	}
	res := e.redeem("nope", "", nil)

	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, res).Code)
	require.NotEmpty(t, res.Header.Get("Retry-After"))
}

type checkBody struct {
	ExpiresAt      time.Time `json:"expires_at"`
	ForCurrentUser bool      `json:"for_current_user"`
}

func (e *env) check(token string, header http.Header) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/login-links/check", body: map[string]any{"token": token}, header: header})
}

func TestCheckingALoginLinkDoesNotUseItUp(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	link := e.makeLink(token, id)

	res := e.check(link, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
	b := decode[checkBody](t, res)
	require.Equal(t, e.clock.now().Add(7*24*time.Hour), b.ExpiresAt)
	require.False(t, b.ForCurrentUser)

	res = e.check(link, cookie(token))
	require.True(t, decode[checkBody](t, res).ForCurrentUser)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(token)}).StatusCode, "a check keeps the session")

	_, otherToken := e.otherUser("Grace")
	require.False(t, decode[checkBody](t, e.check(link, cookie(otherToken))).ForCurrentUser)

	require.Equal(t, http.StatusOK, e.redeem(link, "", nil).StatusCode, "the link is still unused")
}

func TestCheckingABadLinkIs401WithACode(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	used := e.makeLink(token, id)
	require.Equal(t, http.StatusOK, e.redeem(used, "", nil).StatusCode)
	expired := e.makeLink(token, id)
	e.clock.advance(8 * 24 * time.Hour)

	for name, link := range map[string]string{"used": used, "expired": expired, "unknown": "nope", "empty": ""} {
		res := e.check(link, nil)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, name)
		require.Equal(t, "login_link_invalid", readProblem(t, res).Code, name)
		require.Equal(t, "no-store", res.Header.Get("Cache-Control"), name)
	}
}

func TestLinkChecksHaveALimitOfTheirOwn(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 2 })

	for range 2 {
		require.Equal(t, http.StatusUnauthorized, e.check("nope", nil).StatusCode)
	}
	res := e.check("nope", nil)

	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
	require.Equal(t, "no-store", res.Header.Get("Cache-Control"))
	// Redeeming has its own count.
	require.Equal(t, http.StatusUnauthorized, e.redeem("nope", "", nil).StatusCode)
}

func TestABearerSessionLastsAYearAfterItsLastUse(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	b := decode[redeemBody](t, e.redeem(e.makeLink(token, id), "bearer", nil))

	for range 3 {
		e.clock.advance(300 * 24 * time.Hour)
		require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: bearer(b.Token)}).StatusCode)
	}
	e.clock.advance(366 * 24 * time.Hour)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: bearer(b.Token)}).StatusCode)
}
