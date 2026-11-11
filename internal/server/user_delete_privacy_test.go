package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover what a delete shows the people who stay. A recipient never
// sees the claim state of their list, and a delete must not show it by accident:
// not in a flag, a quantity, item_count, an ETag, an image or an avatar
// (docs/spec.md, Lists, Recipients; Deactivation and deletion, Deletion).

func TestDeletingARecipientAndGiverShowsTheOtherRecipientsNothingOfClaims(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3, "image_ids": imageIDs(photo)})
	kettle := e.addItem(f.mom, f.house, "Kettle", map[string]any{"quantity": 1})
	tess := e.giver("Tess", f)
	// A given gift, and claims on Mom and Dad's list, one of them for Dad's birthday.
	given := e.given(tess, e.addItem(f.mom, f.house, "Mug", map[string]any{"quantity": 2}).ID, nil)
	e.claim(f.sue, grill.ID, map[string]any{"quantity": 3, "occasion_id": e.birthdayOf(f.sue, f.dadID)})
	e.claim(tess, kettle.ID, nil)
	// Dad also claimed the whole of an item on Hedy's list, and Hedy is a recipient.
	hedyPhoto := e.uploaded(f.hedy)
	hat := e.addItem(f.hedy, f.hedysList, "Hat", map[string]any{"quantity": 2, "image_ids": imageIDs(hedyPhoto)})
	e.join(f.admin, e.cousins(f), f.hedyID)
	e.claim(f.dad, hat.ID, map[string]any{"quantity": 2})
	reads := func() map[string]seen {
		return map[string]seen{
			"mom items":  e.seen(f.mom, "/lists/"+f.house+"/items"),
			"mom grill":  e.seen(f.mom, "/items/"+grill.ID),
			"mom kettle": e.seen(f.mom, "/items/"+kettle.ID),
			"mom photo":  e.seen(f.mom, "/images/"+photo.ID),
			"mom gifts":  e.seen(f.mom, "/gifts?recipient_id=me"),
			"mom given":  e.seen(f.mom, "/gifts/"+given.ID),
			"hedy list":  e.seen(f.hedy, "/lists/"+f.hedysList),
			"hedy items": e.seen(f.hedy, "/lists/"+f.hedysList+"/items"),
			"hedy hat":   e.seen(f.hedy, "/items/"+hat.ID),
			"hedy photo": e.seen(f.hedy, "/images/"+hedyPhoto.ID),
		}
	}
	before := reads()
	houseBefore := decode[listView](t, e.getList(f.mom, f.house))
	mugBefore := e.gift(f.mom, given.ID)

	e.deleteUser(f.dadID)

	after := reads()
	for name, was := range before {
		if name == "mom given" || name == "mom gifts" {
			continue // Dad was one of the recipients of the given gift, and it reads Mom alone now
		}
		require.Equal(t, was, after[name], "%s: the same body, ETag and image", name)
	}
	houseAfter := decode[listView](t, e.getList(f.mom, f.house))
	require.Equal(t, houseBefore.ItemCount, houseAfter.ItemCount, "item_count")
	require.Equal(t, houseBefore.Name, houseAfter.Name)
	// A recipient's claim is refused the same way for a claimed item and an unclaimed one.
	requireCoded(t, e.postGift(f.mom, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	requireCoded(t, e.postGift(f.mom, kettle.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	// The given gift reads the same, but for the recipients Dad was among.
	mugAfter := e.gift(f.mom, given.ID)
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(mugBefore.Recipients)))
	require.Equal(t, []string{f.momID}, summaryIDs(anySlice(mugAfter.Recipients)))
	mugBefore.Recipients, mugAfter.Recipients, mugBefore.Version, mugAfter.Version = nil, nil, "", ""
	require.Equal(t, mugBefore, mugAfter)
}

func TestDeletingTheOnlyUserAGiftWasGivenToChangesNothingForTheListsOtherRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	tools := e.extraList("Tools", f.dadID)
	e.join(f.admin, e.cousins(f), f.dadID)
	hammer := e.addItem(f.dad, tools, "Hammer", map[string]any{"quantity": 2, "image_ids": imageIDs(e.uploaded(f.dad))})
	saw := e.addItem(f.dad, tools, "Saw")
	e.claim(f.sue, hammer.ID, nil)
	e.claim(f.sue, saw.ID, nil)
	require.Equal(t, 2, e.revealAt("2027-01-02T00:00:00Z"))
	// Mom joins after the reveal. The saw left the list when it was given, and the
	// hammer has one left. She sees neither the saw nor that a hammer was given.
	e.joinList(f.dad, tools, f.momID, f.mom)
	_, err := e.db.ExecContext(t.Context(), `UPDATE items SET added_by = ? WHERE list_id = ?`, f.momID, tools)
	require.NoError(t, err)
	reads := func() map[string]seen {
		return map[string]seen{
			"items":  e.seen(f.mom, "/lists/"+tools+"/items"),
			"hammer": e.seen(f.mom, "/items/"+hammer.ID),
			"saw":    e.seen(f.mom, "/items/"+saw.ID),
			"gifts":  e.seen(f.mom, "/gifts?recipient_id=me"),
		}
	}
	before := reads()
	require.Equal(t, 1, decode[listView](t, e.getList(f.mom, tools)).ItemCount)
	require.Equal(t, 1, decode[itemView](t, e.getItem(f.mom, hammer.ID)).Quantity)
	requireCoded(t, e.getItem(f.mom, saw.ID), http.StatusNotFound, "not_found")

	e.deleteUser(f.dadID)

	// The two gifts were for Dad alone, so they are deleted. The items read as before:
	// the quantity does not come back on the hammer, and the saw does not come back.
	require.Zero(t, e.rowCount("gifts", `1 = 1`))
	after := reads()
	for name, was := range before {
		require.Equal(t, was, after[name], "%s: the same body and ETag", name)
	}
	require.Equal(t, 1, decode[listView](t, e.getList(f.mom, tools)).ItemCount, "item_count")
	requireCoded(t, e.getItem(f.mom, saw.ID), http.StatusNotFound, "not_found")
	require.Equal(t, 1, e.itemQuantity(hammer.ID))
}

func TestDeletingAParentWhoClaimedForTheirChildShowsTheChildNothingOfClaims(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID, f.dadID)
	emmasList := e.primaryListFor(emma, emmaID)
	photo := e.uploaded(emma)
	book := e.addItem(emma, emmasList, "Book", map[string]any{"quantity": 2, "image_ids": imageIDs(photo)})
	// A parent is not a recipient of the child's list, so Dad is a giver on it.
	e.claim(f.dad, book.ID, map[string]any{"quantity": 2})
	reads := func() map[string]seen {
		return map[string]seen{
			"list":  e.seen(emma, "/lists/"+emmasList),
			"items": e.seen(emma, "/lists/"+emmasList+"/items"),
			"book":  e.seen(emma, "/items/"+book.ID),
			"photo": e.seen(emma, "/images/"+photo.ID),
			"gifts": e.seen(emma, "/gifts?recipient_id=me"),
		}
	}
	before := reads()
	requireCoded(t, e.getItem(f.mom, book.ID), http.StatusNotFound, "not_found") // fully claimed, as a giver sees it

	res := e.deleteUser(f.dadID)

	after := reads()
	for name, was := range before {
		require.Equal(t, was, after[name], "%s: Emma reads the same body, ETag and image", name)
	}
	require.Equal(t, 2, *decode[itemView](t, e.getItem(f.mom, book.ID)).StillWanted, "the quantity is back for the givers")
	require.Empty(t, res.Orphans, "Emma still has Mom")
}
