package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/session"
)

func TestASignedInRouteWithNoSessionIs401(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	for name, header := range map[string]http.Header{
		"no credentials":       nil,
		"an unknown cookie":    cookie("nope"),
		"an unknown token":     bearer("nope"),
		"an empty bearer":      {"Authorization": {"Bearer "}},
		"a scheme that is not": {"Authorization": {"Basic dXNlcjpwYXNz"}},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.do(request{path: "/users/me", header: header})
			require.Equal(t, http.StatusUnauthorized, res.StatusCode)
			require.Equal(t, "unauthorized", readProblem(t, res).Code)
			require.Nil(t, sessionCookie(res), "a 401 must not clear a cookie that a newer sign-in may have replaced")
		})
	}
}

func TestACookieAndABearerTokenGiveTheSameResponse(t *testing.T) {
	e := newEnv(t)
	id, cookieToken := e.signUp()
	uid, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	bearerToken, _, err := session.Create(t.Context(), e.db, e.clock.now(), uid, "native app")
	require.NoError(t, err)

	viaCookie := e.do(request{path: "/users/me", header: cookie(cookieToken)})
	viaBearer := e.do(request{path: "/users/me", header: bearer(bearerToken)})

	require.Equal(t, http.StatusOK, viaCookie.StatusCode)
	require.Equal(t, http.StatusOK, viaBearer.StatusCode)
	require.Equal(t, readBody(t, viaCookie), readBody(t, viaBearer))
}

func TestTheBearerTokenWinsOverTheCookie(t *testing.T) {
	e := newEnv(t)
	_, cookieToken := e.signUp()
	_, otherToken := e.otherUser("Grace")

	both := http.Header{
		"Cookie":        {sessionCookieName + "=" + cookieToken},
		"Authorization": {"Bearer " + otherToken},
	}
	res := e.do(request{path: "/users/me", header: both})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "Grace", decode[map[string]any](t, res)["name"])

	// A bad bearer token is not rescued by a good cookie.
	both.Set("Authorization", "Bearer nope")
	res = e.do(request{path: "/users/me", header: both})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	// A good bearer token is not spoiled by a bad cookie.
	res = e.do(request{path: "/users/me", header: http.Header{
		"Cookie":        {sessionCookieName + "=nope"},
		"Authorization": {"bearer " + otherToken},
	}})
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestMeStandsForTheCallerInAnyUserPath(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	otherID, _ := e.otherUser("Grace")

	byMe := e.do(request{path: "/users/me", header: cookie(token)})
	byID := e.do(request{path: "/users/" + id, header: cookie(token)})
	require.Equal(t, http.StatusOK, byID.StatusCode)
	require.Equal(t, readBody(t, byID), readBody(t, byMe))

	// A user the caller can't see is a 404, and so is an ID that is no ID.
	for _, path := range []string{"/users/" + otherID + "x", "/users/0", "/users/abc"} {
		res := e.do(request{path: path, header: cookie(token)})
		require.Equal(t, http.StatusNotFound, res.StatusCode, path)
		require.Equal(t, "not_found", readProblem(t, res).Code)
	}
}

