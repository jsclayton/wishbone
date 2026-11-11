package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cover the changed flag: the fields of an edited item that flag
// the item's open gifts, and the ones that don't (docs/spec.md, Items, Who
// changes an item; Gifts, Changes while a gift is open).

// changes returns the lines of a gift's changed flag, by field (and by store
// link URL, for a store link's line).
func changes(t *testing.T, g giftView) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, raw := range flagOf(t, g, "changed")["fields"].([]any) {
		line := raw.(map[string]any)
		key := line["field"].(string)
		if url, ok := line["url"]; ok {
			key += " " + url.(string)
		}
		out[key] = line
	}
	return out
}

// price is a price as a flag shows it, with the numbers as JSON decodes them.
func price(minor int, top any, currency string) map[string]any {
	if n, ok := top.(int); ok {
		top = float64(n)
	}
	return map[string]any{"price_minor": float64(minor), "top_price_minor": top, "currency": currency}
}

func TestEditingAClaimedItemFlagsTheGiftChangedWithWhatChanged(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "notes": "Gas"})
	g := e.claim(f.sue, grill.ID, nil)
	tess := e.giver("Tess", f)
	elsewhere := e.claim(tess, e.addItem(f.mom, f.house, "Other").ID, nil)
	e.addItem(f.mom, f.house, "Unclaimed")

	// Dad did not add the item: whoever can edit the list takes the owner's place.
	res := e.patchItem(f.dad, grill.ID, map[string]any{"title": "Big grill", "notes": nil, "quantity": 3, "is_unlimited": true}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, []string{"changed"}, flagCodes(got))
	require.True(t, got.CanAcknowledge)
	require.True(t, got.NeedsAttention)
	lines := changes(t, got)
	require.Len(t, lines, 4)
	require.Equal(t, map[string]any{"field": "title", "old": "Grill", "new": "Big grill"}, lines["title"])
	require.Equal(t, map[string]any{"field": "notes", "old": "Gas", "new": nil}, lines["notes"])
	require.Equal(t, map[string]any{"field": "quantity", "old": float64(2), "new": float64(3)}, lines["quantity"])
	require.Equal(t, map[string]any{"field": "is_unlimited", "old": false, "new": true}, lines["is_unlimited"])
	require.Empty(t, e.gift(tess, elsewhere.ID).Flags, "a gift on another item")

	// A patch that changes nothing flags nothing, and a gift claimed after an edit is not flagged by it.
	uma := e.giver("Uma", f)
	later := e.claim(uma, grill.ID, nil)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"title": "Big grill", "position": 0}, nil).StatusCode)
	require.Empty(t, e.gift(uma, later.ID).Flags)
	require.Len(t, changes(t, e.gift(f.sue, g.ID)), 4)

	// Acknowledging changed drops the diff, and a new diff starts from what the giver last saw.
	cleared := decode[giftView](t, e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"changed"}}))
	require.Empty(t, cleared.Flags)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"title": "Grill"}, nil).StatusCode)
	require.Equal(t, map[string]any{"field": "title", "old": "Big grill", "new": "Grill"}, changes(t, e.gift(f.sue, g.ID))["title"])
}

func TestAFieldThatChangesAgainKeepsItsFirstOldValueAndChangingBackRemovesTheLine(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"notes": "Gas"})
	g := e.claim(f.sue, grill.ID, nil)

	e.patchItem(f.mom, grill.ID, map[string]any{"title": "Grill 2", "notes": "Charcoal"}, nil)
	e.patchItem(f.mom, grill.ID, map[string]any{"title": "Grill 3"}, nil)
	lines := changes(t, e.gift(f.sue, g.ID))
	require.Equal(t, map[string]any{"field": "title", "old": "Grill", "new": "Grill 3"}, lines["title"])

	e.patchItem(f.mom, grill.ID, map[string]any{"title": "Grill"}, nil)
	lines = changes(t, e.gift(f.sue, g.ID))
	require.Len(t, lines, 1, "the title is as the giver picked it")
	require.Contains(t, lines, "notes")

	e.patchItem(f.mom, grill.ID, map[string]any{"notes": "Gas"}, nil)
	got := e.gift(f.sue, g.ID)
	require.Empty(t, got.Flags, "nothing is left to flag")
	require.False(t, got.CanAcknowledge)
}

