package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover what deleting a user does to gifts (docs/spec.md,
// Deactivation and deletion, Deletion; Gifts, Reveal and Changes while a gift is
// open). The clock starts on 2026-11-08, and every birthday is on Jan 1.

func TestDeletingAGiverReleasesTheirOpenGiftsAndKeepsTheOnesTheyGaveWithTheirName(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	kettle := e.addItem(f.mom, f.house, "Kettle", map[string]any{"quantity": 2})
	tess := e.giver("Tess", f)
	given := e.given(f.sue, grill.ID, nil)
	theirs := e.claim(tess, grill.ID, nil)
	open := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2})
	requireCoded(t, e.getItem(tess, kettle.ID), http.StatusNotFound, "not_found") // fully claimed

	e.deleteUser(f.sueID)

	// The open gift is deleted, and its quantity is back for every giver.
	require.Zero(t, e.rowCount("gifts", `id = ?`, open.ID))
	require.Equal(t, 2, *decode[itemView](t, e.getItem(tess, kettle.ID)).StillWanted)
	require.Equal(t, http.StatusOK, e.getGift(tess, theirs.ID).StatusCode, "another giver's gift stays")
	// What she gave stays in Received, with her name as it was and no giver.
	for name, token := range map[string]string{"Mom": f.mom, "Dad": f.dad} {
		received := decode[giftList](t, e.listGifts(token, "recipient_id=me"))
		require.Equal(t, []string{given.ID}, e.giftIDs(received), name)
		g := received.Data[0]
		require.Nil(t, g.Giver, name)
		require.Equal(t, "Sue", g.GiverName, name)
		require.Equal(t, "given", g.State, name)
	}
	require.Contains(t, readBody(t, e.getGift(f.mom, given.ID)), `"giver":null`)
}

func TestAGiftTheDeletedGiverGaveStaysInTheReceivedOfTheRecipientsParentsToo(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID, f.dadID)
	e.join(f.admin, e.newGroup(f.admin, "Kids").ID, f.sueID, emmaID)
	book := e.addItem(emma, e.primaryListFor(emma, emmaID), "Book")
	given := e.given(f.sue, book.ID, map[string]any{"from_label": "Grandpa"})
	// Sue changes her name before she goes, so the name kept is the one she had then.
	require.Equal(t, http.StatusOK, e.patchUser(f.sue, f.sueID, map[string]any{"name": "Susan"}, nil).StatusCode)

	e.deleteUser(f.sueID)

	for name, token := range map[string]string{"Emma": emma, "her mother": f.mom, "her father": f.dad} {
		received := decode[giftList](t, e.listGifts(token, "recipient_id="+emmaID))
		require.Equal(t, []string{given.ID}, e.giftIDs(received), name)
		g := received.Data[0]
		require.Nil(t, g.Giver, name)
		require.Equal(t, "Susan", g.GiverName, name+": the name as it was at the delete")
		require.Equal(t, "Grandpa", *g.FromLabel, name)
		require.Equal(t, []string{emmaID}, summaryIDs(anySlice(g.Recipients)), name)
	}
}

func TestAGiftGivenToTheUserAndOthersStaysForTheOthersAndOneGivenToThemAloneIsDeleted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "image_ids": imageIDs(photo)})
	socks := e.addItem(f.dad, f.dadList, "Socks", map[string]any{"image_ids": imageIDs(e.uploaded(f.dad))})
	e.join(f.admin, e.cousins(f), f.dadID)
	shared := e.claim(f.sue, grill.ID, nil)
	alone := e.claim(f.sue, socks.ID, nil)
	require.Equal(t, 2, e.revealAt("2027-01-02T00:00:00Z"))
	aloneCopy := e.gift(f.dad, alone.ID).Image.ID
	require.ElementsMatch(t, []string{shared.ID, alone.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.sue, "giver_id=me&state=given"))))

	e.deleteUser(f.dadID)

	// The one for Mom and Dad stays for Mom, without Dad. Its giver still has it.
	got := e.gift(f.mom, shared.ID)
	require.Equal(t, []string{f.momID}, summaryIDs(anySlice(got.Recipients)))
	require.Equal(t, []string{shared.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.mom, "recipient_id=me"))))
	require.Equal(t, []string{shared.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.sue, "giver_id=me&state=given"))), "the one for Dad alone leaves Sue's Given")
	// The one for Dad alone is deleted, with its copy of the image.
	requireCoded(t, e.getGift(f.sue, alone.ID), http.StatusNotFound, "not_found")
	require.Zero(t, e.rowCount("gifts", `id = ?`, alone.ID))
	require.True(t, e.row(aloneCopy).Detached, "the sweep deletes the copy")
	// What the shared gift keeps still loads for the people who see it.
	require.Equal(t, http.StatusOK, e.getImage(f.mom, got.Image.ID).StatusCode)
	// Quantities don't change: the given one left the item at reveal.
	require.Equal(t, 1, e.itemQuantity(grill.ID))
}

func TestAGiftGivenOnAListTheDeleteRemovesStaysForTheRecipientsWhoLeftBeforeIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	given := e.given(f.sue, grill.ID, nil)
	// Mom leaves "Our house" after the reveal. The gift stays in her Received.
	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, nil).StatusCode)
	require.Equal(t, []string{given.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.mom, "recipient_id=me"))))

	e.deleteUser(f.dadID)

	// Dad was the only recipient left, so the list is deleted. The gift is kept for Mom.
	requireCoded(t, e.getList(f.mom, f.house), http.StatusNotFound, "not_found")
	require.Equal(t, http.StatusUnauthorized, e.getList(f.dad, f.house).StatusCode, "Dad's session is gone")
	got := e.gift(f.mom, given.ID)
	require.Equal(t, []string{f.momID}, summaryIDs(anySlice(got.Recipients)))
	require.Equal(t, "Our house", got.List.Name)
}

func TestDeletingAUserMovesAnOpenGiftForTheirOccasionOnAListThatStays(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	grill := e.addItem(f.mom, f.house, "Grill")
	dads := e.addOccasion(f.dad, "Dad's day", annual(3, 4), f.dadID)
	g := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": dads.ID})

	e.deleteUser(f.dadID)

	moved := e.gift(f.sue, g.ID)
	require.Equal(t, f.birthdayOfMom(e), moved.Occasion.ID)
	require.Contains(t, flagCodes(moved), "occasion_moved")
	require.Zero(t, e.rowCount("occasions", `id = ?`, dads.ID))
}
