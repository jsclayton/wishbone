package server

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cover the rules of a change in a list's recipients, and of an
// occasion that stops applying (docs/spec.md, Gifts, Changes while a gift is
// open).

// cousin adds a user to the group "Cousins", so the other cousins see them.
func (e *env) cousin(name string, f family) (id, token string) {
	e.t.Helper()
	id, token = e.otherUser(name)
	e.join(f.admin, e.cousins(f), id)
	return id, token
}

func recipientsFlag(t *testing.T, g giftView) (oldIDs, newIDs []string) {
	t.Helper()
	flag := flagOf(t, g, "recipients_changed")
	return summaryIDs(flag["old_recipients"]), summaryIDs(flag["new_recipients"])
}

func TestAJoiningOrLeavingRecipientFlagsEachOpenGiftWithTheOldAndNewRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	sues := e.claim(f.sue, grill.ID, nil)
	tess := e.giver("Tess", f)
	tesses := e.claim(tess, grill.ID, nil)
	zedID, zed := e.cousin("Zed", f)

	e.joinList(f.mom, f.house, zedID, zed)

	for name, g := range map[string]giftView{"Sue": e.gift(f.sue, sues.ID), "Tess": e.gift(tess, tesses.ID)} {
		require.Equal(t, []string{"recipients_changed"}, flagCodes(g), name)
		oldIDs, newIDs := recipientsFlag(t, g)
		require.Equal(t, []string{f.momID, f.dadID}, oldIDs, name)
		require.Equal(t, []string{f.momID, f.dadID, zedID}, newIDs, name)
		require.Equal(t, newIDs, summaryIDs(anySlice(g.Recipients)), name+": the gift is now for them")
		require.True(t, g.CanAcknowledge, name)
		require.False(t, g.NeedsAttention, name+": a change of recipients does not need attention")
	}

	// A second change keeps the first old recipients and shows the latest new ones.
	require.Equal(t, http.StatusNoContent, e.leave(f.dad, f.house, f.dadID, nil).StatusCode)
	oldIDs, newIDs := recipientsFlag(t, e.gift(tess, tesses.ID))
	require.Equal(t, []string{f.momID, f.dadID}, oldIDs)
	require.Equal(t, []string{f.momID, zedID}, newIDs)

	// Answering it clears it, and the next change starts from what is now.
	require.Empty(t, decode[giftView](t, e.acknowledge(tess, tesses.ID, map[string]any{"codes": []string{"recipients_changed"}})).Flags)
	require.Equal(t, http.StatusNoContent, e.leave(zed, f.house, zedID, nil).StatusCode)
	oldIDs, newIDs = recipientsFlag(t, e.gift(tess, tesses.ID))
	require.Equal(t, []string{f.momID, zedID}, oldIDs)
	require.Equal(t, []string{f.momID}, newIDs)
}

func TestARecipientWhoJoinsAndLeavesAgainLeavesNoFlag(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	zedID, zed := e.cousin("Zed", f)

	e.joinList(f.mom, f.house, zedID, zed)
	require.Equal(t, []string{"recipients_changed"}, flagCodes(e.gift(f.sue, g.ID)))
	require.Equal(t, http.StatusNoContent, e.leave(zed, f.house, zedID, nil).StatusCode)

	require.Empty(t, e.gift(f.sue, g.ID).Flags, "the recipients are the ones the giver saw")
}

func TestTheRecipientsChangedFlagHasNullRecipientsForAGiverWhoCannotSeeTheList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	require.Equal(t, http.StatusNotFound, e.getList(f.sue, f.house).StatusCode)
	zedID, zed := e.cousin("Zed", f)

	e.joinList(f.mom, f.house, zedID, zed)

	body := readBody(t, e.getGift(f.sue, g.ID))
	require.Contains(t, body, `"old_recipients":null`)
	require.Contains(t, body, `"new_recipients":null`)
}

func TestAGiverWhoBecomesTheOnlyRecipientHasTheirGiftsReleased(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	sues := e.claim(f.sue, grill.ID, map[string]any{"quantity": 2})
	e.joinList(f.mom, f.house, f.sueID, f.sue)
	require.Equal(t, http.StatusNoContent, e.leave(f.dad, f.house, f.dadID, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.getGift(f.sue, sues.ID).StatusCode, "Mom is still a recipient")

	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, nil).StatusCode)

	requireCoded(t, e.getGift(f.sue, sues.ID), http.StatusNotFound, "not_found")
	require.Empty(t, decode[giftList](t, e.listGifts(f.sue, "")).Data)
	var gifts int
	require.NoError(t, e.db.GetContext(t.Context(), &gifts, `SELECT count(*) FROM gifts`))
	require.Zero(t, gifts, "the quantity went back to all givers")
}