func TestThePriceFlagsWhenItChangesOrClearsButNotWhenItIsSetForTheFirstTime(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)

	// The item's first price ever is not a change to the gift.
	e.patchItem(f.mom, grill.ID, map[string]any{"price_minor": 50000}, nil)
	require.Empty(t, e.gift(f.sue, g.ID).Flags)

	e.patchItem(f.mom, grill.ID, map[string]any{"price_minor": 45000, "top_price_minor": 60000, "currency": "EUR"}, nil)
	lines := changes(t, e.gift(f.sue, g.ID))
	require.Equal(t, map[string]any{"field": "price", "old": price(50000, nil, "USD"), "new": price(45000, 60000, "EUR")}, lines["price"])

	e.patchItem(f.mom, grill.ID, map[string]any{"price_minor": nil, "top_price_minor": nil}, nil)
	lines = changes(t, e.gift(f.sue, g.ID))
	require.Equal(t, map[string]any{"field": "price", "old": price(50000, nil, "USD"), "new": nil}, lines["price"], "old to none")

	// A price set after a clear flags: the item had one once.
	e.acknowledge(f.sue, g.ID, nil)
	e.patchItem(f.mom, grill.ID, map[string]any{"price_minor": 70000}, nil)
	lines = changes(t, e.gift(f.sue, g.ID))
	require.Equal(t, map[string]any{"field": "price", "old": nil, "new": price(70000, nil, "USD")}, lines["price"])
}

func TestAChangeOfTheMainImageFlagsAndAChangeOfAnotherImageDoesNot(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	a, b, c := e.uploaded(f.mom), e.uploaded(f.mom), e.uploaded(f.mom)

	require.Equal(t, http.StatusOK, e.patchImages(f.mom, grill.ID, map[string]any{"image_ids": []string{a.ID, b.ID}}).StatusCode)
	lines := changes(t, e.gift(f.sue, g.ID))
	require.Equal(t, map[string]any{"field": "primary_image"}, lines["primary_image"], "the first image is the main image, and it has no text to show")
	e.acknowledge(f.sue, g.ID, nil)

	require.Equal(t, http.StatusOK, e.patchImages(f.mom, grill.ID, map[string]any{"image_ids": []string{a.ID, c.ID}}).StatusCode)
	require.Empty(t, e.gift(f.sue, g.ID).Flags, "b was not the main image")

	require.Equal(t, http.StatusOK, e.patchImages(f.mom, grill.ID, map[string]any{"image_ids": []string{a.ID, c.ID}, "primary_image_id": c.ID}).StatusCode)
	require.Contains(t, changes(t, e.gift(f.sue, g.ID)), "primary_image")
}

func TestStoreLinkChangesFlagTheGiftAndAddingOneDoesNot(t *testing.T) {
	shop := newPricedShop()
	shop.set("https://a.example/k", "30")
	shop.set("https://b.example/k", "40")
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)

	// Adding a link, and its first price, change nothing about the gift.
	a := decode[storeLinkView](t, e.postStoreLink(f.mom, grill.ID, "https://a.example/k"))
	b := decode[storeLinkView](t, e.postStoreLink(f.mom, grill.ID, "https://b.example/k"))
	require.Empty(t, e.gift(f.sue, g.ID).Flags)

	// A refresh that changes a price flags it, with both prices; one that changes nothing does not.
	require.Equal(t, http.StatusOK, e.refreshPrices(f.mom, grill.ID).StatusCode)
	require.Empty(t, e.gift(f.sue, g.ID).Flags, "a refresh with no price change")
	shop.set("https://a.example/k", "25")
	e.clock.advance(time.Hour)
	require.Equal(t, http.StatusOK, e.refreshPrices(f.mom, grill.ID).StatusCode)
	lines := changes(t, e.gift(f.sue, g.ID))
	require.Len(t, lines, 1)
	require.Equal(t, map[string]any{"field": "store_link_price", "url": "https://a.example/k", "old": price(3000, nil, "EUR"), "new": price(2500, nil, "EUR")},
		lines["store_link_price https://a.example/k"])

	// A hand edit that clears a price flags it too, and removing a link flags the removal.
	require.Equal(t, http.StatusOK, e.patchStoreLink(f.mom, grill.ID, b.ID, map[string]any{"price_minor": nil}).StatusCode)
	require.Contains(t, changes(t, e.gift(f.sue, g.ID)), "store_link_price https://b.example/k")
	require.Equal(t, http.StatusNoContent, e.deleteStoreLink(f.mom, grill.ID, a.ID).StatusCode)
	removed := changes(t, e.gift(f.sue, g.ID))["store_link_removed https://a.example/k"]
	require.Equal(t, map[string]any{"field": "store_link_removed", "url": "https://a.example/k"}, removed)
}

func TestAGivenGiftIsNeverFlagged(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	g := e.claim(f.sue, grill.ID, nil)
	_, err := e.db.ExecContext(t.Context(), `UPDATE gifts SET given_at = ? WHERE id = ?`, e.clock.now(), g.ID)
	require.NoError(t, err)

	e.patchItem(f.mom, grill.ID, map[string]any{"title": "Big grill"}, nil)
	e.deleteItem(f.mom, grill.ID, nil)

	got := e.gift(f.sue, g.ID)
	require.Empty(t, got.Flags)
	require.False(t, got.NeedsAttention, "a given gift never needs attention")
}
