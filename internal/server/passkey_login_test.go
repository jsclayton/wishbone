package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
	"jsclayton.dev/wishbone/internal/session"
)

// passkeyUser makes Grace, a user with one passkey, and returns her ID, her
// session token, and the authenticator that holds the passkey.
func (e *env) passkeyUser() (id, token string, a *passkeytest.Authenticator) {
	e.t.Helper()
	id, token = e.otherUser("Grace")
	a = passkeytest.New(e.t)
	require.Equal(e.t, http.StatusCreated, e.registerPasskey(id, cookie(token), a, "Laptop").StatusCode)
	return id, token, a
}

func (e *env) loginOptions() []byte {
	e.t.Helper()
	res := e.do(request{method: http.MethodPost, path: "/login/passkey/options"})
	require.Equal(e.t, http.StatusOK, res.StatusCode)
	b, err := io.ReadAll(res.Body)
	require.NoError(e.t, err)
	return b
}

func (e *env) passkeyLogin(credential []byte, header http.Header) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/login/passkey", header: header, body: map[string]any{"credential": json.RawMessage(credential)}})
}

func TestSigningInWithAPasskeyStartsACookieSession(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, _, a := e.passkeyUser()

	res := e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	c := sessionCookie(res)
	require.NotNil(t, c)
	require.True(t, c.Secure)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, int(session.Lifetime/time.Second), c.MaxAge)
	require.Empty(t, readBody(t, res), "a passkey sign-in gives no bearer token")

	me := e.do(request{path: "/users/me", header: cookie(c.Value)})
	require.Equal(t, http.StatusOK, me.StatusCode)
	require.Equal(t, id, decode[UserBody](t, me).ID)
}

func TestAPasskeySessionLastsLikeAnyOther(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, _, a := e.passkeyUser()
	c := sessionCookie(e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), nil))

	e.clock.advance(session.Lifetime - time.Hour)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(c.Value)}).StatusCode)
	e.clock.advance(session.Lifetime - time.Hour) // past the first end, inside the moved one
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(c.Value)}).StatusCode)
}

func TestAPasskeySignInRecordsTheDeviceAndTheUse(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token, a := e.passkeyUser()
	require.Nil(t, decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data[0].LastUsedAt)
	e.clock.advance(time.Hour)

	res := e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), http.Header{"User-Agent": {"Mozilla/5.0 (Macintosh) Firefox/130.0"}})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	used := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data[0].LastUsedAt
	require.NotNil(t, used)
	require.Equal(t, e.clock.now(), *used)
	var agent string
	require.NoError(t, e.db.GetContext(t.Context(), &agent, `SELECT user_agent FROM sessions ORDER BY id DESC LIMIT 1`))
	require.Contains(t, agent, "Firefox")
}

func TestAPasskeySignInReplacesTheBrowsersCookieSessionAndNoOther(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	id, grace, a := e.passkeyUser()
	_ = id

	res := e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), cookie(admin))
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(admin)}).StatusCode, "the browser's old session is gone")
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(grace)}).StatusCode, "another browser's session stays")
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(sessionCookie(res).Value)}).StatusCode)
}

func TestAPasskeySignInKeepsTheCallersBearerSession(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	bearerToken := decode[redeemBody](t, e.redeem(e.makeLink(admin, adminID), "bearer", nil)).Token
	_, _, a := e.passkeyUser()

	res := e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), bearer(bearerToken))

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: bearer(bearerToken)}).StatusCode, "a passkey sign-in starts a cookie session and replaces only a cookie one")
}

const passkeyLoginFailure = "That passkey didn't work. Use a login link instead."

// requireLoginFailure checks a failed sign-in: the same 401 and the same words
// for every cause, no cookie, and nothing about any user.
func requireLoginFailure(t *testing.T, res *http.Response, names ...string) {
	t.Helper()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Nil(t, sessionCookie(res))
	raw := readBody(t, res)
	var problem Problem
	require.NoError(t, json.Unmarshal([]byte(raw), &problem))
	require.Equal(t, "passkey_login_failed", problem.Code)
	require.Equal(t, passkeyLoginFailure, problem.Detail)
	for _, n := range append(names, "admin") {
		require.NotContains(t, strings.ToLower(raw), strings.ToLower(n))
	}
}

