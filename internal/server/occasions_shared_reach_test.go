package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover who a shared occasion applies to, and who sees it
// (docs/routes.md, Occasion fields and Occasion routes): the users in its
// groups with nesting, or every user; and `?user_id=` and `GET /users?occasion_id=`.

// reunion is a world for the reach of a shared occasion. "Cousins" holds Mom and
// Sue, and the group "Kids" is inside it with Kit. Tom shares the group "Friends"
// with Mom only. Nan is Mom's parent. Dad and Hedy are in no group. "Reunion" is
// attached to Cousins.
type reunion struct {
	admin                                    string
	mom, sue, kit, tom, nan, dad, hedy       string // session tokens
	momID, sueID, kitID, tomID, nanID, dadID string
	hedyID, cousinsID, kidsID, friendsID     string
	occasion                                 occasionView
}

func (e *env) reunion() reunion {
	e.t.Helper()
	adminID, admin := e.signUp()
	r := reunion{admin: admin}
	r.momID, r.mom = e.otherUser("Mom")
	r.sueID, r.sue = e.otherUser("Sue")
	r.kitID, r.kit = e.otherUser("Kit")
	r.tomID, r.tom = e.otherUser("Tom")
	r.nanID, r.nan = e.otherUser("Nan")
	r.dadID, r.dad = e.otherUser("Dad")
	r.hedyID, r.hedy = e.otherUser("Hedy")
	_ = adminID
	r.cousinsID = e.newGroup(admin, "Cousins").ID
	r.kidsID = e.newGroup(admin, "Kids").ID
	r.friendsID = e.newGroup(admin, "Friends").ID
	e.join(admin, r.cousinsID, r.momID, r.sueID)
	e.join(admin, r.kidsID, r.kitID)
	e.nest(admin, r.cousinsID, r.kidsID)
	e.join(admin, r.friendsID, r.tomID, r.momID)
	e.linkParents(admin, r.momID, r.nanID)
	r.occasion = e.addShared(admin, "Reunion", annual(7, 4), false)
	e.attach(admin, r.occasion.ID, r.cousinsID)
	return r
}

func userNames(l userList) []string {
	out := make([]string, len(l.Data))
	for i, u := range l.Data {
		out[i] = u.Name
	}
	return out
}

