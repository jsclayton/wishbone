package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func (e *env) groupUsers(token, groupID, query string) *http.Response {
	e.t.Helper()
	path := "/groups/" + groupID + "/users"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) removeGroupUser(token, groupID, userID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodDelete, path: "/groups/" + groupID + "/users/" + userID, header: cookie(token)})
}

func (e *env) innerGroups(token, groupID string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/groups/" + groupID + "/groups", header: cookie(token)})
}

func (e *env) removeGroupGroup(token, outerID, innerID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodDelete, path: "/groups/" + outerID + "/groups/" + innerID, header: cookie(token)})
}

func TestTheAdminAddsAUserToAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	g := e.newGroup(admin, "Family")

	res := e.addUserToGroup(admin, g.ID, graceID)

	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "/api/v2/users/"+graceID, res.Header.Get("Location"), "a membership has no address, so the user's own address")
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	u := decode[userView](t, res)
	require.Equal(t, graceID, u.ID)
	require.Equal(t, "Grace", u.Name)
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.groupUsers(admin, g.ID, ""))))
}

func TestAddingAUserWhoIsAlreadyInTheGroupIsA409(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, graceID)

	res := e.addUserToGroup(admin, g.ID, graceID)

	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "already_in_group", readProblem(t, res).Code)
}

func TestAUserWhoIsInTheGroupOnlyThroughNestingCanStillBeAddedDirectly(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)
	e.join(admin, inner.ID, graceID)

	require.Equal(t, http.StatusCreated, e.addUserToGroup(admin, outer.ID, graceID).StatusCode)
}

func TestAddingAUserNeedsTheAdminAndAUserAndAGroupThatExist(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zedID, zed := e.otherUser("Zed")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, graceID)

	res := e.addUserToGroup(grace, g.ID, zedID)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "Grace sees the group but only the admin adds users")
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	require.Equal(t, http.StatusNotFound, e.addUserToGroup(zed, g.ID, zedID).StatusCode, "Zed can't see the group")

	for _, id := range []string{"9999", "abc", "0", "me"} {
		res := e.addUserToGroup(admin, g.ID, id)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, id)
		require.Equal(t, "reference_not_found", readProblem(t, res).Code, id)
	}
	require.Equal(t, http.StatusNotFound, e.addUserToGroup(admin, "9999", zedID).StatusCode)
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.groupUsers(admin, g.ID, ""))), "nothing else was added")
}

func TestAGroupsUsersAreListedByNameForThoseInTheGroupAndTheAdmin(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("grace")
	barbID, _ := e.otherUser("Barbara")
	zedID, zed := e.otherUser("Zed")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, zedID, graceID, barbID)
	_, outsider := e.otherUser("Outsider")

	list := decode[userList](t, e.groupUsers(grace, g.ID, ""))
	require.Equal(t, []string{"Barbara", "grace", "Zed"}, names(list), "by name, ignoring case")
	require.Equal(t, 3, list.TotalCount)
	require.Equal(t, names(list), names(decode[userList](t, e.groupUsers(admin, g.ID, ""))))
	for _, u := range list.Data {
		require.Regexp(t, `^"[0-9a-f]{32}"$`, u.Version, "a full user with its version")
	}

	first := decode[userList](t, e.groupUsers(zed, g.ID, "limit=2&sort=-name"))
	require.Equal(t, []string{"Zed", "grace"}, names(first))
	require.NotNil(t, first.NextCursor)
	rest := decode[userList](t, e.groupUsers(zed, g.ID, "limit=2&sort=-name&cursor="+url.QueryEscape(*first.NextCursor)))
	require.Equal(t, []string{"Barbara"}, names(rest))
	require.Equal(t, 3, rest.TotalCount)

	res := e.groupUsers(outsider, g.ID, "")
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", readProblem(t, res).Code)
	for query, code := range map[string]string{"shade=red": "unknown_filter", "sort=created_at": "unknown_sort"} {
		res := e.groupUsers(admin, g.ID, query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, query)
		require.Equal(t, code, readProblem(t, res).Code, query)
	}
}

func TestAGroupsUsersAreOnlyTheOnesDirectlyInItNotThoseInTheGroupsInsideIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zedID, _ := e.otherUser("Zed")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)
	e.join(admin, outer.ID, graceID)
	e.join(admin, inner.ID, zedID)

	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.groupUsers(grace, outer.ID, ""))))
	require.Equal(t, []string{"Zed"}, names(decode[userList](t, e.groupUsers(admin, inner.ID, ""))))
}

func TestTheAdminTakesAUserOutOfAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zedID, _ := e.otherUser("Zed")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, graceID, zedID)

	res := e.removeGroupUser(grace, g.ID, zedID)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "only the admin")
	res = e.removeGroupUser(admin, g.ID, zedID)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.groupUsers(admin, g.ID, ""))))
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/" + zedID, header: cookie(admin)}).StatusCode, "the user stays")
	again := e.removeGroupUser(admin, g.ID, zedID)
	require.Equal(t, http.StatusNotFound, again.StatusCode, "a user who is not in the group")
	require.Equal(t, "not_found", readProblem(t, again).Code)
	require.Equal(t, http.StatusNotFound, e.removeGroupUser(admin, g.ID, "abc").StatusCode)
	require.Equal(t, http.StatusNotFound, e.removeGroupUser(admin, "9999", graceID).StatusCode)
}

func TestTheAdminNestsAGroupInAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")

	res := e.nestGroup(admin, outer.ID, inner.ID)

	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "/api/v2/groups/"+inner.ID, res.Header.Get("Location"), "the inner group's own address")
	require.Equal(t, inner.ID, decode[groupView](t, res).ID)
	list := decode[groupList](t, e.innerGroups(admin, outer.ID))
	require.Equal(t, []string{"Inner"}, groupNames(list))
	require.Equal(t, 1, list.TotalCount)
	require.True(t, list.Data[0].CanEdit)
	require.Equal(t, 0, decode[groupList](t, e.innerGroups(admin, inner.ID)).TotalCount)
}

func TestNestingAGroupThatIsAlreadyInsideIsA409(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)

	res := e.nestGroup(admin, outer.ID, inner.ID)

	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "already_in_group", readProblem(t, res).Code)
}

func TestNestingThatWouldMakeALoopIsA422(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	a := e.newGroup(admin, "A")
	b := e.newGroup(admin, "B")
	c := e.newGroup(admin, "C")
	e.nest(admin, a.ID, b.ID)
	e.nest(admin, b.ID, c.ID)

	for name, pair := range map[string][2]string{
		"a group in itself":             {a.ID, a.ID},
		"a group in its own child":      {b.ID, a.ID},
		"a group in its own descendant": {c.ID, a.ID},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.nestGroup(admin, pair[0], pair[1])
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "group_loop", readProblem(t, res).Code)
		})
	}
	// A group may be in two groups, and a group inside a group of a group is no loop.
	d := e.newGroup(admin, "D")
	e.nest(admin, a.ID, d.ID)
	e.nest(admin, c.ID, d.ID)
	require.Equal(t, []string{"B", "D"}, groupNames(decode[groupList](t, e.innerGroups(admin, a.ID))))
}

func TestNestingNeedsTheAdminAndGroupsThatExist(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.join(admin, outer.ID, graceID)

	require.Equal(t, http.StatusForbidden, e.nestGroup(grace, outer.ID, inner.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.nestGroup(zed, outer.ID, inner.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.nestGroup(admin, "9999", inner.ID).StatusCode)
	for _, id := range []string{"9999", "abc"} {
		res := e.nestGroup(admin, outer.ID, id)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, id)
		require.Equal(t, "reference_not_found", readProblem(t, res).Code, id)
	}
}

func TestOnlyTheAdminListsTheGroupsInsideAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	outer := e.newGroup(admin, "Outer")
	e.join(admin, outer.ID, graceID)

	res := e.innerGroups(grace, outer.ID)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "a user in the outer group is not in the groups inside it")
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	require.Equal(t, http.StatusNotFound, e.innerGroups(zed, outer.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.innerGroups(admin, outer.ID).StatusCode)
}

func TestTheAdminTakesAGroupOutOfAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)
	e.join(admin, outer.ID, graceID)

	require.Equal(t, http.StatusForbidden, e.removeGroupGroup(grace, outer.ID, inner.ID).StatusCode)
	res := e.removeGroupGroup(admin, outer.ID, inner.ID)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, 0, decode[groupList](t, e.innerGroups(admin, outer.ID)).TotalCount)
	require.Equal(t, http.StatusOK, e.getGroup(admin, inner.ID).StatusCode, "the group stays")
	again := e.removeGroupGroup(admin, outer.ID, inner.ID)
	require.Equal(t, http.StatusNotFound, again.StatusCode)
	require.Equal(t, http.StatusNotFound, e.removeGroupGroup(admin, outer.ID, "abc").StatusCode)
}
