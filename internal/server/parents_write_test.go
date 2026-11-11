package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOnlyTheAdminSetsParentIDsAndAnyoneElseWhoSeesTheUserGetsFieldNotWritable(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)

	for name, token := range map[string]string{"the child": emma, "a parent": mom} {
		res := e.patchUser(token, emmaID, map[string]any{"parent_ids": []string{}}, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "field_not_writable", readProblem(t, res).Code, name)
	}
	// Someone who sees the user but can't edit them is refused before the body.
	sueID, sue := e.otherUser("Sue")
	e.join(admin, e.newGroup(admin, "Cousins").ID, emmaID, sueID)
	res := e.patchUser(sue, emmaID, map[string]any{"parent_ids": []string{}}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	require.Equal(t, []string{"Mom"}, summaryNames(decode[userView](t, e.getUser(admin, emmaID)).Parents))
}

func TestTheAdminReplacesAUsersParentsAndAnEmptyListRemovesThemAll(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, _ := e.otherUser("Mom")
	dadID, _ := e.otherUser("Dad")
	emmaID, _ := e.otherUser("Emma")

	both := e.linkParents(admin, emmaID, momID, dadID, momID) // Mom twice
	require.Equal(t, []string{"Dad", "Mom"}, summaryNames(both.Parents), "a user listed twice is linked once")

	swapped := e.linkParents(admin, emmaID, dadID)
	require.Equal(t, []string{"Dad"}, summaryNames(swapped.Parents))
	mom := decode[userView](t, e.getUser(admin, momID))
	require.Empty(t, mom.Children, "the link went with the replace")

	require.Empty(t, e.linkParents(admin, emmaID).Parents)
	require.Empty(t, decode[userView](t, e.getUser(admin, dadID)).Children)
}

func TestLinkingAParentMovesTheUsersVersionAndIfMatchGuardsIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, _ := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	before := e.getUser(admin, emmaID).Header.Get("ETag")

	stale := e.patchUser(admin, emmaID, map[string]any{"parent_ids": []string{momID}}, http.Header{"If-Match": {`"nope"`}})
	require.Equal(t, http.StatusPreconditionFailed, stale.StatusCode)
	require.Empty(t, decode[userView](t, e.getUser(admin, emmaID)).Parents, "a refused write links nothing")

	res := e.patchUser(admin, emmaID, map[string]any{"parent_ids": []string{momID}}, http.Header{"If-Match": {before}})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotEqual(t, before, res.Header.Get("ETag"))
}

func TestTheAdminCannotMakeALoopOrAUserTheirOwnParent(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	grandmaID, _ := e.otherUser("Grandma")
	dadID, _ := e.otherUser("Dad")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, dadID, grandmaID)
	e.linkParents(admin, emmaID, dadID)

	for name, c := range map[string]struct{ child, parent string }{
		"their own parent":       {emmaID, emmaID},
		"the child as a parent":  {grandmaID, dadID},
		"a grandchild as parent": {grandmaID, emmaID},
		"two in a row":           {dadID, emmaID},
	} {
		res := e.patchUser(admin, c.child, map[string]any{"parent_ids": []string{c.parent}}, nil)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "parent_loop", readProblem(t, res).Code, name)
	}
	require.Empty(t, decode[userView](t, e.getUser(admin, grandmaID)).Parents)
	// A diamond is not a loop: Emma may have both Dad and Grandma as parents.
	require.Equal(t, []string{"Dad", "Grandma"}, summaryNames(e.linkParents(admin, emmaID, dadID, grandmaID).Parents))
}

func TestTheAdminCannotHaveAParentButCanBeOne(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	momID, _ := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")

	res := e.patchUser(admin, adminID, map[string]any{"parent_ids": []string{momID}}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "admin_has_no_parents", readProblem(t, res).Code)
	require.Equal(t, http.StatusOK, e.patchUser(admin, adminID, map[string]any{"parent_ids": []string{}}, nil).StatusCode, "no parents is no change")

	linked := e.linkParents(admin, emmaID, adminID)
	require.Equal(t, []string{"Ada"}, summaryNames(linked.Parents), "the admin is a parent like any other")
	me := decode[userView](t, e.getUser(admin, adminID))
	require.Equal(t, []string{"Emma"}, summaryNames(me.Children))
	require.False(t, me.CanEditParents, "the admin's own parents can't be edited")
	require.True(t, linked.CanEditParents)
}

func TestAParentThatTheCallerCannotSeeIsA422ReferenceNotFound(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	emmaID, _ := e.otherUser("Emma")

	for name, id := range map[string]string{"missing": "9999", "not an id": "abc", "zero": "0"} {
		res := e.patchUser(admin, emmaID, map[string]any{"parent_ids": []string{id}}, nil)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		p := readProblem(t, res)
		require.Equal(t, "reference_not_found", p.Code, name)
	}
}

func TestParentIDsCannotBeNull(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	emmaID, _ := e.otherUser("Emma")

	res := e.patchUser(admin, emmaID, map[string]any{"parent_ids": nil}, nil)

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}

func TestTheAdminCreatesAUserWithParents(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")

	res := e.createUser(admin, map[string]any{"name": "Emma", "birthday": birthday(3, 4, 2020), "parent_ids": []string{momID}})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	u := decode[userView](t, res)
	require.Equal(t, []string{"Mom"}, summaryNames(u.Parents))
	require.Equal(t, []string{"Emma"}, summaryNames(decode[userView](t, e.getUser(mom, momID)).Children))
	require.Equal(t, http.StatusOK, e.getUser(mom, u.ID).StatusCode)

	res = e.createUser(admin, map[string]any{"name": "Zed", "birthday": birthday(3, 4, nil), "parent_ids": []string{"9999"}})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "reference_not_found", readProblem(t, res).Code)
	require.Equal(t, 3, decode[userList](t, e.listUsers(admin, "")).TotalCount, "a refused create makes no user")

	plain := decode[userView](t, e.createUser(admin, map[string]any{"name": "Y", "birthday": birthday(1, 1, nil)}))
	require.NotNil(t, plain.Parents, "no parents is an empty list")
	require.Empty(t, plain.Parents)
}

func TestOnlyTheAdminHasCanEditParents(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)

	require.True(t, decode[userView](t, e.getUser(admin, emmaID)).CanEditParents)
	require.False(t, decode[userView](t, e.getUser(mom, emmaID)).CanEditParents)
	require.False(t, decode[userView](t, e.getUser(emma, emmaID)).CanEditParents)
}
