package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type groupView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	Through   *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"through"`
	CanEdit     bool   `json:"can_edit"`
	CanDelete   bool   `json:"can_delete"`
	CanAddUser  bool   `json:"can_add_user"`
	CanAddGroup bool   `json:"can_add_group"`
	Version     string `json:"version"`
}

type groupList struct {
	Data       []groupView `json:"data"`
	NextCursor *string     `json:"next_cursor"`
	TotalCount int         `json:"total_count"`
}

func groupNames(list groupList) []string {
	out := make([]string, len(list.Data))
	for i, g := range list.Data {
		out[i] = g.Name
	}
	return out
}

func (e *env) postGroup(token, name string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/groups", body: map[string]any{"name": name}, header: cookie(token)})
}

// newGroup makes a group as the admin and returns it.
func (e *env) newGroup(admin, name string) groupView {
	e.t.Helper()
	res := e.postGroup(admin, name)
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "create group %q", name)
	return decode[groupView](e.t, res)
}

func (e *env) listGroups(token, query string) *http.Response {
	e.t.Helper()
	path := "/groups"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) getGroup(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/groups/" + id, header: cookie(token)})
}

func (e *env) addUserToGroup(token, groupID, userID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/groups/" + groupID + "/users", body: map[string]any{"user_id": userID}, header: cookie(token)})
}

// join puts users in a group as the admin.
func (e *env) join(admin, groupID string, userIDs ...string) {
	e.t.Helper()
	for _, id := range userIDs {
		res := e.addUserToGroup(admin, groupID, id)
		require.Equal(e.t, http.StatusCreated, res.StatusCode, "add user %s to group %s", id, groupID)
	}
}

func (e *env) nestGroup(token, outerID, innerID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/groups/" + outerID + "/groups", body: map[string]any{"group_id": innerID}, header: cookie(token)})
}

// nest puts a group inside another as the admin.
func (e *env) nest(admin, outerID, innerID string) {
	e.t.Helper()
	require.Equal(e.t, http.StatusCreated, e.nestGroup(admin, outerID, innerID).StatusCode, "nest %s in %s", innerID, outerID)
}

func TestTheAdminCreatesAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.postGroup(admin, " My family ")

	require.Equal(t, http.StatusCreated, res.StatusCode)
	g := decode[groupView](t, res)
	require.Equal(t, "/api/v2/groups/"+g.ID, res.Header.Get("Location"))
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	require.Equal(t, "My family", g.Name, "the name is trimmed")
	require.NotEmpty(t, g.CreatedAt)
	require.Nil(t, g.Through)
	require.True(t, g.CanEdit && g.CanDelete && g.CanAddUser && g.CanAddGroup, "the admin can do all of it")
	require.Equal(t, g, withVersion(decode[groupView](t, e.getGroup(admin, g.ID)), ""), "the group at its address is the one that was made")
}

// withVersion sets the version of a group, so a group from a collection row can
// be compared with one from its own address (which has the version in the ETag).
func withVersion(g groupView, v string) groupView {
	g.Version = v
	return g
}

func TestOnlyTheAdminCreatesGroups(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")

	res := e.postGroup(grace, "Mine")
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)

	res = e.do(request{method: http.MethodPost, path: "/groups", body: map[string]any{"name": "x"}})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestAGroupNameIsRequiredAndShort(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for name, body := range map[string]map[string]any{
		"no name":     {},
		"a blank":     {"name": "   "},
		"a long name": {"name": strings.Repeat("é", 101)},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.do(request{method: http.MethodPost, path: "/groups", body: body, header: cookie(admin)})
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "invalid_input", readProblem(t, res).Code)
		})
	}
	require.Equal(t, 0, decode[groupList](t, e.listGroups(admin, "")).TotalCount, "nothing was made")
	require.Equal(t, http.StatusCreated, e.postGroup(admin, strings.Repeat("é", 100)).StatusCode, "100 characters is the limit")
}

func TestAUserListsTheGroupsTheyAreInDirectlyOrThroughNesting(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	family := e.newGroup(admin, "My family")
	inlaws := e.newGroup(admin, "In-laws")
	e.newGroup(admin, "Strangers")
	e.nest(admin, inlaws.ID, family.ID) // My family is inside In-laws
	e.join(admin, family.ID, graceID)

	list := decode[groupList](t, e.listGroups(grace, ""))

	require.Equal(t, []string{"In-laws", "My family"}, groupNames(list), "by name, and not the group Grace is not in")
	require.Equal(t, 2, list.TotalCount)
	require.Nil(t, list.Data[1].Through, "Grace is in My family directly")
	require.Equal(t, "My family", list.Data[0].Through.Name, "and in In-laws through it")
	require.Equal(t, family.ID, list.Data[0].Through.ID)
	for _, g := range list.Data {
		require.False(t, g.CanEdit || g.CanDelete || g.CanAddUser || g.CanAddGroup, "only the admin manages groups")
	}
}

func TestThroughIsTheFirstGroupByNameWhenSeveralPutTheCallerInOne(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zeta := e.newGroup(admin, "Zeta")
	alpha := e.newGroup(admin, "Alpha")
	top := e.newGroup(admin, "Top")
	e.nest(admin, top.ID, zeta.ID)
	e.nest(admin, top.ID, alpha.ID)
	e.join(admin, zeta.ID, graceID)
	e.join(admin, alpha.ID, graceID)

	list := decode[groupList](t, e.listGroups(grace, ""))

	require.Equal(t, []string{"Alpha", "Top", "Zeta"}, groupNames(list))
	require.Equal(t, "Alpha", list.Data[1].Through.Name)
}

func TestTheAdminListsEveryGroupAndIncludesMeLimitsThemToTheirOwn(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	family := e.newGroup(admin, "My family")
	e.newGroup(admin, "Strangers")
	e.join(admin, family.ID, adminID)

	all := decode[groupList](t, e.listGroups(admin, ""))
	require.Equal(t, []string{"My family", "Strangers"}, groupNames(all))
	require.Equal(t, 2, all.TotalCount)

	for _, query := range []string{"includes_me=true"} {
		mine := decode[groupList](t, e.listGroups(admin, query))
		require.Equal(t, []string{"My family"}, groupNames(mine), query)
		require.Equal(t, 1, mine.TotalCount, query)
	}
	require.Equal(t, 2, decode[groupList](t, e.listGroups(admin, "includes_me=false")).TotalCount, "false is the default result")
	for _, g := range all.Data {
		require.True(t, g.CanEdit, "the admin manages every group, including the ones they are not in")
	}
}

func TestIncludesMeChangesNothingForAUserWhoIsNotTheAdmin(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	e.join(admin, e.newGroup(admin, "My family").ID, graceID)
	e.newGroup(admin, "Strangers")

	require.Equal(t, []string{"My family"}, groupNames(decode[groupList](t, e.listGroups(grace, "includes_me=true"))))
	require.Equal(t, []string{"My family"}, groupNames(decode[groupList](t, e.listGroups(grace, "includes_me=false"))))
}

func TestTheGroupsCollectionPagesSortsAndRejectsWhatItDoesNotKnow(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	for _, name := range []string{"Cy", "Ada", "Bea"} {
		e.newGroup(admin, name)
	}

	first := decode[groupList](t, e.listGroups(admin, "limit=2"))
	require.Equal(t, []string{"Ada", "Bea"}, groupNames(first))
	require.Equal(t, 3, first.TotalCount)
	require.NotNil(t, first.NextCursor)
	rest := decode[groupList](t, e.listGroups(admin, "limit=2&cursor="+url.QueryEscape(*first.NextCursor)))
	require.Equal(t, []string{"Cy"}, groupNames(rest))
	require.Equal(t, []string{"Cy", "Bea", "Ada"}, groupNames(decode[groupList](t, e.listGroups(admin, "sort=-name"))))

	for query, code := range map[string]string{"shade=red": "unknown_filter", "sort=created_at": "unknown_sort", "includes_me=maybe": "invalid_input"} {
		res := e.listGroups(admin, query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, query)
		require.Equal(t, code, readProblem(t, res).Code, query)
	}
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/groups"}).StatusCode)
}

func TestAGroupIsSeenByTheUsersInItAndTheAdminAndIsA404ToEveryoneElse(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	family := e.newGroup(admin, "My family")
	e.join(admin, family.ID, graceID)

	res := e.getGroup(grace, family.ID)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	require.False(t, decode[groupView](t, res).CanEdit)
	require.Equal(t, http.StatusOK, e.getGroup(admin, family.ID).StatusCode)

	res = e.getGroup(zed, family.ID)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", readProblem(t, res).Code)
	require.Equal(t, http.StatusNotFound, e.getGroup(zed, "9999").StatusCode)
	require.Equal(t, http.StatusNotFound, e.getGroup(admin, "9999").StatusCode)
	require.Equal(t, http.StatusNotFound, e.getGroup(admin, "abc").StatusCode)
}

func TestAUserInAnInnerGroupIsInTheOuterGroupButNotTheOtherWayRound(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	zedID, zed := e.otherUser("Zed")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)
	e.join(admin, outer.ID, graceID)
	e.join(admin, inner.ID, zedID)

	require.Equal(t, http.StatusNotFound, e.getGroup(grace, inner.ID).StatusCode, "Grace is in Outer, which contains Inner, but she is not in Inner")
	require.Equal(t, http.StatusOK, e.getGroup(zed, outer.ID).StatusCode, "Zed is in Inner, which is inside Outer, so Zed is in Outer")
}

func TestTheAdminRenamesAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	g := e.newGroup(admin, "Famly")

	res := e.do(request{method: http.MethodPatch, path: "/groups/" + g.ID, body: map[string]any{"name": " Family "}, header: cookie(admin)})

	require.Equal(t, http.StatusOK, res.StatusCode)
	renamed := decode[groupView](t, res)
	require.Equal(t, "Family", renamed.Name)
	require.NotEqual(t, `"`, res.Header.Get("ETag"))
	require.Equal(t, "Family", decode[groupView](t, e.getGroup(admin, g.ID)).Name)
}

