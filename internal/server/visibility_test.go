package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func (e *env) getUser(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/users/" + id, header: cookie(token)})
}

func TestUsersWhoShareAGroupSeeEachOtherAndOnlyEachOther(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	zedID, zed := e.otherUser("Zed")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID, barbID)

	graceSees := decode[userList](t, e.listUsers(grace, ""))
	require.Equal(t, []string{"Barbara", "Grace"}, names(graceSees))
	require.Equal(t, 2, graceSees.TotalCount, "total_count follows the same rule")
	require.Equal(t, []string{"Barbara", "Grace"}, names(decode[userList](t, e.listUsers(barb, ""))))
	zedSees := decode[userList](t, e.listUsers(zed, ""))
	require.Equal(t, []string{"Zed"}, names(zedSees))
	require.Equal(t, 1, zedSees.TotalCount)

	require.Equal(t, http.StatusOK, e.getUser(grace, barbID).StatusCode)
	require.Equal(t, http.StatusOK, e.getUser(barb, graceID).StatusCode)
	for _, id := range []string{graceID, barbID} {
		res := e.getUser(zed, id)
		require.Equal(t, http.StatusNotFound, res.StatusCode)
		require.Equal(t, "not_found", readProblem(t, res).Code)
	}
	require.Equal(t, http.StatusNotFound, e.getUser(grace, zedID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getUser(grace, "9999").StatusCode, "a user who doesn't exist looks the same")
}

func TestTheAdminStillSeesEveryUserAndAUserOutsideEveryGroupSeesOnlyThemselves(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	e.otherUser("Zed")
	e.join(admin, e.newGroup(admin, "Family").ID, graceID)

	list := decode[userList](t, e.listUsers(admin, ""))

	require.Equal(t, []string{"Ada", "Grace", "Zed"}, names(list))
	require.Equal(t, 3, list.TotalCount)
}

func TestSharingAGroupThroughNestingIsEnough(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, hedy := e.otherUser("Hedy")
	ivyID, ivy := e.otherUser("Ivy")
	inlaws := e.newGroup(admin, "In-laws")
	larkin := e.newGroup(admin, "Larkin family")
	mother := e.newGroup(admin, "Mother-in-law")
	e.nest(admin, inlaws.ID, larkin.ID)
	e.nest(admin, inlaws.ID, mother.ID)
	e.join(admin, larkin.ID, graceID)
	e.join(admin, mother.ID, hedyID)
	e.join(admin, inlaws.ID, ivyID)

	// Grace is in Larkin family, which is in In-laws. Hedy is in Mother-in-law,
	// which is also in In-laws. In-laws contains both, so they see each other.
	require.Equal(t, http.StatusOK, e.getUser(grace, hedyID).StatusCode)
	require.Equal(t, http.StatusOK, e.getUser(hedy, graceID).StatusCode)
	// Ivy is directly in In-laws, which contains both families' users.
	require.Equal(t, []string{"Grace", "Hedy", "Ivy"}, names(decode[userList](t, e.listUsers(ivy, ""))))
	require.Equal(t, []string{"Grace", "Hedy", "Ivy"}, names(decode[userList](t, e.listUsers(grace, ""))))
	require.Equal(t, http.StatusOK, e.getUser(grace, ivyID).StatusCode)
}

func TestUsersInSiblingBranchesNestedDeepSeeEachOtherThroughTheSharedRootOnly(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, hedy := e.otherUser("Hedy")
	ivyID, ivy := e.otherUser("Ivy")
	// Root > A > A1 > A2 holds Grace, and Root > B > B1 > B2 holds Hedy: two
	// branches, each three levels below the root they share. Ivy is alone in
	// Other > O1 > O2 > O3.
	chain := func(names ...string) []string {
		ids := make([]string, len(names))
		for i, name := range names {
			ids[i] = e.newGroup(admin, name).ID
			if i > 0 {
				e.nest(admin, ids[i-1], ids[i])
			}
		}
		return ids
	}
	left := chain("Root", "A", "A1", "A2")
	right := chain("B", "B1", "B2")
	e.nest(admin, left[0], right[0])
	other := chain("Other", "O1", "O2", "O3")
	e.join(admin, left[3], graceID)
	e.join(admin, right[2], hedyID)
	e.join(admin, other[3], ivyID)

	require.Equal(t, []string{"Grace", "Hedy"}, names(decode[userList](t, e.listUsers(grace, ""))))
	require.Equal(t, []string{"Grace", "Hedy"}, names(decode[userList](t, e.listUsers(hedy, ""))))
	require.Equal(t, http.StatusNotFound, e.getUser(ivy, graceID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getUser(grace, ivyID).StatusCode)

	// The group filter follows the same rule at depth: Grace sees Root and A, and
	// Hedy sees neither A nor the branch that she is not in.
	require.Equal(t, []string{"Grace", "Hedy"}, names(decode[userList](t, e.listUsers(grace, "group_id="+left[0]))))
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.listUsers(grace, "group_id="+left[1]))))
	require.Empty(t, names(decode[userList](t, e.listUsers(hedy, "group_id="+left[1]))), "Hedy is not in A, so can't see it")
	require.Equal(t, []string{"Hedy"}, names(decode[userList](t, e.listUsers(hedy, "group_id="+right[0]))))
	require.Empty(t, names(decode[userList](t, e.listUsers(grace, "group_id="+other[0]))))
}

