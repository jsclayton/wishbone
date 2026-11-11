package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover shared occasions (docs/spec.md, Occasions, Shared
// occasions; docs/routes.md, Occasions): the admin defines them, attaches
// groups or marks them everyone, and they reach the users in those groups.

// addShared makes a shared occasion as the admin and fails the test when the
// server refuses. Marking it everyone is part of the create.
func (e *env) addShared(admin, name string, rule map[string]any, everyone bool) occasionView {
	e.t.Helper()
	body := map[string]any{"kind": "shared", "name": name, "rule": rule}
	if everyone {
		body["is_everyone"] = true
	}
	res := e.postOccasion(admin, body)
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "add shared occasion "+name)
	o := decode[occasionView](e.t, res)
	o.Version = res.Header.Get("ETag")
	return o
}

func TestTheAdminCreatesASharedOccasion(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.postOccasion(admin, map[string]any{"kind": "shared", "name": "Christmas", "rule": annual(12, 25)})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	o := decode[occasionView](t, res)
	require.NotEmpty(t, o.ID)
	require.Equal(t, "/api/v2/occasions/"+o.ID, res.Header.Get("Location"))
	require.Equal(t, "shared", o.Kind)
	require.Equal(t, "Christmas", o.Name)
	require.Nil(t, o.User, "a shared occasion has no user")
	require.Equal(t, "2026-12-25", *o.NextDate)
	require.NotNil(t, o.IsEveryone)
	require.False(t, *o.IsEveryone, "not everyone unless the admin marks it")
	require.True(t, o.CanEdit)
	require.True(t, o.CanDelete)
	require.True(t, o.CanAttachGroup, "it is attached to no groups yet, and not everyone")
}

func TestTheAdminMarksASharedOccasionEveryoneWhenCreatingIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	o := e.addShared(admin, "New Year", annual(1, 1), true)

	require.True(t, *o.IsEveryone)
	require.False(t, o.CanAttachGroup, "no group is attached to an occasion marked everyone")
}

func TestOnlyTheAdminCreatesASharedOccasion(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")

	res := e.postOccasion(grace, map[string]any{"kind": "shared", "name": "Christmas", "rule": annual(12, 25)})

	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)
}

func TestOnlyASharedOccasionTakesIsEveryoneAndOnlyACustomOneTakesAUser(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()

	custom := e.postOccasion(admin, map[string]any{"kind": "custom", "name": "Mine", "rule": annual(5, 5), "is_everyone": true})
	require.Equal(t, http.StatusUnprocessableEntity, custom.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, custom).Code)

	shared := e.postOccasion(admin, map[string]any{"kind": "shared", "name": "Ours", "rule": annual(5, 5), "user_id": adminID})
	require.Equal(t, http.StatusUnprocessableEntity, shared.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, shared).Code)

	bad := e.postOccasion(admin, map[string]any{"kind": "shared", "name": "", "rule": annual(5, 5)})
	require.Equal(t, http.StatusUnprocessableEntity, bad.StatusCode)
}

func TestACustomAndABirthdayOccasionHaveNoIsEveryoneAndCannotAttachAGroup(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	custom := e.addOccasion(grace, "Mine", annual(5, 5), "")

	for _, o := range decode[occasionList](t, e.listOccasions(grace, "")).Data {
		require.Nil(t, o.IsEveryone, o.Name)
		require.False(t, o.CanAttachGroup, o.Name)
	}
	require.Nil(t, decode[occasionView](t, e.getOccasion(grace, custom.ID)).IsEveryone)
}

func TestTheAdminChangesASharedOccasionsNameRuleAndEveryoneMark(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Christmas", annual(12, 25), false)

	res := e.patchOccasion(admin, o.ID, map[string]any{"name": "Xmas", "rule": annual(12, 24), "is_everyone": true}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[occasionView](t, res)
	require.Equal(t, "Xmas", got.Name)
	require.Equal(t, "2026-12-24", *got.NextDate)
	require.True(t, *got.IsEveryone)
	require.False(t, got.CanAttachGroup)

	res = e.patchOccasion(admin, o.ID, map[string]any{"is_everyone": false}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	got = decode[occasionView](t, res)
	require.False(t, *got.IsEveryone)
	require.True(t, got.CanAttachGroup)
	require.Equal(t, "Xmas", got.Name, "a field left out stays")
}

func TestASharedOccasionPatchChecksIsEveryoneAndTheVersion(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Christmas", annual(12, 25), false)

	res := e.patchOccasion(admin, o.ID, map[string]any{"is_everyone": nil}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)

	res = e.patchOccasion(admin, o.ID, map[string]any{"name": "Xmas"}, http.Header{"If-Match": {`"stale"`}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)

	res = e.patchOccasion(admin, o.ID, map[string]any{"name": "Xmas"}, http.Header{"If-Match": {o.Version}})
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func TestACustomOccasionCannotBeMarkedEveryone(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	custom := e.addOccasion(grace, "Mine", annual(5, 5), "")

	res := e.patchOccasion(grace, custom.ID, map[string]any{"is_everyone": true}, nil)

	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "field_not_writable", readProblem(t, res).Code)
}

func TestOnlyTheAdminChangesOrDeletesASharedOccasion(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	_, grace := e.otherUser("Grace")
	o := e.addShared(admin, "Christmas", annual(12, 25), true)

	seen := decode[occasionView](t, e.getOccasion(grace, o.ID))
	require.False(t, seen.CanEdit)
	require.False(t, seen.CanDelete)
	require.False(t, seen.CanAttachGroup)
	require.True(t, *seen.IsEveryone, "everyone sees the mark")

	res := e.patchOccasion(grace, o.ID, map[string]any{"name": "Mine"}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	require.Equal(t, http.StatusForbidden, e.deleteOccasion(grace, o.ID).StatusCode)
	require.Equal(t, "Christmas", decode[occasionView](t, e.getOccasion(admin, o.ID)).Name)
}

func TestTheAdminDeletesASharedOccasion(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	o := e.addShared(admin, "Christmas", annual(12, 25), false)

	require.Equal(t, http.StatusNoContent, e.deleteOccasion(admin, o.ID).StatusCode)

	require.Equal(t, http.StatusNotFound, e.getOccasion(admin, o.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.deleteOccasion(admin, o.ID).StatusCode)
}
