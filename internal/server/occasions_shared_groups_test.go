package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the groups of a shared occasion (docs/routes.md,
// Occasion routes): the admin attaches and detaches them.

func (e *env) occasionGroups(token, occasionID, query string) *http.Response {
	e.t.Helper()
	path := "/occasions/" + occasionID + "/groups"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) attachGroup(token, occasionID, groupID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/occasions/" + occasionID + "/groups", body: map[string]any{"group_id": groupID}, header: cookie(token)})
}

// attach attaches groups to a shared occasion as the admin.
func (e *env) attach(admin, occasionID string, groupIDs ...string) {
	e.t.Helper()
	for _, id := range groupIDs {
		require.Equal(e.t, http.StatusCreated, e.attachGroup(admin, occasionID, id).StatusCode, "attach group %s", id)
	}
}

func (e *env) detachGroup(token, occasionID, groupID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodDelete, path: "/occasions/" + occasionID + "/groups/" + groupID, header: cookie(token)})
}

func TestTheAdminAttachesAndDetachesGroups(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Reunion", annual(7, 4), false)
	cousins, aunts := e.newGroup(admin, "Cousins"), e.newGroup(admin, "Aunts")

	res := e.attachGroup(admin, o.ID, cousins.ID)

	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, "/api/v2/groups/"+cousins.ID, res.Header.Get("Location"))
	got := decode[groupView](t, res)
	require.Equal(t, cousins.ID, got.ID)
	require.Equal(t, "Cousins", got.Name)
	e.attach(admin, o.ID, aunts.ID)

	list := decode[groupList](t, e.occasionGroups(admin, o.ID, ""))
	require.Equal(t, []string{"Aunts", "Cousins"}, groupNames(list), "sorted by name")
	require.Equal(t, 2, list.TotalCount)
	require.Equal(t, []string{"Cousins", "Aunts"}, groupNames(decode[groupList](t, e.occasionGroups(admin, o.ID, "sort=-name"))))
	require.True(t, decode[occasionView](t, e.getOccasion(admin, o.ID)).CanAttachGroup, "attached groups don't stop more")

	require.Equal(t, http.StatusNoContent, e.detachGroup(admin, o.ID, cousins.ID).StatusCode)
	require.Equal(t, []string{"Aunts"}, groupNames(decode[groupList](t, e.occasionGroups(admin, o.ID, ""))))
	require.Equal(t, http.StatusOK, e.getGroup(admin, cousins.ID).StatusCode, "detaching leaves the group")
}

func TestAGroupAttachesToAnOccasionOnce(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Reunion", annual(7, 4), false)
	cousins := e.newGroup(admin, "Cousins")
	e.attach(admin, o.ID, cousins.ID)

	res := e.attachGroup(admin, o.ID, cousins.ID)

	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "already_attached", readProblem(t, res).Code)
}

func TestNoGroupAttachesToAnOccasionMarkedEveryone(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "New Year", annual(1, 1), true)
	cousins := e.newGroup(admin, "Cousins")

	res := e.attachGroup(admin, o.ID, cousins.ID)

	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "occasion_is_everyone", readProblem(t, res).Code)
	require.Empty(t, decode[groupList](t, e.occasionGroups(admin, o.ID, "")).Data)
}

func TestMarkingAnOccasionEveryoneDetachesItsGroupsAndUnmarkingLeavesNone(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Reunion", annual(7, 4), false)
	cousins := e.newGroup(admin, "Cousins")
	e.attach(admin, o.ID, cousins.ID)

	require.Equal(t, http.StatusOK, e.patchOccasion(admin, o.ID, map[string]any{"is_everyone": true}, nil).StatusCode)
	require.Empty(t, decode[groupList](t, e.occasionGroups(admin, o.ID, "")).Data, "marking it everyone clears the groups")

	require.Equal(t, http.StatusOK, e.patchOccasion(admin, o.ID, map[string]any{"is_everyone": false}, nil).StatusCode)
	require.Empty(t, decode[groupList](t, e.occasionGroups(admin, o.ID, "")).Data, "unmarking it leaves it attached to none")
	e.attach(admin, o.ID, cousins.ID)
	require.Len(t, decode[groupList](t, e.occasionGroups(admin, o.ID, "")).Data, 1, "and the admin can attach groups again")
}

func TestAttachingNeedsAGroupThatExists(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Reunion", annual(7, 4), false)

	for _, id := range []string{"9999", "abc"} {
		res := e.attachGroup(admin, o.ID, id)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, id)
		require.Equal(t, "reference_not_found", readProblem(t, res).Code, id)
	}
}

func TestDetachingNeedsAnAttachedGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Reunion", annual(7, 4), false)
	cousins := e.newGroup(admin, "Cousins")

	require.Equal(t, http.StatusNotFound, e.detachGroup(admin, o.ID, cousins.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.detachGroup(admin, o.ID, "abc").StatusCode)
}

func TestOnlyTheAdminManagesAnOccasionsGroups(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	cousins := e.newGroup(admin, "Cousins")
	e.join(admin, cousins.ID, graceID)
	o := e.addShared(admin, "Reunion", annual(7, 4), false)
	e.attach(admin, o.ID, cousins.ID)

	// Grace sees the occasion through the group, and may not manage its groups.
	require.Equal(t, http.StatusOK, e.getOccasion(grace, o.ID).StatusCode)
	for name, res := range map[string]*http.Response{
		"list":   e.occasionGroups(grace, o.ID, ""),
		"attach": e.attachGroup(grace, o.ID, cousins.ID),
		"detach": e.detachGroup(grace, o.ID, cousins.ID),
	} {
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "forbidden", readProblem(t, res).Code, name)
	}

	// An occasion that a user can't see is a 404, as it is for everything else.
	_, hedy := e.otherUser("Hedy")
	for name, res := range map[string]*http.Response{
		"list":   e.occasionGroups(hedy, o.ID, ""),
		"attach": e.attachGroup(hedy, o.ID, cousins.ID),
		"detach": e.detachGroup(hedy, o.ID, cousins.ID),
	} {
		require.Equal(t, http.StatusNotFound, res.StatusCode, name)
	}
}

func TestOnlyASharedOccasionHasGroups(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	cousins := e.newGroup(admin, "Cousins")
	birthday := decode[occasionList](t, e.listOccasions(admin, "kind=birthday")).Data[0]

	require.Equal(t, http.StatusNotFound, e.occasionGroups(admin, birthday.ID, "").StatusCode)
	require.Equal(t, http.StatusNotFound, e.attachGroup(admin, birthday.ID, cousins.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.detachGroup(admin, birthday.ID, cousins.ID).StatusCode)
}

func TestDeletingAGroupOrAnOccasionRemovesTheAttachment(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Reunion", annual(7, 4), false)
	cousins, aunts := e.newGroup(admin, "Cousins"), e.newGroup(admin, "Aunts")
	e.attach(admin, o.ID, cousins.ID, aunts.ID)

	require.Equal(t, http.StatusNoContent, e.do(request{method: http.MethodDelete, path: "/groups/" + cousins.ID, header: cookie(admin)}).StatusCode)

	require.Equal(t, []string{"Aunts"}, groupNames(decode[groupList](t, e.occasionGroups(admin, o.ID, ""))))
	require.Equal(t, http.StatusNoContent, e.deleteOccasion(admin, o.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getGroup(admin, aunts.ID).StatusCode, "deleting the occasion leaves the group")
}
