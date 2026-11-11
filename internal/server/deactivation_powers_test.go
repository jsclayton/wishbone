package server

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

func requireDeactivated(t *testing.T, res *http.Response) {
	t.Helper()
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "user_deactivated", readProblem(t, res).Code)
}

// deactivatedEmma makes Emma, a child of Mom, with a timezone and one passkey,
// and deactivates her. Deactivation revokes her sessions, so it then makes a
// session row for her, as a sign-in that raced the deactivation would. It
// returns Emma's ID, that session's ID, and the passkey's ID.
func (e *env) deactivatedEmma(admin, momID string) (emmaID, sessionID, passkeyID string) {
	e.t.Helper()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	require.Equal(e.t, http.StatusOK, e.patchUser(admin, emmaID, map[string]any{"timezone": "Europe/Paris"}, nil).StatusCode)
	made := decode[passkeyView](e.t, e.registerPasskey(emmaID, cookie(emma), passkeytest.New(e.t), "Phone"))
	e.deactivated(admin, emmaID)
	_, sess, err := session.Create(e.t.Context(), e.db, e.clock.now(), mustParse(e.t, emmaID), "left behind")
	require.NoError(e.t, err)
	return emmaID, strconv.FormatInt(sess.ID, 10), made.ID
}

func TestAParentOfADeactivatedChildReadsThroughThemButChangesNothing(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, sessionID, passkeyID := e.deactivatedEmma(admin, momID)

	u := decode[userView](t, e.getUser(mom, emmaID))
	require.NotNil(t, u.DeactivatedAt)
	require.Equal(t, "Europe/Paris", *u.Timezone, "the parent still sees the private fields")
	require.False(t, u.CanEdit)
	require.False(t, u.CanMakeLoginLink)
	require.False(t, u.CanManageSessions)
	require.False(t, u.CanDeactivate)
	require.False(t, u.CanReactivate, "only the admin reactivates")

	require.Equal(t, http.StatusOK, e.listSessions(emmaID, cookie(mom)).StatusCode, "the reads stay")
	list := decode[passkeyList](t, e.listPasskeys(emmaID, cookie(mom), ""))
	require.Len(t, list.Data, 1)
	require.False(t, list.Data[0].CanDelete)
	require.False(t, list.Data[0].CanEdit)
	sessions := decode[sessionList](t, e.listSessions(emmaID, cookie(mom)))
	require.Len(t, sessions.Data, 1)
	require.False(t, sessions.Data[0].CanDelete)

	requireDeactivated(t, e.patchUser(mom, emmaID, map[string]any{"good_to_know": "Likes cats"}, nil))
	requireDeactivated(t, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(mom)}))
	requireDeactivated(t, e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/sessions/" + sessionID, header: cookie(mom)}))
	requireDeactivated(t, e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/passkeys/" + passkeyID, header: cookie(mom)}))

	require.Nil(t, decode[userView](t, e.getUser(admin, emmaID)).GoodToKnow, "nothing changed")
	require.Len(t, decode[sessionList](t, e.listSessions(emmaID, cookie(admin))).Data, 1)
	require.Len(t, decode[passkeyList](t, e.listPasskeys(emmaID, cookie(admin), "")).Data, 1)
}

func TestTheAdminCanOnlyReactivateAndChangeTheParentsOfADeactivatedUser(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, _ := e.otherUser("Mom")
	dadID, _ := e.otherUser("Dad")
	emmaID, sessionID, passkeyID := e.deactivatedEmma(admin, momID)

	u := decode[userView](t, e.getUser(admin, emmaID))
	require.False(t, u.CanEdit)
	require.False(t, u.CanMakeLoginLink)
	require.False(t, u.CanManageSessions)
	require.False(t, u.CanDeactivate)
	require.True(t, u.CanReactivate)
	require.True(t, u.CanEditParents)

	requireDeactivated(t, e.patchUser(admin, emmaID, map[string]any{"name": "Em"}, nil))
	requireDeactivated(t, e.patchUser(admin, emmaID, map[string]any{"name": "Em", "parent_ids": []string{momID}}, nil))
	requireDeactivated(t, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(admin)}))
	requireDeactivated(t, e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/sessions/" + sessionID, header: cookie(admin)}))
	requireDeactivated(t, e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/passkeys/" + passkeyID, header: cookie(admin)}))

	res := e.patchUser(admin, emmaID, map[string]any{"parent_ids": []string{momID, dadID}}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode, "the parents can change")
	require.Equal(t, []string{"Dad", "Mom"}, summaryNames(decode[userView](t, res).Parents))
	require.Equal(t, http.StatusOK, e.reactivate(admin, emmaID).StatusCode)
	require.Equal(t, http.StatusOK, e.patchUser(admin, emmaID, map[string]any{"name": "Em"}, nil).StatusCode)
}

func TestAParentStillCannotSetParentIDsOnADeactivatedChild(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _, _ := e.deactivatedEmma(admin, momID)

	res := e.patchUser(mom, emmaID, map[string]any{"parent_ids": []string{}}, nil)

	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "field_not_writable", readProblem(t, res).Code, "the field is the admin's; deactivation isn't why")
}

func TestReactivatingGivesTheParentTheirPowersBack(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _, _ := e.deactivatedEmma(admin, momID)
	require.Equal(t, http.StatusOK, e.reactivate(admin, emmaID).StatusCode)

	u := decode[userView](t, e.getUser(mom, emmaID))
	require.True(t, u.CanEdit)
	require.True(t, u.CanMakeLoginLink)
	require.True(t, u.CanManageSessions)
	require.Equal(t, http.StatusOK, e.patchUser(mom, emmaID, map[string]any{"good_to_know": "Likes cats"}, nil).StatusCode)
	require.Equal(t, http.StatusCreated, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(mom)}).StatusCode)
}

