package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every route that only the admin may call asks one question, requireAdmin, so
// each refuses another caller the same way: a 403 forbidden that starts with
// "Only the admin".
func TestEveryAdminOnlyRouteRefusesAnotherCallerWithTheSameForbidden(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	cousins := e.newGroup(admin, "Cousins")
	store := e.blockStore(admin, "temu.com")
	// Grace sees the group and the user, so the refusal is a 403 and not a 404.
	e.join(admin, cousins.ID, graceID)

	for name, res := range map[string]*http.Response{
		"create a user":       e.createUser(grace, map[string]any{"name": "Hedy", "birthday": birthday(1, 1, nil)}),
		"deactivate a user":   e.deactivate(grace, graceID),
		"reactivate a user":   e.reactivate(grace, graceID),
		"create a group":      e.postGroup(grace, "Family"),
		"rename a group":      e.do(request{method: http.MethodPatch, path: "/groups/" + cousins.ID, body: map[string]any{"name": "x"}, header: cookie(grace)}),
		"add a user to group": e.addUserToGroup(grace, cousins.ID, graceID),
		"nest a group":        e.nestGroup(grace, cousins.ID, cousins.ID),
		"list blocked stores": e.listBlockedStores(grace, ""),
		"block a store":       e.postBlockedStore(grace, "example.com"),
		"allow a store again": e.deleteBlockedStore(grace, store.ID, nil),
	} {
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		p := readProblem(t, res)
		require.Equal(t, "forbidden", p.Code, name)
		require.True(t, strings.HasPrefix(p.Detail, "Only the admin "), "%s: %q", name, p.Detail)
	}
}