func TestUsersInTwoGroupsThatAreInsideTheSameOuterGroupSeeEachOther(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, _ := e.otherUser("Hedy")
	_, ivy := e.otherUser("Ivy")
	outer := e.newGroup(admin, "Outer")
	left := e.newGroup(admin, "Left")
	right := e.newGroup(admin, "Right")
	e.nest(admin, outer.ID, left.ID)
	e.nest(admin, outer.ID, right.ID)
	e.join(admin, left.ID, graceID)
	e.join(admin, right.ID, hedyID)

	require.Equal(t, http.StatusOK, e.getUser(grace, hedyID).StatusCode, "Outer contains both")
	require.Equal(t, http.StatusNotFound, e.getUser(ivy, graceID).StatusCode, "a user in no group sees nobody")
}

func TestUsersInGroupsThatNoGroupContainsTogetherDoNotSeeEachOther(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, hedy := e.otherUser("Hedy")
	left := e.newGroup(admin, "Left")
	right := e.newGroup(admin, "Right")
	e.join(admin, left.ID, graceID)
	e.join(admin, right.ID, hedyID)

	require.Equal(t, http.StatusNotFound, e.getUser(grace, hedyID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getUser(hedy, graceID).StatusCode)
}

func TestTakingAUserOutOfTheGroupTakesAwayTheSight(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, _ := e.otherUser("Barbara")
	g := e.newGroup(admin, "Family")
	e.join(admin, g.ID, graceID, barbID)
	require.Equal(t, http.StatusOK, e.getUser(grace, barbID).StatusCode)

	require.Equal(t, http.StatusNoContent, e.removeGroupUser(admin, g.ID, barbID).StatusCode)

	require.Equal(t, http.StatusNotFound, e.getUser(grace, barbID).StatusCode)
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.listUsers(grace, ""))))
}

func TestUnnestingOrDeletingAGroupTakesAwayTheSightItGave(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, _ := e.otherUser("Hedy")
	outer := e.newGroup(admin, "Outer")
	inner := e.newGroup(admin, "Inner")
	e.nest(admin, outer.ID, inner.ID)
	e.join(admin, outer.ID, graceID)
	e.join(admin, inner.ID, hedyID)
	require.Equal(t, http.StatusOK, e.getUser(grace, hedyID).StatusCode)

	require.Equal(t, http.StatusNoContent, e.removeGroupGroup(admin, outer.ID, inner.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getUser(grace, hedyID).StatusCode)

	e.nest(admin, outer.ID, inner.ID)
	require.Equal(t, http.StatusOK, e.getUser(grace, hedyID).StatusCode)
	require.Equal(t, http.StatusNoContent, e.do(request{method: http.MethodDelete, path: "/groups/" + outer.ID, header: cookie(admin)}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getUser(grace, hedyID).StatusCode)
}

func TestTimezoneAndProfileConfirmedAtAreAbsentForAUserWhoSharesAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID, barbID)
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"timezone": "Europe/Paris"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{method: http.MethodPost, path: "/users/" + graceID + "/confirm-profile", header: cookie(grace)}).StatusCode)

	own := decode[map[string]json.RawMessage](t, e.getUser(grace, graceID))
	require.JSONEq(t, `"Europe/Paris"`, string(own["timezone"]))
	require.NotEqual(t, "null", string(own["profile_confirmed_at"]))
	asAdmin := decode[map[string]json.RawMessage](t, e.getUser(admin, graceID))
	require.JSONEq(t, `"Europe/Paris"`, string(asAdmin["timezone"]))
	require.Contains(t, asAdmin, "profile_confirmed_at")

	res := e.getUser(barb, graceID)
	require.Equal(t, http.StatusOK, res.StatusCode)
	other := decode[map[string]json.RawMessage](t, res)
	require.NotContains(t, other, "timezone", "absent, not null")
	require.NotContains(t, other, "profile_confirmed_at", "absent, not null")
	for _, field := range []string{"id", "name", "last_name", "avatar", "birthday", "good_to_know", "is_admin", "created_at", "can_edit"} {
		require.Contains(t, other, field)
	}
	require.JSONEq(t, `false`, string(other["can_edit"]))
	require.JSONEq(t, `false`, string(other["can_make_login_link"]))
	require.JSONEq(t, `false`, string(other["can_manage_sessions"]))
}

