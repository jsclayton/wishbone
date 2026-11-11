package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover who sees an occasion, who changes it, and what a deactivated
// user stops (docs/routes.md, Occasion fields; docs/spec.md, Occasions,
// Screens, and The admin).

func TestAUserSeesTheOccasionsOfTheUsersTheySeeAndNoOthers(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	wedding := e.addOccasion(f.mom, "Wedding day", annual(5, 4), "")

	// Sue shares a group with Mom: she sees Mom's occasions, and can't change them.
	got := decode[occasionView](t, e.getOccasion(f.sue, wedding.ID))
	require.Equal(t, "Wedding day", got.Name)
	require.False(t, got.CanEdit)
	require.False(t, got.CanDelete)
	sues := decode[occasionList](t, e.listOccasions(f.sue, "sort=name"))
	require.Equal(t, []string{"Birthday", "Birthday", "Wedding day"}, occasionNames(sues), "Sue's birthday, Mom's, and Mom's custom occasion")
	require.Equal(t, 3, sues.TotalCount)

	// Dad and Hedy share no group with Mom.
	require.Equal(t, http.StatusNotFound, e.getOccasion(f.dad, wedding.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getOccasion(f.hedy, wedding.ID).StatusCode)
	require.Equal(t, 1, decode[occasionList](t, e.listOccasions(f.dad, "")).TotalCount, "only Dad's birthday")
}

func TestTheAdminSeesEveryBirthdayAndNoCustomOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	wedding := e.addOccasion(f.mom, "Wedding day", annual(5, 4), "")
	own := e.addOccasion(f.admin, "Ada's day", annual(7, 7), "")

	all := decode[occasionList](t, e.listOccasions(f.admin, "sort=name"))

	require.Equal(t, []string{"Ada's day", "Birthday", "Birthday", "Birthday", "Birthday", "Birthday"}, occasionNames(all),
		"five birthdays, and the admin's own custom occasion")
	require.Equal(t, 6, all.TotalCount)
	require.Equal(t, http.StatusNotFound, e.getOccasion(f.admin, wedding.ID).StatusCode, "the admin's access gives no custom occasion")
	require.Equal(t, http.StatusOK, e.getOccasion(f.admin, own.ID).StatusCode)
	require.Equal(t, 0, decode[occasionList](t, e.listOccasions(f.admin, "kind=custom&user_id="+f.momID)).TotalCount)
	require.Equal(t, 1, decode[occasionList](t, e.listOccasions(f.admin, "kind=birthday&user_id="+f.momID)).TotalCount)
	// The admin who is a child's parent sees the child's custom occasions as any parent does.
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.adminID)
	spring := e.addOccasion(emma, "Spring show", annual(4, 4), "")
	seen := decode[occasionView](t, e.getOccasion(f.admin, spring.ID))
	require.True(t, seen.CanEdit)
}

func TestAParentManagesTheirChildsCustomOccasions(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)

	res := e.postOccasion(f.mom, map[string]any{"kind": "custom", "name": "Recital", "rule": annual(3, 3), "user_id": emmaID})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	o := decode[occasionView](t, res)
	require.Equal(t, emmaID, o.User.ID)
	require.True(t, o.CanEdit, "the parent edits through the child")
	require.True(t, o.CanDelete)
	asChild := decode[occasionView](t, e.getOccasion(emma, o.ID))
	require.True(t, asChild.CanEdit)
	require.Equal(t, http.StatusNotFound, e.getOccasion(f.dad, o.ID).StatusCode, "another parent-less user sees nothing")

	renamed := e.patchOccasion(f.mom, o.ID, map[string]any{"name": "Piano recital"}, nil)
	require.Equal(t, http.StatusOK, renamed.StatusCode)
	require.Equal(t, "Piano recital", decode[occasionView](t, renamed).Name)
	require.Equal(t, http.StatusNoContent, e.deleteOccasion(f.mom, o.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getOccasion(emma, o.ID).StatusCode)
}