func TestEveryFailedPasskeySignInGetsTheSameAnswer(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, _, a := e.passkeyUser()
	valid := func() []byte { return a.Assert(t, e.loginOptions(), passkeyOrigin) }

	// A passkey the server doesn't know, with a user handle it does.
	stranger := passkeytest.New(t)
	stranger.UserHandle = a.UserHandle
	requireLoginFailure(t, e.passkeyLogin(stranger.Assert(t, e.loginOptions(), passkeyOrigin), nil), "Grace")

	// A wrong origin.
	requireLoginFailure(t, e.passkeyLogin(a.Assert(t, e.loginOptions(), "https://evil.example"), nil), "Grace")

	// An answer that comes too late.
	late := valid()
	e.clock.advance(passkey.ChallengeLifetime + time.Second)
	requireLoginFailure(t, e.passkeyLogin(late, nil), "Grace")

	// An answer to a challenge that was already used.
	once := valid()
	require.Equal(t, http.StatusNoContent, e.passkeyLogin(once, nil).StatusCode)
	requireLoginFailure(t, e.passkeyLogin(once, nil), "Grace")

	// Garbage.
	requireLoginFailure(t, e.passkeyLogin([]byte(`{}`), nil), "Grace")
}

func TestAFailedPasskeySignInKeepsTheCurrentSession(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	_, _, a := e.passkeyUser()

	requireLoginFailure(t, e.passkeyLogin(a.Assert(t, e.loginOptions(), "https://evil.example"), cookie(admin)))

	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(admin)}).StatusCode)
}

func TestAPasskeySignInRefusesASignCountThatGoesBackwards(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, _, a := e.passkeyUser()

	require.Equal(t, http.StatusNoContent, e.passkeyLogin(a.AssertWithCount(t, e.loginOptions(), passkeyOrigin, 7), nil).StatusCode)
	requireLoginFailure(t, e.passkeyLogin(a.AssertWithCount(t, e.loginOptions(), passkeyOrigin, 4), nil), "Grace")
	require.Equal(t, http.StatusNoContent, e.passkeyLogin(a.AssertWithCount(t, e.loginOptions(), passkeyOrigin, 8), nil).StatusCode)
}

func TestADeletedPasskeyNoLongerSignsIn(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	id, grace, a := e.passkeyUser()
	made := decode[passkeyList](t, e.listPasskeys(id, cookie(grace), "")).Data[0]
	// The admin deletes it, as they do for a user who lost a device.
	require.Equal(t, http.StatusNoContent,
		e.do(request{method: http.MethodDelete, path: "/users/" + id + "/passkeys/" + made.ID, header: cookie(admin)}).StatusCode)

	requireLoginFailure(t, e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), nil), "Grace")
}

func TestPasskeySignInOptionsNameNoUser(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	e.passkeyUser()

	opts := e.loginOptions()

	require.NotContains(t, string(opts), "Grace")
	var got struct {
		PublicKey struct {
			AllowCredentials []any `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(opts, &got))
	require.Empty(t, got.PublicKey.AllowCredentials)
}

func TestThePasskeySignInRoutesShareTheRedeemLimit(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 3 })

	for range 3 {
		require.Equal(t, http.StatusOK, e.do(request{method: http.MethodPost, path: "/login/passkey/options"}).StatusCode)
	}
	res := e.do(request{method: http.MethodPost, path: "/login/passkey/options"})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, res).Code)
	require.NotEmpty(t, res.Header.Get("Retry-After"))

	// The answer comes from the same budget.
	res = e.do(request{method: http.MethodPost, path: "/login/passkey", body: map[string]any{"credential": map[string]any{}}})
	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)

	e.clock.advance(redeemWindow + time.Second)
	require.Equal(t, http.StatusOK, e.do(request{method: http.MethodPost, path: "/login/passkey/options"}).StatusCode)
}