func TestMeNeedsASession(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	res := e.do(request{path: "/users/me"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestNoResponseContainsMeOrCurrent(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()

	res := e.do(request{path: "/users/me", header: cookie(token)})
	body := readBody(t, res)
	require.NotContains(t, body, `"me"`)
	require.NotContains(t, body, `"current"`)
	require.Contains(t, body, `"id":"`+id+`"`)
}

func TestLogOutRevokesTheSessionAndClearsTheCookie(t *testing.T) {
	e := newEnv(t)
	_, token := e.signUp()

	res := e.do(request{method: http.MethodDelete, path: "/users/me/sessions/current", header: cookie(token)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	c := sessionCookie(res)
	require.NotNil(t, c, "the response clears the cookie")
	require.Empty(t, c.Value)
	require.Negative(t, c.MaxAge)
	again := e.do(request{path: "/users/me", header: cookie(token)})
	require.Equal(t, http.StatusUnauthorized, again.StatusCode)
}

func TestLogOutOfABearerSessionSetsNoCookie(t *testing.T) {
	e := newEnv(t)
	id, _ := e.signUp()
	uid, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	token, _, err := session.Create(t.Context(), e.db, e.clock.now(), uid, "")
	require.NoError(t, err)

	res := e.do(request{method: http.MethodDelete, path: "/users/me/sessions/current", header: bearer(token)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Nil(t, sessionCookie(res))
	again := e.do(request{path: "/users/me", header: bearer(token)})
	require.Equal(t, http.StatusUnauthorized, again.StatusCode)
}

func TestLogOutLeavesTheCallersOtherSessionsAlone(t *testing.T) {
	e := newEnv(t)
	id, first := e.signUp()
	uid, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	second, _, err := session.Create(t.Context(), e.db, e.clock.now(), uid, "")
	require.NoError(t, err)

	e.do(request{method: http.MethodDelete, path: "/users/me/sessions/current", header: cookie(first)})

	res := e.do(request{path: "/users/me", header: bearer(second)})
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestLogOutWithNoSessionIs401(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	res := e.do(request{method: http.MethodDelete, path: "/users/me/sessions/current"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Nil(t, sessionCookie(res))
}

func TestASessionOfAnotherUserIsA404(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	_, otherToken := e.otherUser("Grace")
	var otherSession int64
	require.NoError(t, e.db.GetContext(t.Context(), &otherSession, `SELECT id FROM sessions WHERE token_hash = ?`, session.Hash(otherToken)))

	res := e.do(request{method: http.MethodDelete, path: "/users/" + id + "/sessions/" + strconv.FormatInt(otherSession, 10), header: cookie(token)})

	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(otherToken)}).StatusCode, "the session stays")
}

func TestTheCookieIsRenewedAtMostOnceADay(t *testing.T) {
	e := newEnv(t)
	_, token := e.signUp()

	res := e.do(request{path: "/users/me", header: cookie(token)})
	require.Nil(t, sessionCookie(res), "the cookie was just sent")

	e.clock.advance(25 * time.Hour)
	res = e.do(request{path: "/users/me", header: cookie(token)})
	c := sessionCookie(res)
	require.NotNil(t, c)
	require.Equal(t, token, c.Value)
	require.Equal(t, int(session.Lifetime/time.Second), c.MaxAge)
	require.True(t, c.Secure)
	require.True(t, c.HttpOnly)

	res = e.do(request{path: "/users/me", header: cookie(token)})
	require.Nil(t, sessionCookie(res), "the second request of the day")
}

func TestABearerSessionIsNeverGivenACookie(t *testing.T) {
	e := newEnv(t)
	id, _ := e.signUp()
	uid, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	token, _, err := session.Create(t.Context(), e.db, e.clock.now(), uid, "")
	require.NoError(t, err)

	e.clock.advance(25 * time.Hour)
	res := e.do(request{path: "/users/me", header: bearer(token)})

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Nil(t, sessionCookie(res))
}

func TestASessionLastsAYearAfterItsLastUse(t *testing.T) {
	e := newEnv(t)
	_, token := e.signUp()

	for range 3 {
		e.clock.advance(300 * 24 * time.Hour)
		res := e.do(request{path: "/users/me", header: cookie(token)})
		require.Equal(t, http.StatusOK, res.StatusCode)
	}
	e.clock.advance(session.Lifetime + time.Hour)
	res := e.do(request{path: "/users/me", header: cookie(token)})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestTheOpenAPIDocumentHasBothSecuritySchemes(t *testing.T) {
	e := newEnv(t)

	res := e.do(request{path: "/openapi.json"})
	doc := decode[struct {
		Components struct {
			SecuritySchemes map[string]map[string]any `json:"securitySchemes"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			Security []map[string][]string `json:"security"`
		} `json:"paths"`
	}](t, res)

	cookieScheme, bearerScheme := doc.Components.SecuritySchemes["cookieAuth"], doc.Components.SecuritySchemes["bearerAuth"]
	require.Equal(t, "apiKey", cookieScheme["type"])
	require.Equal(t, "cookie", cookieScheme["in"])
	require.Equal(t, "wishbone_session", cookieScheme["name"])
	require.Equal(t, "http", bearerScheme["type"])
	require.Equal(t, "bearer", bearerScheme["scheme"])

	signedIn := []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}}
	require.Equal(t, signedIn, doc.Paths["/api/v2/users/{id}"]["get"].Security)
	require.Equal(t, signedIn, doc.Paths["/api/v2/users/{id}/sessions/{session_id}"]["delete"].Security)
	for _, public := range []struct{ path, method string }{
		{"/api/v2/health", "get"}, {"/api/v2/setup", "get"}, {"/api/v2/setup", "post"},
	} {
		require.Empty(t, doc.Paths[public.path][public.method].Security, "%s %s", public.method, public.path)
	}
}

func TestAPanicAnswersAProblemWithACode(t *testing.T) {
	e := newEnv(t)
	e.srv.router.Get("/api/v2/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	res := e.do(request{path: "/boom"})

	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	p := readProblem(t, res)
	require.Equal(t, "internal_error", p.Code)
	require.NotContains(t, strings.ToLower(p.Detail), "boom", "the detail does not leak the panic")
}

func TestAnAbortedHandlerStillAborts(t *testing.T) {
	e := newEnv(t)
	e.srv.router.Get("/api/v2/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.ts.URL+"/api/v2/abort", nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		res.Body.Close()
	}
	require.Error(t, err, "the connection is cut, as net/http does for ErrAbortHandler")
}

// Every route is either public or signed in. A signed-in route has both
// security schemes and documents its 401, because huma reads a route's Errors
// before the group adds the check, so a route that leaves 401 out would answer
// it with no word of it in the contract.
func TestEverySignedInRouteDocumentsItsSecurityAnd401(t *testing.T) {
	e := newEnv(t)
	public := map[string]bool{
		"get /api/v2/health": true, "get /api/v2/setup": true, "post /api/v2/setup": true,
		"post /api/v2/login-links/check": true, "post /api/v2/login-links/redeem": true,
		"post /api/v2/login/passkey/options": true, "post /api/v2/login/passkey": true,
	}

	doc := decode[struct {
		Paths map[string]map[string]struct {
			Security  []map[string][]string     `json:"security"`
			Responses map[string]map[string]any `json:"responses"`
		} `json:"paths"`
	}](t, e.do(request{path: "/openapi.json"}))

	signedIn := []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}}
	seen := 0
	for path, methods := range doc.Paths {
		for method, op := range methods {
			name := method + " " + path
			if public[name] {
				require.Empty(t, op.Security, name)
				continue
			}
			seen++
			require.Equal(t, signedIn, op.Security, name)
			require.Contains(t, op.Responses, "401", "%s leaves the 401 out of its Errors", name)
		}
	}
	require.Positive(t, seen)
}

func TestAPanicInAHumaOperationAnswersAProblemWithACode(t *testing.T) {
	e := newEnv(t)
	cfg := huma.DefaultConfig("test", "test")
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	api := humachi.New(e.srv.router, cfg)
	huma.Register(api, huma.Operation{
		OperationID: "boom", Method: http.MethodGet, Path: "/api/v2/boom-huma",
	}, func(context.Context, *struct{}) (*struct{}, error) { panic("boom") })

	res := e.do(request{path: "/boom-huma"})

	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	p := readProblem(t, res)
	require.Equal(t, "internal_error", p.Code)
	require.NotContains(t, strings.ToLower(p.Detail), "boom")
}