func TestASharedOccasionAppliesToTheUsersInItsGroupsWithNesting(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()

	// Mom and Sue are directly in Cousins, and Kit is in Kids, which is inside it.
	for name, token := range map[string]string{"Mom": r.mom, "Sue": r.sue, "Kit": r.kit} {
		require.Equal(t, http.StatusOK, e.getOccasion(token, r.occasion.ID).StatusCode, name)
		require.Contains(t, occasionNames(decode[occasionList](t, e.listOccasions(token, "kind=shared"))), "Reunion", name)
	}
	// Dad and Hedy are in no group of it.
	for name, token := range map[string]string{"Dad": r.dad, "Hedy": r.hedy} {
		require.Equal(t, http.StatusNotFound, e.getOccasion(token, r.occasion.ID).StatusCode, name)
		require.Empty(t, decode[occasionList](t, e.listOccasions(token, "kind=shared")).Data, name)
	}
	// The admin manages every shared occasion.
	require.Equal(t, http.StatusOK, e.getOccasion(r.admin, r.occasion.ID).StatusCode)

	// An outer group does not reach up: an occasion on Kids applies to Kit and not
	// to Sue, though Sue sees Kit and so sees the occasion.
	kidsOnly := e.addShared(r.admin, "Kids' day", annual(6, 1), false)
	e.attach(r.admin, kidsOnly.ID, r.kidsID)
	require.Equal(t, http.StatusOK, e.getOccasion(r.kit, kidsOnly.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getOccasion(r.sue, kidsOnly.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getOccasion(r.tom, kidsOnly.ID).StatusCode)
	require.Equal(t, []string{"Reunion"}, occasionNames(decode[occasionList](t, e.listOccasions(r.sue, "kind=shared&user_id="+r.sueID))), "Reunion is the only shared occasion that applies to Sue")
	require.Equal(t, []string{"Kit"}, userNames(decode[userList](t, e.listUsers(r.admin, "occasion_id="+kidsOnly.ID))))
}

func TestASharedOccasionReachesGroupsToAnyDepth(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()
	// Cousins > Kids > Tots > Mites, so Mites is three levels below Cousins.
	tots := e.newGroup(r.admin, "Tots").ID
	mites := e.newGroup(r.admin, "Mites").ID
	e.nest(r.admin, r.kidsID, tots)
	e.nest(r.admin, tots, mites)
	tessID, tess := e.otherUser("Tess")
	e.join(r.admin, mites, tessID)

	require.Equal(t, http.StatusOK, e.getOccasion(tess, r.occasion.ID).StatusCode, "Tess is three levels below Cousins")
	require.Equal(t, []string{"Reunion"}, occasionNames(decode[occasionList](t, e.listOccasions(tess, "kind=shared&user_id="+tessID))))
	require.Equal(t, []string{"Kit", "Mom", "Sue", "Tess"}, userNames(decode[userList](t, e.listUsers(r.admin, "occasion_id="+r.occasion.ID))))
	// An occasion on Tots reaches Tess and not Kit, who is in the group above it.
	inner := e.addShared(r.admin, "Tots' day", annual(6, 1), false)
	e.attach(r.admin, inner.ID, tots)
	require.Equal(t, []string{"Tess"}, userNames(decode[userList](t, e.listUsers(r.admin, "occasion_id="+inner.ID))))
}

func TestASharedOccasionIsSeenByTheUsersWhoSeeAUserItAppliesTo(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()

	// Nan is Mom's parent, and Tom shares "Friends" with Mom: each sees Mom.
	for name, token := range map[string]string{"Nan": r.nan, "Tom": r.tom} {
		require.Equal(t, http.StatusOK, e.getOccasion(token, r.occasion.ID).StatusCode, name)
	}
	// But it does not apply to them.
	require.Empty(t, decode[occasionList](t, e.listOccasions(r.tom, "kind=shared&user_id="+r.tomID)).Data)
	require.Equal(t, []string{"Reunion"}, occasionNames(decode[occasionList](t, e.listOccasions(r.tom, "kind=shared&user_id="+r.momID))))
}

func TestChangingTheGroupsOrTheNestingChangesWhoSeesASharedOccasion(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()

	require.Equal(t, http.StatusNoContent, e.do(request{method: http.MethodDelete, path: "/groups/" + r.cousinsID + "/groups/" + r.kidsID, header: cookie(r.admin)}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getOccasion(r.kit, r.occasion.ID).StatusCode, "Kids is no longer inside Cousins")

	e.join(r.admin, r.cousinsID, r.dadID)
	require.Equal(t, http.StatusOK, e.getOccasion(r.dad, r.occasion.ID).StatusCode, "a user added to the group")

	require.Equal(t, http.StatusNoContent, e.detachGroup(r.admin, r.occasion.ID, r.cousinsID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getOccasion(r.mom, r.occasion.ID).StatusCode, "detached")
}

func TestASharedOccasionMarkedEveryoneAppliesToEveryUserIncludingLaterOnes(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()
	newYear := e.addShared(r.admin, "New Year", annual(1, 1), true)

	for name, token := range map[string]string{"Mom": r.mom, "Dad": r.dad, "Hedy": r.hedy, "Tom": r.tom} {
		require.Equal(t, http.StatusOK, e.getOccasion(token, newYear.ID).StatusCode, name)
	}
	res := e.createUser(r.admin, map[string]any{"name": "Late", "birthday": map[string]any{"month": 3, "day": 5, "year": nil}})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	late := decode[userView](t, res)

	// Late is in no group, and the occasion applies to them.
	users := decode[userList](t, e.listUsers(r.admin, "occasion_id="+newYear.ID))
	require.Contains(t, userNames(users), "Late")
	require.Equal(t, 9, users.TotalCount, "Ada, Mom, Sue, Kit, Tom, Nan, Dad, Hedy and Late")
	require.Equal(t, []string{"New Year"}, occasionNames(decode[occasionList](t, e.listOccasions(r.admin, "kind=shared&user_id="+late.ID+"&next_date_before=2027-01-01"))))
}

func TestSharedOccasionsAreInAUsersOccasions(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()
	e.addShared(r.admin, "New Year", annual(1, 1), true)

	// Mom: her birthday, the one for her groups, and the one for everyone.
	got := decode[occasionList](t, e.listOccasions(r.mom, "user_id="+r.momID))
	require.Equal(t, []string{"Birthday", "New Year", "Reunion"}, occasionNames(got))
	require.Equal(t, []string{"2027-01-01", "2027-01-01", "2027-07-04"}, nextDates(got))
	require.Equal(t, 3, got.TotalCount)
	// Dad is in no group of Reunion.
	require.Equal(t, []string{"Birthday", "New Year"}, occasionNames(decode[occasionList](t, e.listOccasions(r.dad, "user_id="+r.dadID))))
	// Tom can see Mom, so Mom's shared occasions show beside her own.
	require.Equal(t, []string{"Birthday", "New Year", "Reunion"}, occasionNames(decode[occasionList](t, e.listOccasions(r.tom, "user_id="+r.momID))))
	// Hedy can't see Mom, so she gets no shared occasion for her.
	require.Empty(t, decode[occasionList](t, e.listOccasions(r.hedy, "user_id="+r.momID)).Data)
	require.Empty(t, decode[occasionList](t, e.listOccasions(r.hedy, "user_id=99999")).Data, "a user that doesn't exist has none")
	// The kind filter and the shared kind.
	require.Equal(t, []string{"New Year", "Reunion"}, occasionNames(decode[occasionList](t, e.listOccasions(r.mom, "kind=shared&user_id="+r.momID))))
	// The admin sees every user, so every user's shared occasions.
	require.Equal(t, []string{"Birthday", "New Year", "Reunion"}, occasionNames(decode[occasionList](t, e.listOccasions(r.admin, "user_id="+r.momID))))
}

func TestTheUsersAnOccasionAppliesTo(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()

	// The admin sees every user: the users of Cousins and of Kids inside it.
	require.Equal(t, []string{"Kit", "Mom", "Sue"}, userNames(decode[userList](t, e.listUsers(r.admin, "occasion_id="+r.occasion.ID))))
	// Mom sees Sue and herself; Kit is in Kids, inside Cousins, so she shares a group with Kit too.
	require.Equal(t, []string{"Kit", "Mom", "Sue"}, userNames(decode[userList](t, e.listUsers(r.mom, "occasion_id="+r.occasion.ID))))
	// Tom sees Reunion through Mom, and of the users it applies to he sees only Mom.
	tom := decode[userList](t, e.listUsers(r.tom, "occasion_id="+r.occasion.ID))
	require.Equal(t, []string{"Mom"}, userNames(tom))
	require.Equal(t, 1, tom.TotalCount)
	// It combines with the other filters.
	require.Equal(t, []string{"Kit"}, userNames(decode[userList](t, e.listUsers(r.admin, "occasion_id="+r.occasion.ID+"&group_id="+r.kidsID))))
	// An occasion that the caller can't see, one that doesn't exist, and a bad ID give none.
	for _, id := range []string{r.occasion.ID, "99999", "abc"} {
		require.Empty(t, decode[userList](t, e.listUsers(r.hedy, "occasion_id="+id)).Data, id)
	}
	require.Equal(t, http.StatusOK, e.listUsers(r.hedy, "occasion_id=abc").StatusCode)
}

func TestTheUsersABirthdayOrCustomOccasionAppliesToIsItsUser(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()
	custom := e.addOccasion(r.mom, "Wedding day", annual(5, 4), "")
	birthday := decode[occasionList](t, e.listOccasions(r.mom, "kind=birthday&user_id="+r.momID)).Data[0]

	require.Equal(t, []string{"Mom"}, userNames(decode[userList](t, e.listUsers(r.mom, "occasion_id="+custom.ID))))
	require.Equal(t, []string{"Mom"}, userNames(decode[userList](t, e.listUsers(r.sue, "occasion_id="+birthday.ID))))
	// The admin's access doesn't show Mom's custom occasion, and Hedy can't see it.
	require.Empty(t, decode[userList](t, e.listUsers(r.admin, "occasion_id="+custom.ID)).Data)
	require.Empty(t, decode[userList](t, e.listUsers(r.hedy, "occasion_id="+custom.ID)).Data)
}

func TestTheAdminSeesTheDeactivatedUsersASharedOccasionAppliesTo(t *testing.T) {
	e := newEnv(t)
	r := e.reunion()
	e.deactivated(r.admin, r.sueID)

	require.Equal(t, []string{"Kit", "Mom", "Sue"}, userNames(decode[userList](t, e.listUsers(r.admin, "occasion_id="+r.occasion.ID))))
	require.Equal(t, []string{"Kit", "Mom"}, userNames(decode[userList](t, e.listUsers(r.mom, "occasion_id="+r.occasion.ID))), "a deactivated user is seen only by the admin, their parents and themselves")
}
