package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
)

func TestOnlyDirectLinksMakeUsersVisible(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	grandmaID, grandma := e.otherUser("Grandma")
	dadID, dad := e.otherUser("Dad")
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, dadID, grandmaID)
	e.linkParents(admin, emmaID, dadID)

	require.Equal(t, []string{"Dad", "Grandma"}, names(decode[userList](t, e.listUsers(grandma, ""))))
	require.Equal(t, []string{"Dad", "Emma", "Grandma"}, names(decode[userList](t, e.listUsers(dad, ""))))
	require.Equal(t, []string{"Dad", "Emma"}, names(decode[userList](t, e.listUsers(emma, ""))))
	require.Equal(t, http.StatusNotFound, e.getUser(grandma, emmaID).StatusCode, "a child's child is not seen")
	require.Equal(t, http.StatusNotFound, e.getUser(emma, grandmaID).StatusCode, "a parent's parent is not seen")
}

func TestAUserSeesTheirChildrenAndParentsByNameWithTheirSummaries(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	dadID, _ := e.otherUser("Dad")
	emmaID, _ := e.otherUser("Emma")
	benID, _ := e.otherUser("Ben")
	e.linkParents(admin, emmaID, momID, dadID)
	e.linkParents(admin, benID, momID)

	asMom := decode[userView](t, e.getUser(mom, momID))

	require.Equal(t, []string{"Ben", "Emma"}, summaryNames(asMom.Children), "by name")
	require.Equal(t, summaryView{ID: benID, Name: "Ben"}, asMom.Children[0])
	emma := decode[userView](t, e.getUser(mom, emmaID))
	require.Equal(t, []string{"Dad", "Mom"}, summaryNames(emma.Parents))
	require.Equal(t, http.StatusNotFound, e.getUser(mom, dadID).StatusCode,
		"a summary in a body does not make Dad visible: Mom shares no link or group with him")
}

func TestAParentSeesTheTimezoneAndConfirmationOfTheChildAndAGroupMemberDoesNot(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	sueID, sue := e.otherUser("Sue")
	e.join(admin, e.newGroup(admin, "Cousins").ID, emmaID, sueID)
	e.linkParents(admin, emmaID, momID)
	require.Equal(t, http.StatusOK, e.patchUser(admin, emmaID, map[string]any{"timezone": "Europe/Paris"}, nil).StatusCode)

	asParent := decode[map[string]json.RawMessage](t, e.getUser(mom, emmaID))
	asMember := decode[map[string]json.RawMessage](t, e.getUser(sue, emmaID))

	require.JSONEq(t, `"Europe/Paris"`, string(asParent["timezone"]))
	require.Contains(t, asParent, "profile_confirmed_at")
	require.NotContains(t, asMember, "timezone")
	require.NotContains(t, asMember, "profile_confirmed_at")
	rows := decode[struct {
		Data []map[string]json.RawMessage `json:"data"`
	}](t, e.listUsers(mom, "")).Data
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Contains(t, row, "timezone", "the collection follows the same rule")
	}
}

func TestAParentMakesLoginLinksForAChildAndSignsInAsThem(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	_, sue := e.otherUser("Sue")
	e.linkParents(admin, emmaID, momID)

	res := e.redeem(e.makeLink(mom, emmaID), "", nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "Emma", decode[redeemBody](t, res).Name)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(sue)}).StatusCode)
}

func TestAParentSeesAndRevokesTheChildsSessions(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emmaToken := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)

	list := decode[sessionList](t, e.listSessions(emmaID, cookie(mom)))
	require.Len(t, list.Data, 1)
	require.True(t, list.Data[0].CanDelete)
	res := e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/sessions/" + list.Data[0].ID, header: cookie(mom)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(emmaToken)}).StatusCode)
}

func TestAParentSeesAndDeletesTheChildsPasskeysButCannotAddOrRenameThem(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	made := decode[passkeyView](t, e.registerPasskey(emmaID, cookie(emma), passkeytest.New(t), "Phone"))

	list := decode[passkeyList](t, e.listPasskeys(emmaID, cookie(mom), ""))
	require.Len(t, list.Data, 1)
	require.False(t, list.Data[0].CanEdit)
	require.True(t, list.Data[0].CanDelete)
	for _, r := range []request{
		{method: http.MethodPost, path: "/users/" + emmaID + "/passkeys/registration"},
		{method: http.MethodPatch, path: "/users/" + emmaID + "/passkeys/" + made.ID, body: map[string]any{"name": "Mine"}},
	} {
		r.header = cookie(mom)
		require.Equal(t, http.StatusForbidden, e.do(r).StatusCode, r.method+" "+r.path)
	}
	res := e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/passkeys/" + made.ID, header: cookie(mom)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, decode[passkeyList](t, e.listPasskeys(emmaID, cookie(emma), "")).Data)
}

