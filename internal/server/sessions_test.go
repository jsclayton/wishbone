package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sessionView struct {
	ID         string    `json:"id"`
	UserAgent  string    `json:"user_agent"`
	LastUsedAt time.Time `json:"last_used_at"`
	CreatedAt  time.Time `json:"created_at"`
	IsCurrent  bool      `json:"is_current"`
	CanDelete  bool      `json:"can_delete"`
}

type sessionList struct {
	Data       []sessionView `json:"data"`
	NextCursor *string       `json:"next_cursor"`
	TotalCount int           `json:"total_count"`
}

func (e *env) listSessions(userID string, header http.Header) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/users/" + userID + "/sessions", header: header})
}

func TestTheSessionsListHasEachLiveSessionNewestUseFirst(t *testing.T) {
	e := newEnv(t)
	id, first := e.signUp() // the cookie session from setup
	e.clock.advance(time.Hour)
	e.redeem(e.makeLink(first, id), "bearer", http.Header{"User-Agent": {"Wishbone iOS/1.0"}})
	e.clock.advance(time.Hour)
	e.do(request{path: "/users/me", header: cookie(first)}) // uses the first session again
	e.clock.advance(time.Hour)
	revoked := decode[redeemBody](t, e.redeem(e.makeLink(first, id), "bearer", nil))
	e.do(request{method: http.MethodDelete, path: "/users/me/sessions/current", header: bearer(revoked.Token)})

	res := e.listSessions(id, cookie(first))

	require.Equal(t, http.StatusOK, res.StatusCode)
	list := decode[sessionList](t, res)
	require.Nil(t, list.NextCursor)
	require.Equal(t, 2, list.TotalCount)
	require.Len(t, list.Data, 2)
	require.True(t, list.Data[0].IsCurrent, "the cookie session was used last")
	require.False(t, list.Data[1].IsCurrent)
	require.Equal(t, "Wishbone iOS/1.0", list.Data[1].UserAgent)
	for _, row := range list.Data {
		require.True(t, row.CanDelete)
		require.NotEmpty(t, row.ID)
		require.False(t, row.CreatedAt.IsZero())
	}
	require.True(t, list.Data[0].LastUsedAt.After(list.Data[1].LastUsedAt))
}

func TestABearerCallerAndACookieCallerGetTheSameSessionsAndCanValues(t *testing.T) {
	e := newEnv(t)
	id, cookieToken := e.signUp()
	bearerToken := decode[redeemBody](t, e.redeem(e.makeLink(cookieToken, id), "bearer", nil)).Token

	viaCookie := e.listSessions(id, cookie(cookieToken))
	viaBearer := e.listSessions(id, bearer(bearerToken))

	require.Equal(t, http.StatusOK, viaBearer.StatusCode)
	cl, bl := decode[sessionList](t, viaCookie), decode[sessionList](t, viaBearer)
	require.Len(t, cl.Data, 2)
	require.Len(t, bl.Data, 2)
	for i := range cl.Data {
		require.Equal(t, cl.Data[i].ID, bl.Data[i].ID)
		require.Equal(t, cl.Data[i].CanDelete, bl.Data[i].CanDelete)
	}
	// Only is_current differs, because each caller has its own session.
	require.NotEqual(t, cl.Data[0].IsCurrent, bl.Data[0].IsCurrent)
}

func TestAnExpiredSessionIsNotListed(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	bearerToken := decode[redeemBody](t, e.redeem(e.makeLink(token, id), "bearer", nil)).Token

	// Use only the bearer session for a year, so the cookie session runs out.
	for range 4 {
		e.clock.advance(100 * 24 * time.Hour)
		require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: bearer(bearerToken)}).StatusCode)
	}
	list := decode[sessionList](t, e.listSessions("me", bearer(bearerToken)))

	require.Len(t, list.Data, 1)
	require.True(t, list.Data[0].IsCurrent)
}

