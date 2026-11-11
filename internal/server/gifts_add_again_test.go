package server

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cover Add again, `POST /lists/{id}/items` with `from_gift_id`
// (docs/spec.md, Gifts, Add again; docs/routes.md, Item routes): a given
// gift makes a new item with the gift's title, image and price.

func (e *env) addAgain(token, listID, giftID string, more ...map[string]any) *http.Response {
	e.t.Helper()
	body := map[string]any{"from_gift_id": giftID}
	for _, m := range more {
		for k, v := range m {
			body[k] = v
		}
	}
	return e.postItem(token, listID, body)
}

func TestAddAgainCopiesTheTitleThePriceAndTheImageOfAGivenGiftAsANewImage(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{
		"quantity": 2, "price_minor": 45000, "top_price_minor": 52000, "currency": "EUR", "notes": "Charcoal", "image_ids": imageIDs(photo),
	})
	g := e.given(f.sue, grill.ID, nil)
	// The item changes after the reveal: the copy is what was given.
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"title": "Big grill", "price_minor": 46000}, nil).StatusCode)
	copyID := e.gift(f.mom, g.ID).Image.ID

	res := e.addAgain(f.mom, f.momList, g.ID)

	require.Equal(t, http.StatusCreated, res.StatusCode)
	got := decode[itemView](t, res)
	require.Equal(t, "Grill", got.Title)
	require.EqualValues(t, 45000, *got.PriceMinor)
	require.EqualValues(t, 52000, *got.TopPriceMinor)
	require.Equal(t, "EUR", *got.Currency)
	require.Equal(t, 1, got.Quantity)
	require.Nil(t, got.Notes, "only the title, the image and the price")
	require.Equal(t, f.momID, got.AddedBy.ID)
	require.Equal(t, f.momList, got.ListID)
	require.Len(t, got.Images, 1)
	require.NotNil(t, got.PrimaryImage)
	require.Equal(t, got.Images[0], *got.PrimaryImage)
	for name, id := range map[string]string{"the gift's copy": copyID, "the item's image": photo.ID} {
		require.NotEqual(t, id, got.PrimaryImage.ID, name+": the image is a new image")
	}
	want, err := io.ReadAll(e.getImage(f.mom, copyID).Body)
	require.NoError(t, err)
	bytes, err := io.ReadAll(e.getImage(f.mom, got.PrimaryImage.ID).Body)
	require.NoError(t, err)
	require.Equal(t, want, bytes)
	// The gift doesn't change.
	require.Equal(t, "given", e.gift(f.mom, g.ID).State)
	require.Equal(t, copyID, e.gift(f.mom, g.ID).Image.ID)
	// The new item's image is its own: deleting the item and sweeping leaves the gift's copy.
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, got.ID, nil).StatusCode)
	e.clock.advance(time.Minute)
	e.sweep()
	require.Equal(t, http.StatusOK, e.getImage(f.mom, copyID).StatusCode)
}

func TestFieldsInTheBodyWinOverWhatAddAgainCopies(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo, other := e.uploaded(f.mom), e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"price_minor": 45000, "image_ids": imageIDs(photo)})
	g := e.given(f.sue, grill.ID, nil)

	res := e.addAgain(f.mom, f.momList, g.ID, map[string]any{
		"title": "Small grill", "quantity": 3, "notes": "Gas", "price_minor": 9900, "currency": "USD", "image_ids": imageIDs(other),
	})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	got := decode[itemView](t, res)
	require.Equal(t, "Small grill", got.Title)
	require.Equal(t, 3, got.Quantity)
	require.Equal(t, "Gas", *got.Notes)
	require.EqualValues(t, 9900, *got.PriceMinor)
	require.Equal(t, other.ID, got.PrimaryImage.ID, "the body's image, not a copy of the gift's")

	// A price set to null wins too.
	res = e.addAgain(f.mom, f.momList, g.ID, map[string]any{"price_minor": nil})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	cleared := decode[itemView](t, res)
	require.Nil(t, cleared.PriceMinor)
	require.Nil(t, cleared.Currency)
	require.Equal(t, "Grill", cleared.Title)
	require.NotNil(t, cleared.PrimaryImage, "the image still comes from the gift")
}

func TestAnEmptyImageIDsAddsTheItemAgainWithNoImage(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"image_ids": imageIDs(photo)})
	g := e.given(f.sue, grill.ID, nil)
	require.NotNil(t, e.gift(f.mom, g.ID).Image)

	res := e.addAgain(f.mom, f.momList, g.ID, map[string]any{"image_ids": []string{}})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	got := decode[itemView](t, res)
	require.Equal(t, "Grill", got.Title)
	require.Empty(t, got.Images, "body fields win: an empty array means no image")
	require.Nil(t, got.PrimaryImage)
	// Left out, the image still comes from the gift.
	require.NotNil(t, decode[itemView](t, e.addAgain(f.mom, f.momList, g.ID)).PrimaryImage)
}

