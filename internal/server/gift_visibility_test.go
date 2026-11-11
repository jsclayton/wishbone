package server

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover what a change in who sees whom does to the open gifts: a
// user in or out of a group, groups nested or unnested, a group deleted, and a
// parent link changed (docs/spec.md, Gifts, Changes while a gift is open). The
// flags are the giver's only.

func (e *env) removeGroup(admin, outerID, innerID string) {
	e.t.Helper()
	res := e.do(request{method: http.MethodDelete, path: "/groups/" + outerID + "/groups/" + innerID, header: cookie(admin)})
	require.Equal(e.t, http.StatusNoContent, res.StatusCode, "remove group %s from group %s", innerID, outerID)
}

func TestAGiverRemovedFromTheOnlyGroupTheySharedWithTheRecipientIsFlaggedAtOnce(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	require.NotNil(t, g.Occasion)

	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, []string{"no_longer_shared"}, flagCodes(got), "Mom's birthday still applies to Mom, so the gift stays")
	require.Nil(t, got.Occasion, "and it is hidden from Sue")
	require.Equal(t, "2027-01-01", got.OccasionDate)
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"occasion_id": g.Occasion.ID}, nil), http.StatusUnprocessableEntity, "occasion_locked")
	// The recipient learns nothing: the gift is not hers, and her list shows no claim.
	requireCoded(t, e.getGift(f.mom, g.ID), http.StatusNotFound, "not_found")
	require.NotContains(t, readBody(t, e.getItem(f.mom, kettle.ID)), "still_wanted")
}

func TestAGiverWhoSeesTheRecipientAgainAndLosesThemAgainIsFlaggedAgain(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	require.Equal(t, []string{"no_longer_shared"}, flagCodes(e.gift(f.sue, g.ID)))
	e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"no_longer_shared"}})

	// Another change that leaves her as she is does not raise it again.
	e.join(f.admin, e.newGroup(f.admin, "Neighbors").ID, f.hedyID)
	require.Empty(t, e.gift(f.sue, g.ID).Flags)

	e.join(f.admin, e.cousins(f), f.sueID)
	require.Empty(t, e.gift(f.sue, g.ID).Flags, "she sees Mom again")
	require.NotNil(t, e.gift(f.sue, g.ID).Occasion)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	require.Equal(t, []string{"no_longer_shared"}, flagCodes(e.gift(f.sue, g.ID)))
}

func TestAParentLinkRemovedThatWasTheGiversOnlyWayToSeeTheListFlagsTheGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	// Hedy shares no group with anyone, and sees Mom only as her parent.
	e.linkParents(f.admin, f.momID, f.hedyID)
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.hedy, kettle.ID, nil)
	require.True(t, g.CanEdit)

	e.linkParents(f.admin, f.momID)

	got := e.gift(f.hedy, g.ID)
	require.Equal(t, []string{"no_longer_shared"}, flagCodes(got))
	require.Nil(t, got.Occasion)
	requireCoded(t, e.getList(f.hedy, f.momList), http.StatusNotFound, "not_found")
	requireCoded(t, e.getGift(f.mom, g.ID), http.StatusNotFound, "not_found")
}

func TestAParentLinkAddedLeavesTheGiftsOfOtherUsersAlone(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)

	e.linkParents(f.admin, f.dadID, f.hedyID)

	require.Empty(t, e.gift(f.sue, g.ID).Flags)
}

func TestUnnestingTheGroupThatCarriedTheGiverFlagsTheGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	// Sue and Mom meet in "Everyone" only, through two groups nested in it.
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	e.removeFromGroup(f.admin, e.cousins(f), f.momID)
	everyone := e.newGroup(f.admin, "Everyone")
	moms := e.newGroup(f.admin, "Moms")
	sues := e.newGroup(f.admin, "Sues")
	e.join(f.admin, moms.ID, f.momID)
	e.join(f.admin, sues.ID, f.sueID)
	e.nest(f.admin, everyone.ID, moms.ID)
	e.nest(f.admin, everyone.ID, sues.ID)
	g := e.claim(f.sue, kettle.ID, nil)

	e.removeGroup(f.admin, everyone.ID, moms.ID)

	require.Equal(t, []string{"no_longer_shared"}, flagCodes(e.gift(f.sue, g.ID)))
}