func TestAUserSeesOnlyTheirOwnSessionsAndTheAdminSeesEveryones(t *testing.T) {
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	otherID, otherToken := e.otherUser("Grace")

	res := e.listSessions(adminID, cookie(otherToken))
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", readProblem(t, res).Code)

	res = e.listSessions(otherID, cookie(adminToken))
	require.Equal(t, http.StatusOK, res.StatusCode)
	list := decode[sessionList](t, res)
	require.Len(t, list.Data, 1)
	require.False(t, list.Data[0].IsCurrent, "the admin's own session is not Grace's")
	require.True(t, list.Data[0].CanDelete)

	require.Equal(t, http.StatusNotFound, e.listSessions("999", cookie(adminToken)).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.listSessions(adminID, nil).StatusCode)
}

func TestRevokingASessionIs204AndEndsIt(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	bearerToken := decode[redeemBody](t, e.redeem(e.makeLink(token, id), "bearer", nil)).Token
	var target string
	for _, row := range decode[sessionList](t, e.listSessions(id, cookie(token))).Data {
		if !row.IsCurrent {
			target = row.ID
		}
	}

	res := e.do(request{method: http.MethodDelete, path: "/users/" + id + "/sessions/" + target, header: cookie(token)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Nil(t, sessionCookie(res), "it was not the caller's own session")
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: bearer(bearerToken)}).StatusCode)
	require.Len(t, decode[sessionList](t, e.listSessions(id, cookie(token))).Data, 1)

	// Revoking it again is a 404, as for any resource that is gone.
	res = e.do(request{method: http.MethodDelete, path: "/users/" + id + "/sessions/" + target, header: cookie(token)})
	require.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestTheAdminRevokesAnotherUsersSessionButNotThroughTheWrongUser(t *testing.T) {
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	otherID, otherToken := e.otherUser("Grace")
	graceSession := decode[sessionList](t, e.listSessions(otherID, cookie(adminToken))).Data[0].ID
	adminSession := decode[sessionList](t, e.listSessions(adminID, cookie(adminToken))).Data[0].ID

	// Grace can't revoke the admin's session, by either address.
	for _, userID := range []string{adminID, otherID} {
		res := e.do(request{method: http.MethodDelete, path: "/users/" + userID + "/sessions/" + adminSession, header: cookie(otherToken)})
		require.Equal(t, http.StatusNotFound, res.StatusCode)
	}
	// The admin can't reach Grace's session through their own address.
	res := e.do(request{method: http.MethodDelete, path: "/users/" + adminID + "/sessions/" + graceSession, header: cookie(adminToken)})
	require.Equal(t, http.StatusNotFound, res.StatusCode)

	res = e.do(request{method: http.MethodDelete, path: "/users/" + otherID + "/sessions/" + graceSession, header: cookie(adminToken)})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Nil(t, sessionCookie(res))
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(otherToken)}).StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(adminToken)}).StatusCode)
}

func TestRevokingTheCallersOwnCookieSessionByIDClearsTheCookie(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	own := decode[sessionList](t, e.listSessions(id, cookie(token))).Data[0].ID

	res := e.do(request{method: http.MethodDelete, path: "/users/" + id + "/sessions/" + own, header: cookie(token)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	c := sessionCookie(res)
	require.NotNil(t, c)
	require.Negative(t, c.MaxAge)
}

func TestTheUserHasTheCanFieldsForSignIn(t *testing.T) {
	e := newEnv(t)
	adminID, adminToken := e.signUp()
	otherID, otherToken := e.otherUser("Grace")

	for _, tc := range []struct {
		name, path, token string
		want              bool
	}{
		{"self", "/users/" + adminID, adminToken, true},
		{"admin on another user", "/users/" + otherID, adminToken, true},
		{"another user on themselves", "/users/" + otherID, otherToken, true},
	} {
		body := decode[map[string]any](t, e.do(request{path: tc.path, header: cookie(tc.token)}))
		require.Equal(t, tc.want, body["can_make_login_link"], tc.name)
		require.Equal(t, tc.want, body["can_manage_sessions"], tc.name)
	}
}

func TestASessionWhoseUserIsGoneIsNoSession(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")
	// Nothing deletes a user yet. Remove the row the way a later delete will.
	_, err := e.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	_, err = e.db.ExecContext(t.Context(), `DELETE FROM users WHERE id = ?`, id)
	require.NoError(t, err)

	res := e.do(request{path: "/users/me", header: cookie(token)})

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "unauthorized", readProblem(t, res).Code)
	require.Nil(t, sessionCookie(res))
}