func TestMovingAnItemToAListWhereTheGiverIsTheOnlyRecipientReleasesTheirGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.linkParents(f.admin, f.sueID, f.momID)
	suesList := e.primaryListFor(f.sue, f.sueID)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	g := e.claim(f.sue, grill.ID, nil)
	tess := e.giver("Tess", f)
	theirs := e.claim(tess, grill.ID, nil)

	// Mom edits both lists, as Sue's parent.
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"list_id": suesList}, nil).StatusCode)

	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	require.Equal(t, http.StatusOK, e.getGift(tess, theirs.ID).StatusCode, "another giver's gift stays")
}

func TestAGiftMovesToTheOtherRecipientsNextOccasionWhenItsRecipientLeaves(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID) // the cousins now see Dad too
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 5})
	tess := e.giver("Tess", f)
	dads := e.birthdayOf(tess, f.dadID)
	moms := e.birthdayOf(tess, f.momID)
	forDad := e.claim(tess, grill.ID, map[string]any{"occasion_id": dads, "quantity": 2})
	forMom := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": moms})

	require.Equal(t, http.StatusNoContent, e.leave(f.dad, f.house, f.dadID, nil).StatusCode)

	moved := e.gift(tess, forDad.ID)
	require.Equal(t, moms, moved.Occasion.ID, "Mom's next occasion")
	require.Equal(t, "2027-01-01", moved.OccasionDate)
	require.ElementsMatch(t, []string{"occasion_moved", "recipients_changed"}, flagCodes(moved))
	require.Equal(t, []string{"recipients_changed"}, flagCodes(e.gift(f.sue, forMom.ID)), "her occasion still applies")
	require.Equal(t, moms, e.gift(f.sue, forMom.ID).Occasion.ID)

	// The giver can still pick: she sees Mom.
	require.Equal(t, http.StatusOK, e.patchGift(tess, forDad.ID, map[string]any{"occasion_id": moms}, nil).StatusCode)
}

func TestTheGiftThatMovesMergesIntoTheGiversGiftThatIsAlreadyThere(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 5})
	tess := e.giver("Tess", f)
	staying := e.claim(tess, grill.ID, map[string]any{"occasion_id": e.birthdayOf(tess, f.momID), "quantity": 1})
	moving := e.claim(tess, grill.ID, map[string]any{"occasion_id": e.birthdayOf(tess, f.dadID), "quantity": 2, "note": "Black"})

	require.Equal(t, http.StatusNoContent, e.leave(f.dad, f.house, f.dadID, nil).StatusCode)

	got := e.gift(tess, staying.ID)
	require.Equal(t, 3, got.Quantity)
	require.Equal(t, "Black", *got.Note)
	require.Contains(t, flagCodes(got), "occasion_moved", "the moving gift's flags are added")
	requireCoded(t, e.getGift(tess, moving.ID), http.StatusNotFound, "not_found")
}

func TestDeletingACustomOccasionMovesItsGiftsToTheRecipientsNextOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})
	require.Equal(t, "2026-12-06", g.OccasionDate)

	require.Equal(t, http.StatusNoContent, e.deleteOccasion(f.mom, advent.ID).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.NotNil(t, got.Occasion, "an open gift never has no occasion")
	require.Equal(t, e.birthdayOf(f.sue, f.momID), got.Occasion.ID)
	require.Equal(t, "2027-01-01", got.OccasionDate)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(got))
}

func TestDetachingOrUnmarkingASharedOccasionMovesTheGiftsThatItNoLongerReaches(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	reunion := e.addShared(f.admin, "Reunion", annual(12, 10), false)
	e.attach(f.admin, reunion.ID, e.cousins(f))
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": reunion.ID})
	require.Equal(t, http.StatusNoContent, e.detachGroup(f.admin, reunion.ID, e.cousins(f)).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, e.birthdayOf(f.sue, f.momID), got.Occasion.ID)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(got))

	// An occasion marked everyone, then unmarked, reaches nobody.
	everyone := e.addShared(f.admin, "Holiday", annual(12, 25), true)
	teapot := e.addItem(f.mom, f.momList, "Teapot")
	h := e.claim(f.sue, teapot.ID, map[string]any{"occasion_id": everyone.ID})
	require.Equal(t, http.StatusOK, e.patchOccasion(f.admin, everyone.ID, map[string]any{"is_everyone": false}, nil).StatusCode)
	moved := e.gift(f.sue, h.ID)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(moved))
	require.NotNil(t, moved.Occasion)
	require.NotEqual(t, everyone.ID, moved.Occasion.ID)
}