func TestARenameChecksTheNameTheVersionAndWhoAsks(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, graceID)
	patch := func(token string, body any, h http.Header) *http.Response {
		hdr := cookie(token)
		for k, v := range h {
			hdr[k] = v
		}
		return e.do(request{method: http.MethodPatch, path: "/groups/" + g.ID, body: body, header: hdr})
	}

	for _, body := range []map[string]any{{"name": ""}, {"name": "  "}, {"name": nil}, {"name": strings.Repeat("x", 101)}} {
		res := patch(admin, body, nil)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%v", body)
		require.Equal(t, "invalid_input", readProblem(t, res).Code)
	}
	res := patch(admin, map[string]any{"name": "New"}, http.Header{"If-Match": {`"stale"`}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)

	res = patch(grace, map[string]any{"name": "Mine"}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "Grace sees the group but can't change it")
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	require.Equal(t, http.StatusNotFound, patch(zed, map[string]any{"name": "Mine"}, nil).StatusCode, "Zed can't see it")

	// The version the admin loaded still fits, and the new one is returned.
	etag := e.getGroup(admin, g.ID).Header.Get("ETag")
	ok := patch(admin, map[string]any{"name": "New"}, http.Header{"If-Match": {etag}})
	require.Equal(t, http.StatusOK, ok.StatusCode)
	require.NotEqual(t, etag, ok.Header.Get("ETag"))
	require.Equal(t, []string{"New"}, groupNames(decode[groupList](t, e.listGroups(grace, ""))))
}