func TestAParentEditsTheChildsProfileButCannotChangeTheirParentsOrConfirmIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)

	res := e.patchUser(mom, emmaID, map[string]any{"good_to_know": "Likes cats", "timezone": "Europe/Paris"}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	u := decode[userView](t, res)
	require.Equal(t, "Likes cats", *u.GoodToKnow)
	require.Nil(t, u.ProfileConfirmedAt, "a parent's edit does not confirm the profile")
	require.True(t, u.CanEdit)
	require.True(t, u.CanMakeLoginLink)
	require.True(t, u.CanManageSessions)
	require.False(t, u.CanConfirmProfile)
	require.False(t, u.CanEditParents, "only the admin changes parents")

	res = e.patchUser(mom, emmaID, map[string]any{"parent_ids": []string{}}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "field_not_writable", readProblem(t, res).Code)
	res = e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/confirm-profile", header: cookie(mom)})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, []string{"Mom"}, summaryNames(decode[userView](t, e.getUser(mom, emmaID)).Parents), "nothing changed")
}

func TestAParentHasNoPowerOverTheirOwnParentOrTheirChildsChild(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	grandmaID, grandma := e.otherUser("Grandma")
	dadID, dad := e.otherUser("Dad")
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, dadID, grandmaID)
	e.linkParents(admin, emmaID, dadID)

	// The link goes one way: a child has no power over a parent.
	res := e.patchUser(dad, grandmaID, map[string]any{"name": "Gran"}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPost, path: "/users/" + grandmaID + "/login-links", header: cookie(dad)}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.listSessions(grandmaID, cookie(emma)).StatusCode)
	// Grandma has no power over Emma, her child's child.
	require.Equal(t, http.StatusNotFound, e.patchUser(grandma, emmaID, map[string]any{"name": "E"}, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.listSessions(emmaID, cookie(grandma)).StatusCode)
	require.Equal(t, http.StatusNotFound, e.listPasskeys(emmaID, cookie(grandma), "").StatusCode)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(grandma)}).StatusCode)
}

func TestUnlinkingTakesTheParentsPowersAtOnceButAnEarlierLoginLinkStillWorks(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, emmaToken := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	link := e.makeLink(mom, emmaID)

	unlinked := e.linkParents(admin, emmaID)

	require.Empty(t, unlinked.Parents)
	require.Equal(t, http.StatusNotFound, e.getUser(mom, emmaID).StatusCode, "the sight goes too")
	require.Equal(t, http.StatusNotFound, e.listSessions(emmaID, cookie(mom)).StatusCode)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(mom)}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchUser(mom, emmaID, map[string]any{"name": "E"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.redeem(link, "", nil).StatusCode, "a link made earlier works until it is used or expires")
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: cookie(emmaToken)}).StatusCode, "the child's sessions don't change")
}

func TestAnAvatarLoadsForWhoeverGetsTheSummaryThroughALink(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	grandmaID, grandma := e.otherUser("Grandma")
	dadID, _ := e.otherUser("Dad")
	emmaID, emma := e.otherUser("Emma")
	_, sue := e.otherUser("Sue")
	e.linkParents(admin, dadID, grandmaID)
	e.linkParents(admin, emmaID, dadID)
	img := e.uploaded(emma)
	require.Equal(t, http.StatusOK, e.setAvatar(emma, emmaID, img.ID).StatusCode)

	require.Equal(t, http.StatusNotFound, e.getUser(grandma, emmaID).StatusCode)
	dadSeenByGrandma := decode[userView](t, e.getUser(grandma, dadID))
	require.Equal(t, img.ID, dadSeenByGrandma.Children[0].Avatar.ID, "Grandma gets Emma's summary in Dad's children")
	require.Equal(t, http.StatusOK, e.getImage(grandma, img.ID).StatusCode, "so she loads the avatar in it")
	require.Equal(t, http.StatusNotFound, e.getImage(sue, img.ID).StatusCode)
}