func TestAGiftKeepsItsOccasionWhenAnotherRouteStillReachesIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	reunion := e.addShared(f.admin, "Reunion", annual(12, 10), false)
	e.attach(f.admin, reunion.ID, e.cousins(f))
	other := e.newGroup(f.admin, "Neighbors")
	e.join(f.admin, other.ID, f.momID)
	e.attach(f.admin, reunion.ID, other.ID)
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": reunion.ID})

	require.Equal(t, http.StatusNoContent, e.detachGroup(f.admin, reunion.ID, e.cousins(f)).StatusCode)

	require.Empty(t, e.gift(f.sue, g.ID).Flags, "Mom is still in the other group")
	require.Equal(t, reunion.ID, e.gift(f.sue, g.ID).Occasion.ID)
}

func TestChangingABirthdayMovesTheGiftsForItToTheNewDate(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	require.Equal(t, "2027-01-01", g.OccasionDate)

	res := e.patchUser(f.mom, f.momID, map[string]any{"birthday": birthday(3, 4, nil)}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, g.Occasion.ID, got.Occasion.ID, "it is still her birthday")
	require.Equal(t, "2027-03-04", got.OccasionDate)
	require.Equal(t, "2027-03-05T00:00:00Z", got.RevealAt)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(got))
}

func TestARenamedOccasionAndAnEditedTimezoneMoveNoGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})

	require.Equal(t, http.StatusOK, e.patchOccasion(f.mom, advent.ID, map[string]any{"name": "First Advent"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.patchUser(f.mom, f.momID, map[string]any{"timezone": "Europe/Paris"}, nil).StatusCode)

	require.Empty(t, e.gift(f.sue, g.ID).Flags)
	require.Equal(t, "2026-12-06", e.gift(f.sue, g.ID).OccasionDate)
}

func TestMovingTheDateOfACustomOccasionMovesItsGifts(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})

	require.Equal(t, http.StatusOK, e.patchOccasion(f.mom, advent.ID, map[string]any{"rule": annual(12, 13)}, nil).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, advent.ID, got.Occasion.ID)
	require.Equal(t, "2026-12-13", got.OccasionDate)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(got))
}

func TestAGiverWhoSeesNoRecipientAnyMoreIsFlaggedAndTheServerPicksTheOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	dads := e.birthdayOf(f.dad, f.dadID)

	// Mom is the only recipient Sue sees. When she leaves, Sue sees none.
	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, nil).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.ElementsMatch(t, []string{"no_longer_shared", "occasion_moved", "recipients_changed"}, flagCodes(got))
	require.Nil(t, got.Occasion, "Dad's birthday is hidden from her")
	require.Equal(t, "2027-01-01", got.OccasionDate)
	var picked int64
	require.NoError(t, e.db.GetContext(t.Context(), &picked, `SELECT occasion_id FROM gifts WHERE id = ?`, g.ID))
	require.Equal(t, dads, strconv.FormatInt(picked, 10), "the server picked Dad's next occasion")
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"occasion_id": dads}, nil), http.StatusUnprocessableEntity, "occasion_locked")

	// She answers it, and a later change doesn't raise it again.
	e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"no_longer_shared"}})
	zedID, zed := e.otherUser("Zed")
	e.join(f.admin, e.newGroup(f.admin, "Neighbors").ID, f.dadID, zedID)
	e.joinList(f.dad, f.house, zedID, zed)
	require.NotContains(t, flagCodes(e.gift(f.sue, g.ID)), "no_longer_shared")
}

func TestAGiverWhoIsARecipientSeesThemselvesSoTheirGiftIsNotFlaggedNoLongerShared(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	e.joinList(f.mom, f.house, f.sueID, f.sue)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)

	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, nil).StatusCode)

	// Dad is the other recipient, and Sue can't see him, but she sees herself.
	require.NotContains(t, flagCodes(e.gift(f.sue, g.ID)), "no_longer_shared")
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"occasion_id": e.birthdayOf(f.dad, f.dadID)}, nil), http.StatusUnprocessableEntity, "occasion_locked")
}

func TestMovingAnItemToAListTheGiverCannotSeeFlagsItMovedAndNoLongerShared(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)

	require.Equal(t, http.StatusOK, e.patchItem(f.dad, grill.ID, map[string]any{"list_id": f.dadList}, nil).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.ElementsMatch(t, []string{"moved", "no_longer_shared", "occasion_moved"}, flagCodes(got))
	require.Nil(t, got.Occasion)
}

func TestAGiftPastItsDateWaitsForItsRevealAndIsNotMovedWhenItsOccasionGoes(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})
	e.clock.advance(30 * 24 * time.Hour) // Dec 8: the date has passed

	require.Equal(t, http.StatusNoContent, e.deleteOccasion(f.mom, advent.ID).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, "2026-12-06", got.OccasionDate, "it is given on the day after, as it was planned")
	require.Empty(t, got.Flags)
}
