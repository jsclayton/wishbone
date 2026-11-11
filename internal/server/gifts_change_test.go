package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover changing a gift, merging two of them, and unclaiming
// (docs/spec.md, Gifts; docs/routes.md, Gift routes).

func TestAGiverChangesTheFieldsOfAnOpenGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 4})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID), "note": "Blue", "from_label": "Emma"})

	res := e.patchGift(f.sue, g.ID, map[string]any{
		"quantity": 3, "occasion_id": advent.ID, "from_label": "Emma and Max", "note": "Blue one", "is_bought": true,
	}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[giftView](t, res)
	require.Equal(t, g.ID, got.ID)
	require.Equal(t, 3, got.Quantity)
	require.Equal(t, advent.ID, got.Occasion.ID)
	require.Equal(t, "2026-12-06", got.OccasionDate)
	require.Equal(t, "2026-12-07T00:00:00Z", got.RevealAt)
	require.Equal(t, "Emma and Max", *got.FromLabel)
	require.Equal(t, "Blue one", *got.Note)
	require.True(t, got.IsBought)
	require.Equal(t, 1, *decode[itemView](t, e.getItem(f.sue, kettle.ID)).StillWanted, "three of four are claimed")
	require.NotEqual(t, g.Version, res.Header.Get("ETag"), "the version follows the body")
}

func TestAGiverClearsALabelAndANoteWithNullAndLowersTheQuantity(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 4})
	g := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 3, "note": "Blue", "from_label": "Emma"})
	tess := e.giver("Tess", f)
	requireCoded(t, e.postGift(tess, kettle.ID, map[string]any{"quantity": 2}), http.StatusConflict, "item_fully_claimed")

	res := e.patchGift(f.sue, g.ID, map[string]any{"quantity": 1, "note": nil, "from_label": ""}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[giftView](t, res)
	require.Nil(t, got.Note)
	require.Nil(t, got.FromLabel)
	require.Equal(t, 1, got.Quantity)
	e.claim(tess, kettle.ID, map[string]any{"quantity": 2}) // the quantity went back to all givers
}

func TestARaiseNeedsThatMoreAreStillWanted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	g := e.claim(f.sue, kettle.ID, nil)
	tess := e.giver("Tess", f)
	e.claim(tess, kettle.ID, map[string]any{"quantity": 1})

	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 3}, nil), http.StatusConflict, "item_fully_claimed")
	require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 2}, nil).StatusCode)
	require.False(t, decode[giftView](t, e.getGift(f.sue, g.ID)).CanRaiseQuantity, "nothing is still wanted")
}

func TestAGiftsFieldsAreChecked(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	g := e.claim(f.sue, grill.ID, nil)
	dads := e.birthdayOf(f.dad, f.dadID)

	for name, tc := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"a quantity of 0":                 {map[string]any{"quantity": 0}, 422, "invalid_input"},
		"a null quantity":                 {map[string]any{"quantity": nil}, 422, "invalid_input"},
		"a null occasion":                 {map[string]any{"occasion_id": nil}, 422, "invalid_input"},
		"a null bought mark":              {map[string]any{"is_bought": nil}, 422, "invalid_input"},
		"a from label over 60":            {map[string]any{"from_label": strings.Repeat("x", 61)}, 422, "invalid_input"},
		"an occasion the giver can't see": {map[string]any{"occasion_id": dads}, 422, "occasion_not_allowed"},
		"an occasion that is nobody's":    {map[string]any{"occasion_id": "999"}, 422, "occasion_not_allowed"},
		"a field the giver sees":          {map[string]any{"state": "given"}, 403, "field_not_writable"},
		"the item":                        {map[string]any{"item": map[string]any{"id": "1"}}, 403, "field_not_writable"},
	} {
		res := e.patchGift(f.sue, g.ID, tc.body, nil)
		require.Equal(t, tc.status, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	after := decode[giftView](t, e.getGift(f.sue, g.ID))
	require.Equal(t, g.Version, e.getGift(f.sue, g.ID).Header.Get("ETag"), "a refused change changes nothing")
	require.Equal(t, 1, after.Quantity)
}

func TestAGiftsETagGuardsItsEditsAndItsUnclaim(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	g := e.claim(f.sue, kettle.ID, nil)
	stale := http.Header{"If-Match": {`"stale"`}}

	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": true}, stale), http.StatusPreconditionFailed, "resource_changed")
	requireCoded(t, e.deleteGift(f.sue, g.ID, stale), http.StatusPreconditionFailed, "resource_changed")
	require.False(t, decode[giftView](t, e.getGift(f.sue, g.ID)).IsBought)
	fresh := http.Header{"If-Match": {g.Version}}
	require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": true}, fresh).StatusCode)
	require.Equal(t, http.StatusPreconditionFailed, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": false}, fresh).StatusCode, "the version moved")
}

func TestAGiftThatMovesToAnOccasionThatHoldsAGiftMergesIntoIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 6})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	birthday := e.birthdayOf(f.sue, f.momID)
	staying := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": birthday, "quantity": 2, "is_bought": true, "note": "Blue"})
	moving := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID, "quantity": 1, "from_label": "Emma", "note": "Or green"})

	res := e.patchGift(f.sue, moving.ID, map[string]any{"occasion_id": birthday, "quantity": 3, "is_bought": false}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[giftView](t, res)
	require.Equal(t, staying.ID, got.ID, "the gift that was already there stays")
	require.Equal(t, 5, got.Quantity, "the quantities add up, with the change applied")
	require.True(t, got.IsBought, "the bought mark stays")
	require.Equal(t, "Emma", *got.FromLabel, "it had none, so it takes the moving gift's")
	require.Equal(t, "Blue\nOr green", *got.Note, "a note goes at the end, on a new line")
	require.Equal(t, birthday, got.Occasion.ID)
	requireCoded(t, e.getGift(f.sue, moving.ID), http.StatusNotFound, "not_found")
	require.Equal(t, []string{staying.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.sue, ""))))
	require.Equal(t, 1, *decode[itemView](t, e.getItem(f.sue, kettle.ID)).StillWanted, "five of six are claimed")
}

