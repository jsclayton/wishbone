package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// GET /users?group_id= is the filter Home's People needs: the users in a group
// directly or through nesting (docs/routes.md, User routes).

func TestTheGroupFilterListsTheUsersDirectlyAndThroughNestingInTheGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zedID, _ := e.otherUser("Zed")
	yvonneID, _ := e.otherUser("Yvonne")
	e.otherUser("Outsider")
	outer := e.newGroup(admin, "Outer")
	middle := e.newGroup(admin, "Middle")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, middle.ID)
	e.nest(admin, middle.ID, inner.ID)
	e.join(admin, outer.ID, graceID)
	e.join(admin, middle.ID, zedID)
	e.join(admin, inner.ID, yvonneID)

	list := decode[userList](t, e.listUsers(grace, "group_id="+outer.ID))

	require.Equal(t, []string{"Grace", "Yvonne", "Zed"}, names(list), "the users in Outer, at any depth")
	require.Equal(t, 3, list.TotalCount)
	for _, u := range list.Data {
		require.Regexp(t, `^"[0-9a-f]{32}"$`, u.Version)
	}
	// Middle holds Zed and Inner's Yvonne, and not Grace, who is only in Outer.
	require.Equal(t, 0, decode[userList](t, e.listUsers(grace, "group_id="+middle.ID)).TotalCount, "Grace is not in Middle, so it is not hers to list")
	require.Equal(t, []string{"Yvonne", "Zed"}, names(decode[userList](t, e.listUsers(admin, "group_id="+middle.ID))))
}

func TestTheGroupFilterAnswersEmptyForAGroupTheCallerCannotSeeOrThatDoesNotExist(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID)

	for _, id := range []string{family.ID, "9999", "abc", "0", "me"} {
		res := e.listUsers(zed, "group_id="+id)
		require.Equal(t, http.StatusOK, res.StatusCode, id)
		list := decode[userList](t, res)
		require.Empty(t, list.Data, id)
		require.Equal(t, 0, list.TotalCount, id)
		require.Nil(t, list.NextCursor, id)
	}
	// The admin sees every group, but no group that is not there.
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.listUsers(admin, "group_id="+family.ID))))
	require.Empty(t, decode[userList](t, e.listUsers(admin, "group_id=9999")).Data)
}

func TestTheGroupFilterOnlyLeavesOutAndNeverAddsUsers(t *testing.T) {
	// Without the filter the caller sees the same users they saw before, and the
	// filter is a subset of them.
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, _ := e.otherUser("Hedy")
	ivyID, _ := e.otherUser("Ivy")
	family := e.newGroup(admin, "Family")
	other := e.newGroup(admin, "Other")
	e.join(admin, family.ID, graceID, hedyID)
	e.join(admin, other.ID, graceID, ivyID)

	require.Equal(t, []string{"Grace", "Hedy", "Ivy"}, names(decode[userList](t, e.listUsers(grace, ""))))
	require.Equal(t, []string{"Grace", "Hedy"}, names(decode[userList](t, e.listUsers(grace, "group_id="+family.ID))))
	require.Equal(t, []string{"Grace", "Ivy"}, names(decode[userList](t, e.listUsers(grace, "group_id="+other.ID))))
}

func TestTheGroupFilterPagesAndSorts(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	ids := e.addUsers("Bea", "Cy", "Di")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, ids...)

	first := decode[userList](t, e.listUsers(admin, "group_id="+g.ID+"&limit=2&sort=-name"))
	require.Equal(t, []string{"Di", "Cy"}, names(first))
	require.Equal(t, 3, first.TotalCount)
	require.NotNil(t, first.NextCursor)
	rest := decode[userList](t, e.listUsers(admin, "group_id="+g.ID+"&limit=2&sort=-name&cursor="+url.QueryEscape(*first.NextCursor)))
	require.Equal(t, []string{"Bea"}, names(rest))
}