func TestACustomOccasionIsAddedOnlyForYourselfOrYourChild(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)

	for name, tc := range map[string]struct {
		token, userID string
		code          string
		status        int
	}{
		"a user the caller sees":        {f.sue, f.momID, "reference_not_allowed", http.StatusForbidden},
		"a child the caller can't see":  {f.dad, emmaID, "reference_not_found", http.StatusUnprocessableEntity},
		"a user that doesn't exist":     {f.mom, "9999", "reference_not_found", http.StatusUnprocessableEntity},
		"not an ID":                     {f.mom, "abc", "reference_not_found", http.StatusUnprocessableEntity},
		"the admin, who isn't a parent": {f.admin, emmaID, "reference_not_allowed", http.StatusForbidden},
	} {
		res := e.postOccasion(tc.token, map[string]any{"kind": "custom", "name": "Nope", "rule": annual(1, 1), "user_id": tc.userID})
		require.Equal(t, tc.status, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	named := e.postOccasion(f.mom, map[string]any{"kind": "custom", "name": "Mine", "rule": annual(1, 1), "user_id": f.momID})
	require.Equal(t, http.StatusCreated, named.StatusCode, "naming oneself is the default")
	require.Equal(t, 0, decode[occasionList](t, e.listOccasions(f.sue, "kind=custom&user_id="+f.sueID)).TotalCount)
}

func TestNobodyChangesABirthdayOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	birthday := decode[occasionList](t, e.listOccasions(emma, "user_id="+emmaID)).Data[0]
	require.False(t, birthday.CanEdit)
	require.False(t, birthday.CanDelete)

	// The user, their parent, someone who only sees it, and the admin all get the
	// same answer.
	for name, token := range map[string]string{"the user": emma, "their parent": f.mom, "the admin": f.admin} {
		res := e.patchOccasion(token, birthday.ID, map[string]any{"name": "Emma's day"}, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "birthday", readProblem(t, res).Code, name)
		res = e.deleteOccasion(token, birthday.ID)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "birthday", readProblem(t, res).Code, name)
	}
	require.Equal(t, "Birthday", decode[occasionView](t, e.getOccasion(emma, birthday.ID)).Name)
}