func TestNestingGroupsDoesNotFlagAGiftTheGiverCanStillSee(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	outer := e.newGroup(f.admin, "Outer")

	e.nest(f.admin, outer.ID, e.cousins(f))

	require.Empty(t, e.gift(f.sue, g.ID).Flags)
}

func TestDeletingTheGroupThatCarriedTheGiverFlagsTheGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)

	res := e.do(request{method: http.MethodDelete, path: "/groups/" + e.cousins(f), header: cookie(f.admin)})
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Equal(t, []string{"no_longer_shared"}, flagCodes(e.gift(f.sue, g.ID)))
}

func TestAGiftKeptOnADeletedListMovesToTheLastRecipientsNextOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	garden := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Garden"})).ID
	rake := e.addItem(f.mom, garden, "Rake")
	g := e.claim(f.sue, rake.ID, map[string]any{"occasion_id": advent.ID})
	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, garden, nil).StatusCode)
	require.Equal(t, []string{"removed"}, flagCodes(e.gift(f.sue, g.ID)))

	require.Equal(t, http.StatusNoContent, e.deleteOccasion(f.mom, advent.ID).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.ElementsMatch(t, []string{"removed", "occasion_moved"}, flagCodes(got), "it is not flagged no longer shared: the list's delete flagged it")
	require.Equal(t, "2027-01-01", got.OccasionDate)
	var picked int64
	require.NoError(t, e.db.GetContext(t.Context(), &picked, `SELECT occasion_id FROM gifts WHERE id = ?`, g.ID))
	require.Equal(t, e.birthdayOf(f.mom, f.momID), strconv.FormatInt(picked, 10), "the server picked Mom's next occasion")
	require.Nil(t, got.Occasion, "and Sue can't see the list, so she sees only the reveal date")

	// A change of the birthday moves it too, and the gift is not released.
	require.Equal(t, http.StatusOK, e.patchUser(f.mom, f.momID, map[string]any{"birthday": birthday(3, 4, nil)}, nil).StatusCode)
	require.Equal(t, "2027-03-04", e.gift(f.sue, g.ID).OccasionDate)
}

func TestMovingTheAdminRoleSettlesTheGiftsOfTheOldAndTheNewAdmin(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	// Ada sees Mom through "Cousins", and Dad only as the admin.
	e.join(f.admin, e.cousins(f), f.adminID)
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.admin, grill.ID, map[string]any{"occasion_id": e.birthdayOf(f.admin, f.dadID)})
	require.NotNil(t, g.Occasion)

	_, _, err := e.srv.opts.Users.SetAdmin(t.Context(), mustID(t, f.sueID))
	require.NoError(t, err)

	// Ada is a normal user now and no longer sees Dad, so his birthday is gone from her gift.
	got := e.gift(f.admin, g.ID)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(got))
	require.Equal(t, e.birthdayOf(f.admin, f.momID), got.Occasion.ID)
}

func TestARecipientLeavingTheGroupThatReachesASharedOccasionMovesTheGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	reunion := e.addShared(f.admin, "Reunion", annual(12, 10), false)
	e.attach(f.admin, reunion.ID, e.cousins(f))
	friends := e.newGroup(f.admin, "Friends")
	e.join(f.admin, friends.ID, f.momID, f.sueID) // Sue still sees Mom
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": reunion.ID})

	e.removeFromGroup(f.admin, e.cousins(f), f.momID)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(got), "Sue still sees Mom, so it is not unshared")
	require.Equal(t, e.birthdayOf(f.sue, f.momID), got.Occasion.ID)
}