func TestTheAdminDeletesAGroupAndItsPlaceInTheNestingButNotItsUsersOrGroups(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)
	e.join(admin, inner.ID, graceID)

	res := e.do(request{method: http.MethodDelete, path: "/groups/" + inner.ID, header: cookie(admin)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, http.StatusNotFound, e.getGroup(admin, inner.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/" + graceID, header: cookie(admin)}).StatusCode, "the user stays")
	nested := e.do(request{path: "/groups/" + outer.ID + "/groups", header: cookie(admin)})
	require.Equal(t, 0, decode[groupList](t, nested).TotalCount)
	again := e.do(request{method: http.MethodDelete, path: "/groups/" + inner.ID, header: cookie(admin)})
	require.Equal(t, http.StatusNotFound, again.StatusCode, "deleting what is gone is a 404")
}

func TestDeletingAGroupDoesNotDeleteTheGroupsInsideIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)

	require.Equal(t, http.StatusNoContent, e.do(request{method: http.MethodDelete, path: "/groups/" + outer.ID, header: cookie(admin)}).StatusCode)

	require.Equal(t, http.StatusOK, e.getGroup(admin, inner.ID).StatusCode)
}

func TestADeleteChecksTheVersionAndWhoAsks(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, graceID)
	del := func(token string, h http.Header) *http.Response {
		hdr := cookie(token)
		for k, v := range h {
			hdr[k] = v
		}
		return e.do(request{method: http.MethodDelete, path: "/groups/" + g.ID, header: hdr})
	}

	res := del(admin, http.Header{"If-Match": {`"stale"`}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)
	require.Equal(t, http.StatusForbidden, del(grace, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, del(zed, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.getGroup(admin, g.ID).StatusCode, "nothing was deleted")

	etag := e.getGroup(admin, g.ID).Header.Get("ETag")
	require.Equal(t, http.StatusNoContent, del(admin, http.Header{"If-Match": {etag}}).StatusCode)
}

func TestAGroupHasEachRowsVersionEqualToItsETag(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	g := e.newGroup(admin, "Family")

	row := decode[groupList](t, e.listGroups(admin, "")).Data[0]

	require.Equal(t, e.getGroup(admin, g.ID).Header.Get("ETag"), row.Version)
}
