package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetupIsNeededUntilAUserExists(t *testing.T) {
	e := newEnv(t)

	require.True(t, e.isNeeded())

	e.signUp()

	require.False(t, e.isNeeded())
}

func (e *env) isNeeded() bool {
	e.t.Helper()
	res := e.do(request{path: "/setup"})
	require.Equal(e.t, http.StatusOK, res.StatusCode)
	return decode[struct {
		IsNeeded bool `json:"is_needed"`
	}](e.t, res).IsNeeded
}

func TestSetupCreatesTheAdminAndStartsACookieSession(t *testing.T) {
	e := newEnv(t)

	res := e.do(request{method: http.MethodPost, path: "/setup", body: map[string]any{
		"setup_code": e.code,
		"name":       "Ada",
		"last_name":  "Lovelace",
		"birthday":   map[string]any{"month": 2, "day": 29},
	}})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	user := decode[map[string]any](t, res)
	id, ok := user["id"].(string)
	require.True(t, ok, "an ID is a JSON string: %v", user["id"])
	require.Equal(t, "/api/v2/users/"+id, res.Header.Get("Location"), "the address of the API resource")
	require.Equal(t, "Ada", user["name"])
	require.Equal(t, "Lovelace", user["last_name"])
	require.Equal(t, map[string]any{"month": float64(2), "day": float64(29), "year": nil}, user["birthday"])
	require.Equal(t, true, user["is_admin"])
	require.Contains(t, user, "profile_confirmed_at")
	require.Nil(t, user["profile_confirmed_at"])
	require.NotEmpty(t, user["created_at"])
	require.NotContains(t, user, "token", "setup gives no bearer token")

	c := sessionCookie(res)
	require.NotNil(t, c, "the response sets the session cookie")
	require.True(t, c.HttpOnly)
	require.True(t, c.Secure)
	require.Equal(t, http.SameSiteLaxMode, c.SameSite)
	require.Equal(t, "/", c.Path)

	me := e.do(request{path: "/users/me", header: cookie(c.Value)})
	require.Equal(t, http.StatusOK, me.StatusCode)
	require.Equal(t, id, decode[map[string]any](t, me)["id"])
}

func TestSetupKeepsTheLastNameNullWhenThereIsNone(t *testing.T) {
	e := newEnv(t)

	res := e.do(request{method: http.MethodPost, path: "/setup", body: e.ada()})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	user := decode[map[string]any](t, res)
	require.Contains(t, user, "last_name")
	require.Nil(t, user["last_name"])
}

func TestAWrongSetupCodeIs422AndKeepsTheRealCode(t *testing.T) {
	e := newEnv(t)
	body := e.ada()
	body["setup_code"] = "AAAA-AAAA-AAAA"

	res := e.do(request{method: http.MethodPost, path: "/setup", body: body})

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "setup_code_invalid", readProblem(t, res).Code)
	require.Nil(t, sessionCookie(res))
	require.True(t, e.isNeeded())

	e.signUp() // the real code still works
}

func TestSetupAfterAUserExistsIs409AndCreatesNothing(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	for _, code := range []string{e.code, "AAAA-AAAA-AAAA"} {
		body := e.ada()
		body["setup_code"] = code
		res := e.do(request{method: http.MethodPost, path: "/setup", body: body})
		require.Equal(t, http.StatusConflict, res.StatusCode)
		require.Equal(t, "setup_complete", readProblem(t, res).Code)
		require.Nil(t, sessionCookie(res))
	}
	var users int
	require.NoError(t, e.db.GetContext(t.Context(), &users, `SELECT count(*) FROM users`))
	require.Equal(t, 1, users)
}

func TestSetupAttemptsCountAgainstTheRedeemLimit(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 3 })
	wrong := e.ada()
	wrong["setup_code"] = "AAAA-AAAA-AAAA"

	for range 3 {
		res := e.do(request{method: http.MethodPost, path: "/setup", body: wrong})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	}
	res := e.do(request{method: http.MethodPost, path: "/setup", body: e.ada()})

	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, res).Code)
	require.NotEmpty(t, res.Header.Get("Retry-After"))

	e.clock.advance(61 * time.Second) // a minute later the window has passed
	res = e.do(request{method: http.MethodPost, path: "/setup", body: e.ada()})
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestTheSetupLimitDoesNotCountReadingWhetherSetupIsNeeded(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.RateLimitRedeem = 1 })
	for range 5 {
		require.Equal(t, http.StatusOK, e.do(request{path: "/setup"}).StatusCode)
	}
}

func TestSetupChecksTheBirthdayAsAPartialDate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		birthday map[string]any
		status   int
	}{
		{"February 29 with no year", map[string]any{"month": 2, "day": 29}, http.StatusCreated},
		{"February 29 with a null year", map[string]any{"month": 2, "day": 29, "year": nil}, http.StatusCreated},
		{"February 29 in a leap year", map[string]any{"month": 2, "day": 29, "year": 2000}, http.StatusCreated},
		{"February 29 in a year that is not a leap year", map[string]any{"month": 2, "day": 29, "year": 2001}, http.StatusUnprocessableEntity},
		{"February 30", map[string]any{"month": 2, "day": 30}, http.StatusUnprocessableEntity},
		{"April 31", map[string]any{"month": 4, "day": 31}, http.StatusUnprocessableEntity},
		{"month 13", map[string]any{"month": 13, "day": 1}, http.StatusUnprocessableEntity},
		{"no day", map[string]any{"month": 4}, http.StatusUnprocessableEntity},
		{"a year in the future", map[string]any{"month": 4, "day": 1, "year": 2999}, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			body := e.ada()
			body["birthday"] = tc.birthday

			res := e.do(request{method: http.MethodPost, path: "/setup", body: body})

			require.Equal(t, tc.status, res.StatusCode)
			if tc.status == http.StatusUnprocessableEntity {
				require.Equal(t, "invalid_input", readProblem(t, res).Code)
				require.True(t, e.isNeeded())
			}
		})
	}
}

func TestSetupNamesTheFieldThatIsNotValid(t *testing.T) {
	e := newEnv(t)
	body := e.ada()
	body["birthday"] = map[string]any{"month": 4, "day": 31}

	res := e.do(request{method: http.MethodPost, path: "/setup", body: body})

	p := decode[struct {
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}](t, res)
	require.Len(t, p.Errors, 1)
	require.Equal(t, "body.birthday", p.Errors[0].Location)
}

func TestSetupNeedsANameAndTheCode(t *testing.T) {
	for _, field := range []string{"name", "setup_code", "birthday"} {
		t.Run(field, func(t *testing.T) {
			e := newEnv(t)
			body := e.ada()
			delete(body, field)

			res := e.do(request{method: http.MethodPost, path: "/setup", body: body})

			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "invalid_input", readProblem(t, res).Code)
		})
	}
}

func TestSetupRefusesABlankName(t *testing.T) {
	e := newEnv(t)
	body := e.ada()
	body["name"] = "   "

	res := e.do(request{method: http.MethodPost, path: "/setup", body: body})

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}

func TestSetupIsOpenToACallerWhoIsNotSignedIn(t *testing.T) {
	e := newEnv(t)
	// A cookie that means nothing does not stop setup.
	res := e.do(request{method: http.MethodPost, path: "/setup", body: e.ada(), header: cookie("stale")})
	require.Equal(t, http.StatusCreated, res.StatusCode)
}

func TestLocationIsTheAddressUnderTheAPIPrefix(t *testing.T) {
	require.Equal(t, "/api/v2/users/42", location("/users/42"))
	require.Equal(t, "/api/v2/lists/9/items/3", location("/lists/9/items/3"))
}
