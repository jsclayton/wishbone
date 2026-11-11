package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestADeactivatedUserIsSeenOnlyByTheAdminTheirParentsAndThemselves(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	graceID, _ := e.otherUser("Grace")
	sueID, sue := e.otherUser("Sue")
	_, zed := e.otherUser("Zed")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID, momID, sueID)
	e.linkParents(admin, graceID, momID)
	e.deactivated(admin, graceID)

	require.Equal(t, http.StatusOK, e.getUser(admin, graceID).StatusCode)
	require.Equal(t, http.StatusOK, e.getUser(mom, graceID).StatusCode, "a parent still sees the child")
	require.Equal(t, http.StatusNotFound, e.getUser(sue, graceID).StatusCode, "a user who shares a group doesn't")
	require.Equal(t, http.StatusNotFound, e.getUser(zed, graceID).StatusCode)

	require.Equal(t, []string{"Ada", "Grace", "Mom", "Sue", "Zed"}, names(decode[userList](t, e.listUsers(admin, ""))))
	asMom := decode[userList](t, e.listUsers(mom, ""))
	require.Equal(t, []string{"Grace", "Mom", "Sue"}, names(asMom), "a parent's list has the child, and total_count counts them")
	require.Equal(t, 3, asMom.TotalCount)
	asSue := decode[userList](t, e.listUsers(sue, ""))
	require.Equal(t, []string{"Mom", "Sue"}, names(asSue))
	require.Equal(t, 2, asSue.TotalCount)
}

func TestADeactivatedUserSeesThemselves(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	e.deactivated(admin, graceID)
	grace, err := e.srv.opts.Users.Get(t.Context(), mustParse(t, graceID))
	require.NoError(t, err)

	got, err := e.srv.opts.Users.GetVisible(t.Context(), grace, grace.ID)

	require.NoError(t, err)
	require.False(t, got.Active())
}

func TestADeactivatedUserIsLeftOutOfAGroupsUsersForEveryoneElse(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	graceID, _ := e.otherUser("Grace")
	sueID, sue := e.otherUser("Sue")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID, momID, sueID)
	e.linkParents(admin, graceID, momID)
	e.deactivated(admin, graceID)

	require.Equal(t, []string{"Grace", "Mom", "Sue"}, names(decode[userList](t, e.groupUsers(admin, family.ID, ""))), "the admin sees them")
	require.Equal(t, []string{"Grace", "Mom", "Sue"}, names(decode[userList](t, e.groupUsers(mom, family.ID, ""))), "a parent does")
	res := decode[userList](t, e.groupUsers(sue, family.ID, ""))
	require.Equal(t, []string{"Mom", "Sue"}, names(res))
	require.Equal(t, 2, res.TotalCount)
	require.Equal(t, []string{"Mom", "Sue"}, names(decode[userList](t, e.listUsers(sue, "group_id="+family.ID))), "so does the filter of GET /users")
}

func TestAChildDoesNotSeeADeactivatedParentButStillGetsTheirSummary(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	dadID, _ := e.otherUser("Dad")
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, emmaID, dadID)
	e.deactivated(admin, dadID)

	require.Equal(t, http.StatusNotFound, e.getUser(emma, dadID).StatusCode)
	require.Equal(t, []string{"Emma"}, names(decode[userList](t, e.listUsers(emma, ""))))
	require.Equal(t, []string{"Dad"}, summaryNames(decode[userView](t, e.getUser(emma, emmaID)).Parents), "a summary doesn't need visibility")
}

func TestOnlyTheDeactivatedUsersSeeTheirDeactivatedAt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	graceID, _ := e.otherUser("Grace")
	sueID, sue := e.otherUser("Sue")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, sueID, momID)
	e.linkParents(admin, graceID, momID)

	var active map[string]any
	decodeInto(t, e.getUser(sue, momID), &active)
	require.NotContains(t, active, "deactivated_at", "a group peer doesn't get the field")
	var asMom map[string]any
	decodeInto(t, e.getUser(mom, graceID), &asMom)
	require.Contains(t, asMom, "deactivated_at")
	require.Nil(t, asMom["deactivated_at"], "an active user has null")
	e.deactivated(admin, graceID)
	decodeInto(t, e.getUser(mom, graceID), &asMom)
	require.NotNil(t, asMom["deactivated_at"])
}

func TestReactivatingMakesTheUserVisibleAgain(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")
	sueID, sue := e.otherUser("Sue")
	family := e.newGroup(admin, "Family")
	e.join(admin, family.ID, graceID, sueID)
	e.deactivated(admin, graceID)
	require.Equal(t, http.StatusNotFound, e.getUser(sue, graceID).StatusCode)

	require.Equal(t, http.StatusOK, e.reactivate(admin, graceID).StatusCode)

	require.Equal(t, http.StatusOK, e.getUser(sue, graceID).StatusCode)
}