func TestTheStayingGiftKeepsItsOwnFromLabelOnAMerge(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 6})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	birthday := e.birthdayOf(f.sue, f.momID)
	staying := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": birthday, "from_label": "Max"})
	moving := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID, "from_label": "Emma"})

	got := decode[giftView](t, e.patchGift(f.sue, moving.ID, map[string]any{"occasion_id": birthday}, nil))

	require.Equal(t, staying.ID, got.ID)
	require.Equal(t, "Max", *got.FromLabel)
	require.Nil(t, got.Note)
}

func TestAGiverUnclaimsAnOpenGiftAndTheQuantityGoesBack(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	tess := e.giver("Tess", f)
	requireCoded(t, e.getItem(tess, kettle.ID), http.StatusNotFound, "not_found")

	res := e.deleteGift(f.sue, g.ID, nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	require.Equal(t, http.StatusNotFound, e.deleteGift(f.sue, g.ID, nil).StatusCode, "a gift that is gone is a 404")
	back := decode[itemView](t, e.getItem(tess, kettle.ID))
	require.Equal(t, 1, *back.StillWanted)
	require.True(t, back.CanClaim)
}

func TestAGivenGiftCannotBeChangedOrUnclaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	_, err := e.db.ExecContext(t.Context(), `UPDATE gifts SET given_at = ? WHERE id = ?`, e.clock.now(), g.ID)
	require.NoError(t, err)

	requireCoded(t, e.deleteGift(f.sue, g.ID, nil), http.StatusForbidden, "gift_given")
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": true}, nil), http.StatusForbidden, "forbidden")
	got := decode[giftView](t, e.getGift(f.sue, g.ID))
	require.False(t, got.CanEdit)
	require.False(t, got.CanDelete)
	require.False(t, got.CanRaiseQuantity)
}

func TestAGiverWhoBecomesARecipientKeepsTheGiftAndNeverRaisesIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.dad, f.house, "Grill", map[string]any{"quantity": 5})
	g := e.claim(f.sue, grill.ID, map[string]any{"quantity": 2})
	require.True(t, g.CanRaiseQuantity)
	_, err := e.db.ExecContext(t.Context(), `INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (?, ?, ?)`, f.house, f.sueID, e.clock.now())
	require.NoError(t, err)

	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 3}, nil), http.StatusForbidden, "quantity_raise_not_allowed")
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 9}, nil), http.StatusForbidden, "quantity_raise_not_allowed")
	got := decode[giftView](t, e.getGift(f.sue, g.ID))
	require.False(t, got.CanRaiseQuantity)
	require.True(t, got.CanEdit)
	require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 1, "note": "For Dad"}, nil).StatusCode, "a lower quantity is fine")
	requireCoded(t, e.postGift(f.sue, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	// A recipient never sees the claim state of an item.
	require.NotContains(t, readBody(t, e.getItem(f.sue, grill.ID)), "still_wanted")
}

func TestDeletingAnItemWithOpenGiftsIsSoft(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2, "price_minor": 4500})
	e.addItem(f.mom, f.momList, "Other")
	e.addItem(f.mom, f.momList, "Last")
	img := e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.patchImages(f.mom, kettle.ID, map[string]any{"image_ids": []string{img.ID}}).StatusCode)
	g := e.claim(f.sue, kettle.ID, nil)
	tess := e.giver("Tess", f)

	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, kettle.ID, nil).StatusCode)

	// Nobody sees the item, and the list closes the gap.
	for _, token := range []string{f.mom, f.sue, tess} {
		requireCoded(t, e.getItem(token, kettle.ID), http.StatusNotFound, "not_found")
	}
	require.Equal(t, []string{"Other", "Last"}, e.order(f.mom, f.momList))
	require.Equal(t, []string{"Other", "Last"}, e.order(tess, f.momList))
	require.Equal(t, 2, decode[listView](t, e.getList(f.sue, f.momList)).ItemCount)
	require.Equal(t, 0, decode[itemList](t, e.listItems(f.mom, f.momList, "")).Data[0].Position)
	require.Equal(t, 1, decode[itemList](t, e.listItems(f.mom, f.momList, "")).Data[1].Position)
	// The gift stays, with what the item was, and its image still loads for the giver.
	kept := decode[giftView](t, e.getGift(f.sue, g.ID))
	require.Equal(t, "Kettle", kept.Title)
	require.Equal(t, int64(4500), *kept.PriceMinor)
	require.Equal(t, img.ID, kept.Image.ID)
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(tess, img.ID).StatusCode)
	require.Equal(t, 1, e.imageRowsHeld())
	// The giver can still change it, but nobody can claim the deleted item.
	require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": true}, nil).StatusCode)
	requireCoded(t, e.postGift(tess, kettle.ID, nil), http.StatusNotFound, "not_found")
	// Unclaiming the last gift takes the item for good.
	require.Equal(t, http.StatusNoContent, e.deleteGift(f.sue, g.ID, nil).StatusCode)
	var items int
	require.NoError(t, e.db.GetContext(t.Context(), &items, `SELECT count(*) FROM items WHERE id = ?`, kettle.ID))
	require.Zero(t, items)
	require.Equal(t, 0, e.imageRowsHeld())
}