func TestAParentKeepsThePowersThatComeThroughAnotherUser(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	dadID, _ := e.otherUser("Dad")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID, dadID)
	e.deactivated(admin, dadID)

	require.Equal(t, http.StatusOK, e.patchUser(mom, emmaID, map[string]any{"good_to_know": "Likes cats"}, nil).StatusCode, "Emma is active; Dad doesn't change Mom's power")
	require.False(t, decode[userView](t, e.getUser(mom, dadID)).CanEdit, "Mom is not Dad's parent, and Dad is deactivated")
}

func TestAUserWhoOnlySharesAGroupWithAUserSeesThemButCannotEditThemOrMakeThemALink(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	grandmaID, grandma := e.otherUser("Grandma")
	emmaID, _ := e.otherUser("Emma")
	e.join(admin, e.newGroup(admin, "Family").ID, grandmaID, emmaID)

	res := e.patchUser(grandma, emmaID, map[string]any{"name": "Em"}, nil)

	require.Equal(t, http.StatusForbidden, res.StatusCode, "Grandma sees Emma, so it is a 403 and not a 404")
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	u := decode[userView](t, e.getUser(grandma, emmaID))
	require.Equal(t, "Emma", u.Name)
	require.False(t, u.CanEdit)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPost, path: "/users/" + emmaID + "/login-links", header: cookie(grandma)}).StatusCode)
}

// racing builds a server whose user service runs before a function that a test
// sets, just before it writes. The function stands for another request that
// commits between the route's first look at the user and its write.
func racing(t *testing.T) (*env, *atomic.Pointer[func()]) {
	t.Helper()
	var before atomic.Pointer[func()]
	e := newEnv(t, func(o *Options) {
		db := o.Store.(*store.Store).DB()
		o.Users = user.NewService(db, func() time.Time {
			if f := before.Swap(nil); f != nil {
				(*f)()
			}
			return time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)
		})
	})
	return e, &before
}

func TestAnEditThatRacesADeactivationIsRefused(t *testing.T) {
	e, before := racing(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	race := func() { e.deactivated(admin, emmaID) }
	before.Store(&race)

	res := e.patchUser(mom, emmaID, map[string]any{"good_to_know": "Likes cats"}, nil)

	requireDeactivated(t, res)
	require.Nil(t, decode[userView](t, e.getUser(mom, emmaID)).GoodToKnow)
}

func TestAnEditThatRacesAnUnlinkIsRefused(t *testing.T) {
	e, before := racing(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	race := func() { e.linkParents(admin, emmaID) }
	before.Store(&race)

	res := e.patchUser(mom, emmaID, map[string]any{"good_to_know": "Likes cats"}, nil)

	require.Equal(t, http.StatusNotFound, res.StatusCode, "Mom no longer sees Emma")
	require.Nil(t, decode[userView](t, e.getUser(admin, emmaID)).GoodToKnow)
}

func TestADeactivatedUsersLoginLinksDontWorkAndWorkAgainAfterReactivation(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	link := e.makeLink(admin, graceID)
	e.deactivated(admin, graceID)

	res := e.redeem(link, "", nil)
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, "login_link_invalid", readProblem(t, res).Code, "the same answer as any other bad link")
	require.Equal(t, http.StatusUnauthorized, e.check(link, nil).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.redeem(link, "bearer", nil).StatusCode)

	require.Equal(t, http.StatusOK, e.reactivate(admin, graceID).StatusCode)
	require.Equal(t, http.StatusOK, e.check(link, nil).StatusCode, "the link wasn't used up")
	require.Equal(t, http.StatusOK, e.redeem(link, "", nil).StatusCode)
}

func TestAnExpiredLoginLinkStaysExpiredAfterReactivation(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	link := e.makeLink(admin, graceID)
	e.deactivated(admin, graceID)
	e.clock.advance(8 * 24 * time.Hour)
	require.Equal(t, http.StatusOK, e.reactivate(admin, graceID).StatusCode)

	require.Equal(t, http.StatusUnauthorized, e.redeem(link, "", nil).StatusCode)
}

func TestAPasskeySignInRefusesADeactivatedUserAndWorksAfterReactivation(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _, a := e.passkeyUser()
	e.deactivated(admin, graceID)

	res := e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), nil)
	requireLoginFailure(t, res, "Grace")

	require.Equal(t, http.StatusOK, e.reactivate(admin, graceID).StatusCode)
	res = e.passkeyLogin(a.Assert(t, e.loginOptions(), passkeyOrigin), nil)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}

func TestADeactivatedUserCannotBeAddedToOrRemovedFromAGroup(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	family := e.newGroup(admin, "Family")
	cousins := e.newGroup(admin, "Cousins")
	e.join(admin, family.ID, graceID)
	e.deactivated(admin, graceID)

	requireDeactivated(t, e.addUserToGroup(admin, cousins.ID, graceID))
	requireDeactivated(t, e.removeGroupUser(admin, family.ID, graceID))
	require.Equal(t, []string{"Grace"}, names(decode[userList](t, e.groupUsers(admin, family.ID, ""))), "membership stays")
	require.Empty(t, decode[userList](t, e.groupUsers(admin, cousins.ID, "")).Data)

	require.Equal(t, http.StatusOK, e.reactivate(admin, graceID).StatusCode)
	require.Equal(t, http.StatusNoContent, e.removeGroupUser(admin, family.ID, graceID).StatusCode)
}

func TestAGroupNotFoundIsStillA404ForADeactivatedUser(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	e.deactivated(admin, graceID)

	require.Equal(t, http.StatusNotFound, e.addUserToGroup(admin, "9999", graceID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.removeGroupUser(admin, "9999", graceID).StatusCode)
}