func TestTheHiddenFieldsAreAbsentFromCollectionRowsToo(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID, barbID)
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"timezone": "Europe/Paris"}, nil).StatusCode)

	for name, res := range map[string]*http.Response{"/users": e.listUsers(barb, ""), "/groups/{id}/users": e.groupUsers(barb, family.ID, "")} {
		var list struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&list), name)
		require.Len(t, list.Data, 2, name)
		for _, row := range list.Data {
			var id string
			require.NoError(t, json.Unmarshal(row["id"], &id))
			if id == graceID {
				require.NotContains(t, row, "timezone", name)
				require.NotContains(t, row, "profile_confirmed_at", name)
			} else {
				require.Contains(t, row, "timezone", name+": Barbara sees her own")
				require.JSONEq(t, `null`, string(row["timezone"]), name)
				require.Contains(t, row, "profile_confirmed_at", name)
			}
		}
	}
}

func TestTheVersionOfAUserIsTheBodyTheCallerSees(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	e.join(admin, e.newGroup(admin, "Family").ID, graceID, barbID)
	before := e.getUser(barb, graceID).Header.Get("ETag")

	// A change to a field Barbara can't see leaves her version as it was.
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"timezone": "Europe/Paris"}, nil).StatusCode)
	require.Equal(t, before, e.getUser(barb, graceID).Header.Get("ETag"))
	// A change to one she can see moves it.
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"good_to_know": "Tea"}, nil).StatusCode)
	require.NotEqual(t, before, e.getUser(barb, graceID).Header.Get("ETag"))
}

func TestAUserWhoSharesAGroupCannotEditOrSignInForTheOther(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	e.join(admin, e.newGroup(admin, "Family").ID, graceID, barbID)

	res := e.patchUser(barb, graceID, map[string]any{"name": "Nope"}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	res = e.do(request{method: http.MethodPost, path: "/users/" + graceID + "/confirm-profile", header: cookie(barb)})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPost, path: "/users/" + graceID + "/login-links", header: cookie(barb)}).StatusCode, "not an actor for Grace")
	require.Equal(t, http.StatusNotFound, e.do(request{path: "/users/" + graceID + "/sessions", header: cookie(barb)}).StatusCode)
}

func TestAnAvatarLoadsForAUserWhoSharesAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	_, zed := e.otherUser("Zed")
	img := e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, graceID, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(barb, img.ID).StatusCode, "no group yet")
	e.join(admin, e.newGroup(admin, "Family").ID, graceID, barbID)

	require.Equal(t, http.StatusOK, e.getImage(barb, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(zed, img.ID).StatusCode)
}

func TestAnAvatarCanBeAttachedToAUserOnlyByThoseWhoSeeTheImage(t *testing.T) {
	// The uploader sees an image before it is attached; being in a group with
	// the owner of an avatar does not let anyone attach that avatar elsewhere.
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	barbID, barb := e.otherUser("Barbara")
	e.join(admin, e.newGroup(admin, "Family").ID, graceID, barbID)
	img := e.uploaded(grace)
	require.Equal(t, http.StatusOK, e.setAvatar(grace, graceID, img.ID).StatusCode)

	res := e.setAvatar(barb, barbID, img.ID)

	require.Equal(t, http.StatusConflict, res.StatusCode, "Barbara sees Grace's avatar, which another user holds")
	require.Equal(t, "image_attached", readProblem(t, res).Code)
}
