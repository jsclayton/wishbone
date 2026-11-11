package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cover restore, "I didn't get this" (docs/spec.md, Gifts,
// Restore; docs/routes.md, Gift routes): a given gift opens again for its
// giver, and is hidden again from everyone else. The clock starts on 2026-11-08,
// and every birthday is on Jan 1.

func (e *env) restore(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/gifts/" + id + "/restore", header: cookie(token)})
}

// given claims an item and gives the gift at the first reveal, on Jan 2, 2027.
func (e *env) given(giver, itemID string, body map[string]any) giftView {
	e.t.Helper()
	g := e.claim(giver, itemID, body)
	require.Equal(e.t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	return g
}

// itemQuantity reads the quantity column of an item, whoever sees it.
func (e *env) itemQuantity(itemID string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT quantity FROM items WHERE id = ?`, itemID))
	return n
}

func TestARecipientRestoresAGivenGiftAndItOpensAgainForItsGiver(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "price_minor": 45000, "image_ids": imageIDs(photo)})
	g := e.given(f.sue, grill.ID, map[string]any{"quantity": 2, "note": "Costco", "is_bought": true})
	requireCoded(t, e.getItem(f.mom, grill.ID), http.StatusNotFound, "not_found")
	given := e.gift(f.mom, g.ID)
	require.True(t, given.CanRestore)
	require.True(t, given.CanAddAgain)

	res := e.restore(f.mom, g.ID)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, readBody(t, res))
	// It is hidden again from the recipients, and from their Received.
	for name, token := range map[string]string{"Mom": f.mom, "Dad": f.dad, "Hedy": f.hedy} {
		requireCoded(t, e.getGift(token, g.ID), http.StatusNotFound, "not_found")
		require.Empty(t, decode[giftList](t, e.listGifts(token, "recipient_id=me")).Data, name)
	}
	requireCoded(t, e.restore(f.mom, g.ID), http.StatusNotFound, "not_found")
	// The item is on the list again, as an item that is simply on the list.
	back := decode[itemView](t, e.getItem(f.mom, grill.ID))
	require.Equal(t, 2, back.Quantity)
	require.Equal(t, 0, back.Position)
	require.NotContains(t, readBody(t, e.getItem(f.mom, grill.ID)), "still_wanted")
	// The giver has the gift open again: the quantity is claimed, for the next
	// occasion, flagged restored by Mom, who Sue can see.
	got := e.gift(f.sue, g.ID)
	require.Equal(t, "open", got.State)
	require.Nil(t, got.GivenAt)
	require.Equal(t, 2, got.Quantity)
	require.Equal(t, "Costco", *got.Note)
	require.True(t, got.IsBought)
	require.Equal(t, "2028-01-01", got.OccasionDate, "Mom's next birthday")
	require.Equal(t, f.birthdayOfMom(e), got.Occasion.ID)
	require.Equal(t, []string{"restored"}, flagCodes(got))
	by := flagOf(t, got, "restored")["by"].(map[string]any)
	require.Equal(t, f.momID, by["id"])
	require.True(t, got.CanEdit)
	require.False(t, got.CanRestore)
	require.False(t, got.CanAddAgain)
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(got.Recipients)), "the list's recipients now: the gift is for them at its next reveal")
	sues := decode[itemView](t, e.getItem(f.sue, grill.ID))
	require.Equal(t, 0, *sues.StillWanted, "other givers can't claim the quantity")
	require.Equal(t, 2, *sues.MyClaimedQuantity)
	requireCoded(t, e.getItem(e.giver("Tess", f), grill.ID), http.StatusNotFound, "not_found")
}

func TestARestoreByAUserTheGiverCannotSeeFlagsItWithNoUser(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.given(f.sue, grill.ID, nil)

	require.Equal(t, http.StatusNoContent, e.restore(f.dad, g.ID).StatusCode)

	flag := flagOf(t, e.gift(f.sue, g.ID), "restored")
	require.Contains(t, flag, "by", "the key is there")
	require.Nil(t, flag["by"], "Sue can't see Dad")
}

func TestAParentRestoresAGiftGivenToTheirChild(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID, f.dadID)
	e.join(f.admin, e.newGroup(f.admin, "Kids").ID, f.sueID, emmaID)
	book := e.addItem(emma, e.primaryListFor(emma, emmaID), "Book")
	g := e.given(f.sue, book.ID, nil)
	require.True(t, e.gift(f.dad, g.ID).CanRestore, "Emma's father")

	require.Equal(t, http.StatusNoContent, e.restore(f.dad, g.ID).StatusCode)

	for name, token := range map[string]string{"Emma": emma, "her mother": f.mom, "her father": f.dad} {
		requireCoded(t, e.getGift(token, g.ID), http.StatusNotFound, "not_found")
		require.Empty(t, decode[giftList](t, e.listGifts(token, "recipient_id="+emmaID)).Data, name)
	}
	require.Equal(t, "open", e.gift(f.sue, g.ID).State)
	require.Equal(t, 1, decode[itemView](t, e.getItem(emma, book.ID)).Quantity)
}

func TestOnlyTheRecipientsAndTheirParentsRestoreAGivenGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	open := e.claim(f.sue, grill.ID, nil)
	unknown := e.restore(f.mom, "999999")
	require.Equal(t, http.StatusNotFound, unknown.StatusCode)
	want := readBody(t, unknown)

	// Until reveal the gift is a 404 for everyone but its giver, which is what a
	// gift that doesn't exist is.
	for name, token := range map[string]string{"Mom": f.mom, "Dad": f.dad, "Hedy": f.hedy, "the admin": f.admin} {
		res := e.restore(token, open.ID)
		require.Equal(t, http.StatusNotFound, res.StatusCode, name)
		require.Equal(t, want, readBody(t, res), name+": the same answer as a gift that doesn't exist")
	}
	requireCoded(t, e.restore(f.mom, "nope"), http.StatusNotFound, "not_found")
	// Its giver sees it and doesn't receive it.
	requireCoded(t, e.restore(f.sue, open.ID), http.StatusForbidden, "forbidden")
	require.Equal(t, "open", e.gift(f.sue, open.ID).State)

	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	requireCoded(t, e.restore(f.sue, open.ID), http.StatusForbidden, "forbidden")
	for name, token := range map[string]string{"Hedy": f.hedy, "the admin": f.admin, "another giver": e.giver("Tess", f)} {
		res := e.restore(token, open.ID)
		require.Equal(t, http.StatusNotFound, res.StatusCode, name)
		require.Equal(t, want, readBody(t, res), name)
	}
	require.False(t, e.gift(f.sue, open.ID).CanRestore, "the giver can't restore it")
	require.Equal(t, "given", e.gift(f.sue, open.ID).State)
	// Dad is a recipient too, and the first restore wins.
	require.Equal(t, http.StatusNoContent, e.restore(f.dad, open.ID).StatusCode)
	requireCoded(t, e.restore(f.mom, open.ID), http.StatusNotFound, "not_found")
}

func TestARestoreLeavesAnItemLookingToItsRecipientsAsIfItWasNeverClaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addItem(f.mom, f.house, "Kettle")
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	before := e.getItem(f.mom, grill.ID)
	bodyBefore, etagBefore := readBody(t, before), before.Header.Get("ETag")
	dadBefore := readBody(t, e.getItem(f.dad, grill.ID))
	listBefore, itemsBefore := readBody(t, e.getList(f.mom, f.house)), readBody(t, e.listItems(f.mom, f.house, ""))
	g := e.given(f.sue, grill.ID, map[string]any{"quantity": 2})
	require.NotEqual(t, listBefore, readBody(t, e.getList(f.mom, f.house)), "the grill left the list when it was given")

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	after := e.getItem(f.mom, grill.ID)
	require.Equal(t, bodyBefore, readBody(t, after))
	require.Equal(t, etagBefore, after.Header.Get("ETag"), "the version shows no claim")
	require.Equal(t, dadBefore, readBody(t, e.getItem(f.dad, grill.ID)))
	require.Equal(t, listBefore, readBody(t, e.getList(f.mom, f.house)), "item_count shows no claim")
	require.Equal(t, itemsBefore, readBody(t, e.listItems(f.mom, f.house, "")))
	// The gift is the same 404 as one that was never given, and so is its image.
	missing := e.getGift(f.mom, "999999")
	require.Equal(t, http.StatusNotFound, missing.StatusCode)
	gone := e.getGift(f.mom, g.ID)
	require.Equal(t, http.StatusNotFound, gone.StatusCode)
	require.Equal(t, readBody(t, missing), readBody(t, gone))
}

func TestARestoreHidesTheCopyOfTheImageAndTheAvatarsTheGiftExposed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	sueAvatar, momAvatar := e.uploaded(f.sue), e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.setAvatar(f.sue, f.sueID, sueAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.setAvatar(f.mom, f.momID, momAvatar.ID).StatusCode)
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"image_ids": imageIDs(photo)})
	g := e.given(f.sue, grill.ID, nil)
	copyID := e.gift(f.dad, g.ID).Image.ID
	require.Equal(t, http.StatusOK, e.getImage(f.dad, copyID).StatusCode, "the copy loads for a recipient")
	require.Equal(t, http.StatusOK, e.getImage(f.dad, sueAvatar.ID).StatusCode, "Dad sees Sue only through the gift")

	require.Equal(t, http.StatusNoContent, e.restore(f.dad, g.ID).StatusCode)

	for name, token := range map[string]string{"Mom": f.mom, "Dad": f.dad, "Hedy": f.hedy} {
		requireCoded(t, e.getImage(token, copyID), http.StatusNotFound, "not_found")
		_ = name
	}
	requireCoded(t, e.getImage(f.dad, sueAvatar.ID), http.StatusNotFound, "not_found")
	// The giver keeps the copy, and the recipients keep the item's own image.
	require.Equal(t, http.StatusOK, e.getImage(f.sue, copyID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.dad, photo.ID).StatusCode)
}

func TestARestoreOntoAnItemThatLeftTheListPutsItBackInTheLastPlaceAndClearsRemovedFromOtherGifts(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	e.addItem(f.mom, f.momList, "Mug")
	tess := e.giver("Tess", f)
	sues := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2, "occasion_id": advent.ID})
	tesses := e.claim(tess, kettle.ID, map[string]any{"quantity": 1, "occasion_id": e.birthdayOf(tess, f.momID)})
	// Mom lowers the quantity below what is claimed.
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, kettle.ID, map[string]any{"quantity": 2}, nil).StatusCode)
	e.acknowledge(tess, tesses.ID, nil)

	require.Equal(t, 1, e.revealAt("2026-12-07T00:00:00Z"))

	// The kettle's whole quantity was given, so it left the list. Tess's open gift
	// is on an item that is gone from the list, and is flagged removed, as a delete
	// would flag it.
	requireCoded(t, e.getItem(f.mom, kettle.ID), http.StatusNotFound, "not_found")
	require.Equal(t, []string{"Mug"}, e.order(f.mom, f.momList))
	require.Equal(t, []string{"removed"}, flagCodes(e.gift(tess, tesses.ID)))
	require.Zero(t, e.itemQuantity(kettle.ID))

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, sues.ID).StatusCode)

	// It is on the list again with the restored quantity, in the last place.
	back := decode[itemView](t, e.getItem(f.mom, kettle.ID))
	require.Equal(t, 2, back.Quantity)
	require.Equal(t, 1, back.Position)
	require.Equal(t, []string{"Mug", "Kettle"}, e.order(f.mom, f.momList))
	// Sue's gift is open and claimed again. Tess's item is back, so her flag goes.
	mine := e.gift(f.sue, sues.ID)
	require.Equal(t, "open", mine.State)
	require.Equal(t, []string{"restored"}, flagCodes(mine))
	require.Equal(t, "2027-01-01", mine.OccasionDate, "the soonest occasion of Mom after Dec 7")
	require.Empty(t, e.gift(tess, tesses.ID).Flags)
	require.Equal(t, 2, *decode[itemView](t, e.getItem(f.sue, kettle.ID)).MyClaimedQuantity)
}

func TestARestoreAddsTheQuantityToWhatAnItemOnItsListHasLeft(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 5})
	g := e.given(f.sue, kettle.ID, map[string]any{"quantity": 2})
	require.Equal(t, 3, decode[itemView](t, e.getItem(f.mom, kettle.ID)).Quantity)
	// Mom lowers it to 1 after the reveal: the item has 1 left, and works from that.
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, kettle.ID, map[string]any{"quantity": 1}, nil).StatusCode)

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	require.Equal(t, 3, decode[itemView](t, e.getItem(f.mom, kettle.ID)).Quantity, "1 left plus the 2 that come back")
	require.Equal(t, 1, *decode[itemView](t, e.getItem(f.sue, kettle.ID)).StillWanted, "3 on the item, 2 claimed by Sue")
}

func TestAnUnlimitedItemTakesNoQuantityBack(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	socks := e.addItem(f.mom, f.momList, "Socks", map[string]any{"is_unlimited": true})
	g := e.given(f.sue, socks.ID, map[string]any{"quantity": 4})

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	got := decode[itemView](t, e.getItem(f.sue, socks.ID))
	require.True(t, got.IsUnlimited)
	require.Equal(t, 4, *got.MyClaimedQuantity)
	require.Nil(t, got.StillWanted)
	require.Equal(t, "open", e.gift(f.sue, g.ID).State)
}

func TestARestoreOntoADeletedItemKeepsItDeletedAndFlagsTheGiftRemoved(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "price_minor": 45000, "image_ids": imageIDs(photo)})
	g := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": advent.ID})
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.dad, grill.ID, nil).StatusCode)
	e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"removed"}}) // Sue keeps it
	require.Equal(t, 1, e.revealAt("2026-12-07T00:00:00Z"))
	require.Equal(t, 2, e.itemQuantity(grill.ID), "a deleted item keeps the quantity it had: the given quantity doesn't leave it")

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	requireCoded(t, e.getItem(f.mom, grill.ID), http.StatusNotFound, "not_found")
	require.Equal(t, 2, e.itemQuantity(grill.ID), "nothing went back: the item stays deleted")
	got := e.gift(f.sue, g.ID)
	require.Equal(t, "open", got.State)
	require.Equal(t, []string{"restored", "removed"}, flagCodes(got))
	require.Equal(t, "Grill", got.Title, "the copy from the reveal")
	require.EqualValues(t, 45000, *got.PriceMinor)
	require.NotNil(t, got.Image, "the copy of the image stays")
	require.Equal(t, http.StatusOK, e.getImage(f.sue, got.Image.ID).StatusCode)
	require.True(t, got.NeedsAttention, "an open gift flagged removed needs attention")
	// Sue keeps it, and it is given again at the next reveal, from the copy.
	e.acknowledge(f.sue, g.ID, nil)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	again := e.gift(f.mom, g.ID)
	require.Equal(t, "given", again.State)
	require.Equal(t, "Grill", again.Title)
	require.Equal(t, 2, e.itemQuantity(grill.ID))
}

func TestARestoreOntoAFullyGivenItemOfADeletedListKeepsItDeleted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	golf := e.extraList("Golf", f.dadID)
	putter := e.addItem(f.dad, golf, "Putter")
	other := e.addItem(f.dad, golf, "Tees")
	g := e.given(f.sue, putter.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.dad, other.ID, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteList(f.dad, golf, nil).StatusCode)
	requireCoded(t, e.getList(f.dad, golf), http.StatusNotFound, "not_found")

	require.Equal(t, http.StatusNoContent, e.restore(f.dad, g.ID).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, "open", got.State)
	require.Equal(t, []string{"restored", "removed"}, flagCodes(got))
	require.Equal(t, "Golf", got.List.Name)
	require.Equal(t, "2028-01-01", got.OccasionDate, "Dad's next birthday: the giver sees none of the last recipients")
	require.Nil(t, got.Occasion)
	require.NotContains(t, e.rawGift(f.sue, g.ID), "recipients")
	requireCoded(t, e.getItem(f.dad, putter.ID), http.StatusNotFound, "not_found")
	require.Zero(t, e.itemQuantity(putter.ID))
}

func TestRestoreDeletesAGiftWhoseGiverIsDeactivatedAndPutsTheQuantityBackUnclaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "image_ids": imageIDs(photo)})
	g := e.given(f.sue, grill.ID, map[string]any{"quantity": 2})
	copyID := e.gift(f.mom, g.ID).Image.ID
	e.deactivated(f.admin, f.sueID)
	tess := e.giver("Tess", f)

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	requireCoded(t, e.getGift(f.mom, g.ID), http.StatusNotFound, "not_found")
	var rows int
	require.NoError(t, e.db.GetContext(t.Context(), &rows, `SELECT count(*) FROM gifts WHERE id = ?`, g.ID))
	require.Zero(t, rows, "the gift is deleted")
	back := decode[itemView](t, e.getItem(f.mom, grill.ID))
	require.Equal(t, 2, back.Quantity)
	require.Equal(t, 2, *decode[itemView](t, e.getItem(tess, grill.ID)).StillWanted, "unclaimed: another giver can claim it")
	// The copy of the image goes to the sweep.
	e.clock.advance(time.Minute)
	require.Positive(t, e.sweep())
	requireCoded(t, e.getImage(f.mom, copyID), http.StatusNotFound, "not_found")
}

func TestRestoreDeletesAGiftWhoseGiverWasDeleted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.given(f.sue, grill.ID, nil)
	_, err := e.db.ExecContext(t.Context(), `UPDATE gifts SET giver_id = NULL WHERE id = ?`, g.ID)
	require.NoError(t, err)
	require.Nil(t, e.gift(f.mom, g.ID).Giver)

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	requireCoded(t, e.getGift(f.mom, g.ID), http.StatusNotFound, "not_found")
	require.Equal(t, 1, decode[itemView](t, e.getItem(f.mom, grill.ID)).Quantity)
}

func TestRestoreOntoADeletedItemWithAGoneGiverPurgesTheItem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, grill.ID, nil).StatusCode)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	_, err := e.db.ExecContext(t.Context(), `UPDATE gifts SET giver_id = NULL WHERE id = ?`, g.ID)
	require.NoError(t, err)

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	require.Zero(t, e.itemRows(), "the last gift went, so the deleted item goes for good")
}

func TestRestoreReleasesAGiftWhoseGiverIsNowTheOnlyRecipient(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.given(f.sue, grill.ID, nil)
	e.joinList(f.mom, f.house, f.sueID, f.sue)
	require.Equal(t, http.StatusNoContent, e.leave(f.dad, f.house, f.dadID, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, nil).StatusCode)

	// Mom was a recipient of the gift, and left the list since.
	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	require.Equal(t, 1, decode[itemView](t, e.getItem(f.sue, grill.ID)).Quantity)
}

func TestARestoredGiftMergesIntoTheGiversOpenGiftForTheSameOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 4})
	g := e.given(f.sue, kettle.ID, map[string]any{"quantity": 2, "from_label": "Gran"})
	// Sue claims again for Mom's next birthday, while the first is given.
	next := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 1, "note": "Blue"})
	require.Equal(t, "2028-01-01", next.OccasionDate)
	require.Equal(t, next.Occasion.ID, f.birthdayOfMom(e))

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	// The gift that was there takes in the restored one, which is deleted.
	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	merged := e.gift(f.sue, next.ID)
	require.Equal(t, 3, merged.Quantity)
	require.Equal(t, "Gran", *merged.FromLabel, "it had no label, so it takes the moving gift's")
	require.Equal(t, []string{"restored"}, flagCodes(merged))
	require.Equal(t, 4, decode[itemView](t, e.getItem(f.mom, kettle.ID)).Quantity)
	require.Equal(t, 3, *decode[itemView](t, e.getItem(f.sue, kettle.ID)).MyClaimedQuantity)
}

func TestARestoredGiftIsGivenAgainAtTheNextRevealWithANewCopy(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "image_ids": imageIDs(photo)})
	g := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": advent.ID})
	require.Equal(t, 1, e.revealAt("2026-12-07T00:00:00Z"))
	firstCopy := e.gift(f.mom, g.ID).Image.ID
	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)
	require.Equal(t, 2, decode[itemView](t, e.getItem(f.mom, grill.ID)).Quantity, "the one that was given is back")
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"title": "Big grill"}, nil).StatusCode)
	require.Equal(t, "Grill", e.gift(f.sue, g.ID).Title, "an open gift keeps its copy until the next reveal")

	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	again := e.gift(f.dad, g.ID)
	require.Equal(t, "given", again.State)
	require.Equal(t, "Big grill", again.Title, "the next reveal copies the item as it is then")
	require.NotEqual(t, firstCopy, again.Image.ID)
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(again.Recipients)))
	require.Equal(t, 1, decode[itemView](t, e.getItem(f.mom, grill.ID)).Quantity)
	// The old copy was let go: the sweep deletes it before the 24 hours are up.
	e.clock.advance(time.Minute)
	require.Positive(t, e.sweep())
	requireCoded(t, e.getImage(f.sue, firstCopy), http.StatusNotFound, "not_found")
	require.Equal(t, http.StatusOK, e.getImage(f.sue, again.Image.ID).StatusCode)
}

func TestUnclaimingARestoredGiftPutsTheQuantityBackForAllGiversAndLetsGoOfItsCopy(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "image_ids": imageIDs(photo)})
	g := e.given(f.sue, grill.ID, map[string]any{"quantity": 2})
	copyID := e.gift(f.sue, g.ID).Image.ID
	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)
	tess := e.giver("Tess", f)
	requireCoded(t, e.getItem(tess, grill.ID), http.StatusNotFound, "not_found")

	require.Equal(t, http.StatusNoContent, e.deleteGift(f.sue, g.ID, nil).StatusCode)

	require.Equal(t, 2, *decode[itemView](t, e.getItem(tess, grill.ID)).StillWanted)
	e.clock.advance(time.Minute)
	require.Positive(t, e.sweep())
	requireCoded(t, e.getImage(f.sue, copyID), http.StatusNotFound, "not_found")
}

func TestARestoredGiftIsFlaggedNoLongerSharedWhenItsGiverCanNotSeeTheList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.given(f.sue, grill.ID, nil)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)

	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, []string{"restored", "no_longer_shared"}, flagCodes(got))
	require.Nil(t, flagOf(t, got, "restored")["by"], "Sue can't see Mom any more")
	require.Equal(t, "2028-01-01", got.OccasionDate, "the next occasion of the recipients, picked by the server")
	require.Nil(t, got.Occasion)
}