func TestAddAgainAnswersTheSameWhateverHasHappenedToTheGiftsItem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	tess := e.giver("Tess", f)
	// Three given gifts of three items: one still on its list, one deleted, one
	// whose whole quantity was given (so it is off its list, as a claimed item is).
	kept := e.addItem(f.mom, f.momList, "Kept", map[string]any{"quantity": 2, "price_minor": 100})
	deleted := e.addItem(f.mom, f.momList, "Deleted", map[string]any{"quantity": 2, "price_minor": 100})
	all := e.addItem(f.mom, f.momList, "All", map[string]any{"price_minor": 100})
	claimedAfter := e.addItem(f.mom, f.momList, "Claimed after", map[string]any{"quantity": 2, "price_minor": 100})
	gifts := []giftView{
		e.claim(f.sue, kept.ID, nil), e.claim(f.sue, deleted.ID, nil), e.claim(f.sue, all.ID, nil), e.claim(f.sue, claimedAfter.ID, nil),
	}
	require.Equal(t, 4, e.revealAt("2027-01-02T00:00:00Z"))
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, deleted.ID, nil).StatusCode)
	e.claim(tess, claimedAfter.ID, map[string]any{"occasion_id": e.birthdayOf(tess, f.momID)}) // the last one: Tess has it open
	titles := []string{"Kept", "Deleted", "All", "Claimed after"}

	for i, g := range gifts {
		// The giver of the gift and a stranger get the same answer for each of them.
		requireCoded(t, e.addAgain(f.sue, e.primaryListFor(f.sue, f.sueID), g.ID), http.StatusForbidden, "reference_not_allowed")
		requireCoded(t, e.addAgain(f.hedy, f.hedysList, g.ID), http.StatusUnprocessableEntity, "reference_not_found")
		// Its recipient gets a new item with its title, whatever the item is now.
		res := e.addAgain(f.mom, f.momList, g.ID)
		require.Equal(t, http.StatusCreated, res.StatusCode, titles[i])
		got := decode[itemView](t, res)
		require.Equal(t, titles[i], got.Title)
		require.EqualValues(t, 100, *got.PriceMinor)
		require.Equal(t, 1, got.Quantity)
		require.Nil(t, got.Notes)
		require.Empty(t, got.Images)
	}
}

func TestAddAgainRefusesAGiftTheCallerDoesNotReceiveWithoutSayingWhetherItExists(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	open := e.claim(f.sue, grill.ID, nil)
	missing := e.addAgain(f.mom, f.momList, "999999")
	require.Equal(t, http.StatusUnprocessableEntity, missing.StatusCode)
	want := readBody(t, missing)

	// An open gift is a gift that doesn't exist for its recipients: the same 422.
	for name, c := range map[string][2]string{"Mom": {f.mom, f.momList}, "Dad": {f.dad, f.dadList}} {
		res := e.addAgain(c[0], c[1], open.ID)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, want, readBody(t, res), name)
	}
	requireCoded(t, e.addAgain(f.mom, f.momList, "nope"), http.StatusUnprocessableEntity, "reference_not_found")
	// Its giver is refused with a code of its own, before and after reveal.
	suesList := e.primaryListFor(f.sue, f.sueID)
	requireCoded(t, e.addAgain(f.sue, suesList, open.ID), http.StatusForbidden, "reference_not_allowed")
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	requireCoded(t, e.addAgain(f.sue, suesList, open.ID), http.StatusForbidden, "reference_not_allowed")
	tessID, tess := e.cousin("Tess", f)
	for name, c := range map[string][2]string{
		"Hedy": {f.hedy, f.hedysList}, "the admin": {f.admin, e.primaryListFor(f.admin, f.adminID)}, "another giver": {tess, e.primaryListFor(tess, tessID)},
	} {
		res := e.addAgain(c[0], c[1], open.ID)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, want, readBody(t, res), name)
	}
	// After a restore the gift is hidden again, and is the same 422 for its recipient.
	require.Equal(t, http.StatusNoContent, e.restore(f.mom, open.ID).StatusCode)
	res := e.addAgain(f.mom, f.momList, open.ID)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, want, readBody(t, res))
}

func TestAddAgainNeedsAListTheCallerEditsAndATitleFromSomewhere(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.given(f.sue, grill.ID, nil)

	// Mom doesn't edit Dad's list.
	requireCoded(t, e.addAgain(f.mom, f.dadList, g.ID), http.StatusNotFound, "not_found")
	// A parent adds it to any list they edit: Dad edits "Our house" too.
	require.Equal(t, http.StatusCreated, e.addAgain(f.dad, f.house, g.ID).StatusCode)
	// With neither a title nor a gift there is nothing to add.
	res := e.postItem(f.mom, f.momList, map[string]any{"notes": "x"})
	requireCoded(t, res, http.StatusUnprocessableEntity, "invalid_input")
	// A null gift is no gift.
	res = e.postItem(f.mom, f.momList, map[string]any{"from_gift_id": nil, "title": "Plain"})
	require.Equal(t, http.StatusCreated, res.StatusCode)
}