// imageRowsHeld counts the item images that an item still holds.
func (e *env) imageRowsHeld() int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM item_images`))
	return n
}

func TestDeletingAnItemWithNoGiftsStaysAHardDelete(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")

	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, kettle.ID, nil).StatusCode)

	var items int
	require.NoError(t, e.db.GetContext(t.Context(), &items, `SELECT count(*) FROM items`))
	require.Zero(t, items)
}

func TestMovingAndAddingItemsIgnoresTheItemsDeletedSoftly(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	a := e.addItem(f.mom, f.momList, "A")
	b := e.addItem(f.mom, f.momList, "B")
	e.claim(f.sue, a.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, a.ID, nil).StatusCode)

	c := e.addItem(f.mom, f.momList, "C")

	require.Equal(t, 1, c.Position, "B is the only item left before it")
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, c.ID, map[string]any{"position": 0}, nil).StatusCode)
	require.Equal(t, []string{"C", "B"}, e.order(f.mom, f.momList))
	require.Equal(t, 1, decode[itemView](t, e.getItem(f.mom, b.ID)).Position)
}

func TestADeletedItemsImageIsAGoneForItsRecipientWhetherOrNotItWasClaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	claimed := e.addItem(f.mom, f.momList, "Claimed")
	plain := e.addItem(f.mom, f.momList, "Plain")
	claimedImg, plainImg := e.uploaded(f.mom), e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.patchImages(f.mom, claimed.ID, map[string]any{"image_ids": []string{claimedImg.ID}}).StatusCode)
	require.Equal(t, http.StatusOK, e.patchImages(f.mom, plain.ID, map[string]any{"image_ids": []string{plainImg.ID}}).StatusCode)
	e.claim(f.sue, claimed.ID, nil)

	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, claimed.ID, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, plain.ID, nil).StatusCode)

	// The recipient can't tell the two apart, because a detached image is a 404 for everyone.
	require.Equal(t, http.StatusNotFound, e.getImage(f.mom, plainImg.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.mom, claimedImg.ID).StatusCode)
	// The giver keeps the picture of their gift.
	require.Equal(t, http.StatusOK, e.getImage(f.sue, claimedImg.ID).StatusCode)
}

func TestAttachingADeletedItemsImageIsRefusedTheSameWhetherOrNotItWasClaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	claimed := e.addItem(f.mom, f.momList, "Claimed")
	plain := e.addItem(f.mom, f.momList, "Plain")
	target := e.addItem(f.mom, f.momList, "Target")
	claimedImg, plainImg := e.uploaded(f.mom), e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.patchImages(f.mom, claimed.ID, map[string]any{"image_ids": []string{claimedImg.ID}}).StatusCode)
	require.Equal(t, http.StatusOK, e.patchImages(f.mom, plain.ID, map[string]any{"image_ids": []string{plainImg.ID}}).StatusCode)
	e.claim(f.sue, claimed.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, claimed.ID, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, plain.ID, nil).StatusCode)

	for _, img := range []string{claimedImg.ID, plainImg.ID} {
		requireCoded(t, e.patchImages(f.mom, target.ID, map[string]any{"image_ids": []string{img}}), http.StatusUnprocessableEntity, "reference_not_found")
	}
}