func TestACustomOccasionIsChangedOnlyByItsUserOrTheirParent(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	wedding := e.addOccasion(f.mom, "Wedding day", annual(5, 4), "")

	// Sue sees it and can't change it. Dad can't see it.
	res := e.patchOccasion(f.sue, wedding.ID, map[string]any{"name": "Mine"}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	require.Equal(t, http.StatusForbidden, e.deleteOccasion(f.sue, wedding.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchOccasion(f.dad, wedding.ID, map[string]any{"name": "Mine"}, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.deleteOccasion(f.dad, wedding.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchOccasion(f.admin, wedding.ID, map[string]any{"name": "Mine"}, nil).StatusCode, "the admin's access gives no custom occasion")
	require.Equal(t, http.StatusNotFound, e.deleteOccasion(f.admin, wedding.ID).StatusCode)
	require.Equal(t, "Wedding day", decode[occasionView](t, e.getOccasion(f.mom, wedding.ID)).Name)
}

func TestAPatchChangesTheNameAndTheRuleAndNothingElse(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	o := e.addOccasion(f.mom, "Wedding day", annual(5, 4), "")

	res := e.patchOccasion(f.mom, o.ID, map[string]any{"name": "  Anniversary  "}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotEmpty(t, res.Header.Get("ETag"))
	renamed := decode[occasionView](t, res)
	require.Equal(t, "Anniversary", renamed.Name)
	require.Equal(t, o.Rule, renamed.Rule, "a field that is absent doesn't change")
	require.NotEqual(t, o.Version, res.Header.Get("ETag"))

	res = e.patchOccasion(f.mom, o.ID, map[string]any{"rule": map[string]any{"kind": "last_weekday", "weekday": 6, "month": 12}}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	moved := decode[occasionView](t, res)
	six := 6
	require.Equal(t, ruleView{Kind: "last_weekday", Month: 12, Weekday: &six}, moved.Rule, "the rule is replaced whole")
	require.Equal(t, "2026-12-26", *moved.NextDate, "the last Saturday of December")
	require.Equal(t, "Anniversary", moved.Name)

	for name, tc := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"a null name":        {map[string]any{"name": nil}, http.StatusUnprocessableEntity, "invalid_input"},
		"a blank name":       {map[string]any{"name": " "}, http.StatusUnprocessableEntity, "invalid_input"},
		"a null rule":        {map[string]any{"rule": nil}, http.StatusUnprocessableEntity, "invalid_input"},
		"a rule with no day": {map[string]any{"rule": map[string]any{"kind": "annual", "month": 1}}, http.StatusUnprocessableEntity, "invalid_input"},
		"February 30":        {map[string]any{"rule": annual(2, 30)}, http.StatusUnprocessableEntity, "invalid_input"},
		"the kind":           {map[string]any{"kind": "shared"}, http.StatusForbidden, "field_not_writable"},
		"the next date":      {map[string]any{"next_date": "2030-01-01"}, http.StatusForbidden, "field_not_writable"},
		"the user":           {map[string]any{"user": nil}, http.StatusForbidden, "field_not_writable"},
		"no such field":      {map[string]any{"mood": "red"}, http.StatusUnprocessableEntity, "invalid_input"},
	} {
		res := e.patchOccasion(f.mom, o.ID, tc.body, nil)
		require.Equal(t, tc.status, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	require.Equal(t, "Anniversary", decode[occasionView](t, e.getOccasion(f.mom, o.ID)).Name, "a refused patch changes nothing")
}

func TestAnIfMatchThatNoLongerFitsIsA412AndChangesNothing(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	o := e.addOccasion(f.mom, "Wedding day", annual(5, 4), "")
	stale := o.Version
	require.Equal(t, http.StatusOK, e.patchOccasion(f.mom, o.ID, map[string]any{"name": "Anniversary"}, nil).StatusCode)

	res := e.patchOccasion(f.mom, o.ID, map[string]any{"name": "Again"}, http.Header{"If-Match": {stale}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)
	res = e.do(request{method: http.MethodDelete, path: "/occasions/" + o.ID, header: func() http.Header {
		h := cookie(f.mom)
		h.Set("If-Match", stale)
		return h
	}()})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "Anniversary", decode[occasionView](t, e.getOccasion(f.mom, o.ID)).Name)

	current := decode[occasionView](t, e.getOccasion(f.mom, o.ID)).Version
	require.NotEqual(t, stale, current)
	require.Equal(t, http.StatusOK, e.patchOccasion(f.mom, o.ID, map[string]any{"name": "Again"}, http.Header{"If-Match": {current}}).StatusCode)
}

func TestAParentOfADeactivatedChildReadsTheirOccasionsAndChangesNone(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	recital := e.addOccasion(f.mom, "Recital", annual(3, 3), emmaID)
	e.deactivated(f.admin, emmaID)

	seen := decode[occasionView](t, e.getOccasion(f.mom, recital.ID))
	require.Equal(t, "Recital", seen.Name, "the read stays")
	require.False(t, seen.CanEdit)
	require.False(t, seen.CanDelete)
	require.False(t, decode[userView](t, e.getUser(f.mom, emmaID)).CanAddOccasion)
	requireDeactivated(t, e.patchOccasion(f.mom, recital.ID, map[string]any{"name": "Nope"}, nil))
	requireDeactivated(t, e.deleteOccasion(f.mom, recital.ID))
	requireDeactivated(t, e.postOccasion(f.mom, map[string]any{"kind": "custom", "name": "Nope", "rule": annual(1, 1), "user_id": emmaID}))
	require.Equal(t, 2, decode[occasionList](t, e.listOccasions(f.mom, "user_id="+emmaID)).TotalCount, "the birthday and Recital")

	e.reactivate(f.admin, emmaID)
	require.True(t, decode[occasionView](t, e.getOccasion(f.mom, recital.ID)).CanEdit)
	require.Equal(t, http.StatusOK, e.patchOccasion(f.mom, recital.ID, map[string]any{"name": "Back"}, nil).StatusCode)
}

func TestADeactivatedUsersBirthdayIsSeenOnlyByThoseWhoSeeThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.deactivated(f.admin, f.momID)

	// Sue shared a group with Mom, but a deactivated user is seen only by the
	// admin, their parents, and themselves.
	require.Equal(t, 1, decode[occasionList](t, e.listOccasions(f.sue, "")).TotalCount, "only Sue's own")
	require.Equal(t, 5, decode[occasionList](t, e.listOccasions(f.admin, "")).TotalCount, "the admin still sees every birthday")
}

func TestUsersHaveCanAddOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)

	canAdd := func(token, id string) bool { return decode[userView](t, e.getUser(token, id)).CanAddOccasion }

	require.True(t, canAdd(f.mom, f.momID), "the user")
	require.True(t, canAdd(f.mom, emmaID), "their parent")
	require.False(t, canAdd(f.sue, f.momID), "someone who sees the user")
	require.False(t, canAdd(f.admin, f.momID), "the admin, who is neither")
	require.True(t, canAdd(f.admin, f.adminID), "the admin for themselves")
	rows := decode[userList](t, e.listUsers(f.mom, ""))
	for _, u := range rows.Data {
		require.Equal(t, u.ID == f.momID || u.ID == emmaID, u.CanAddOccasion, u.Name+" in the collection")
	}
}

func TestDeletingAUserDeletesTheirOccasions(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addOccasion(f.hedy, "Name day", annual(8, 8), "")

	_, err := e.db.ExecContext(t.Context(), `DELETE FROM sessions WHERE user_id = ?`, f.hedyID)
	require.NoError(t, err)
	_, err = e.db.ExecContext(t.Context(), `DELETE FROM users WHERE id = ?`, f.hedyID)

	require.NoError(t, err)
	var n int
	require.NoError(t, e.db.GetContext(t.Context(), &n, `SELECT count(*) FROM occasions WHERE user_id = ?`, f.hedyID))
	require.Zero(t, n)
}
