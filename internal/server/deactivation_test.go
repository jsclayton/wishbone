package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, id string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(id, 10, 64)
	require.NoError(t, err)
	return n
}

// decodeInto reads a response body into v, so a test can tell a key that is
// absent from one that is null.
func decodeInto(t *testing.T, res *http.Response, v any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(res.Body).Decode(v))
}

func (e *env) deactivate(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/users/" + id + "/deactivate", header: cookie(token)})
}

func (e *env) reactivate(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/users/" + id + "/reactivate", header: cookie(token)})
}

// deactivated deactivates a user as the admin, and fails the test when the
// server refuses.
func (e *env) deactivated(admin, id string) userView {
	e.t.Helper()
	res := e.deactivate(admin, id)
	require.Equal(e.t, http.StatusOK, res.StatusCode, "deactivate")
	return decode[userView](e.t, res)
}

func TestTheAdminDeactivatesAUserAndTheirSessionsEnd(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")

	res := e.deactivate(admin, graceID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	u := decode[userView](t, res)
	require.NotNil(t, u.DeactivatedAt)
	require.Equal(t, "2026-11-08T12:00:00Z", *u.DeactivatedAt)
	require.False(t, u.CanDeactivate)
	require.True(t, u.CanReactivate)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(grace)}).StatusCode, "the session is revoked")
	got := decode[userView](t, e.getUser(admin, graceID))
	require.Equal(t, u.DeactivatedAt, got.DeactivatedAt)
}

func TestDeactivatingADeactivatedUserChangesNothing(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	first := e.deactivated(admin, graceID)
	e.clock.advance(time.Hour)

	res := e.deactivate(admin, graceID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, first.DeactivatedAt, decode[userView](t, res).DeactivatedAt, "the time stays")
}

func TestNobodyDeactivatesTheAdmin(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()

	res := e.deactivate(admin, adminID)

	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "admin_cannot_be_deactivated", readProblem(t, res).Code)
	me := decode[userView](t, e.getUser(admin, adminID))
	require.Nil(t, me.DeactivatedAt)
	require.False(t, me.CanDeactivate, "can_deactivate is false for the admin")
	require.False(t, me.CanReactivate)
}

func TestOnlyTheAdminDeactivatesAndReactivates(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emma := e.otherUser("Emma")
	_, sue := e.otherUser("Sue")
	e.linkParents(admin, emmaID, momID)

	for _, caller := range []string{mom, emma} {
		for _, op := range []func(string, string) *http.Response{e.deactivate, e.reactivate} {
			res := op(caller, emmaID)
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Equal(t, "forbidden", readProblem(t, res).Code)
		}
	}
	require.Equal(t, http.StatusNotFound, e.deactivate(sue, emmaID).StatusCode, "a user who can't see Emma gets a 404")
	require.Equal(t, http.StatusNotFound, e.deactivate(admin, "9999").StatusCode)
	asMom := decode[userView](t, e.getUser(mom, emmaID))
	require.False(t, asMom.CanDeactivate)
	require.False(t, asMom.CanReactivate)
	require.Nil(t, asMom.DeactivatedAt)
	asAdmin := decode[userView](t, e.getUser(admin, emmaID))
	require.True(t, asAdmin.CanDeactivate)
	require.False(t, asAdmin.CanReactivate)
}

func TestReactivatingBringsTheUserBackButTheirSessionsStayRevoked(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	e.deactivated(admin, graceID)

	res := e.reactivate(admin, graceID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	u := decode[userView](t, res)
	require.Nil(t, u.DeactivatedAt)
	require.True(t, u.CanDeactivate)
	require.False(t, u.CanReactivate)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(grace)}).StatusCode, "a revoked session doesn't come back")
}

func TestReactivatingAnActiveUserChangesNothing(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")

	res := e.reactivate(admin, graceID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Nil(t, decode[userView](t, res).DeactivatedAt)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(grace)}).StatusCode, "the session stays")
}
