package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract"
)

// These tests cover the item price: the price givers see (docs/routes.md,
// Items, The item price). It starts from a store link, and a price a user
// clears stays cleared.

var pricedLink = []map[string]any{{"url": "https://a.example/k", "price_minor": 2500, "currency": "EUR"}}

func TestASearchResultSentOnUnchangedGivesTheItemTheStoreLinksPrice(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, nil))
	e.signUp()
	token, listID := e.grace()

	// The result of a link has price_minor, top_price_minor and currency as null:
	// the page's price is on its store link.
	res := only(t, e.search(token, "https://shop.example/kettle"))
	require.Nil(t, res.PriceMinor)
	body := map[string]any{
		"title": *res.Title, "notes": res.Notes, "price_minor": res.PriceMinor, "top_price_minor": res.TopPriceMinor, "currency": res.Currency,
		"store_links": []map[string]any{{
			"url": res.StoreLinks[0].URL, "price_minor": res.StoreLinks[0].PriceMinor, "currency": res.StoreLinks[0].Currency, "image_url": res.StoreLinks[0].ImageURL,
		}},
	}
	it := decode[itemView](t, e.postItem(token, listID, body))

	require.Equal(t, int64(4999), *it.PriceMinor, "null is not a choice of no price")
	require.Equal(t, "USD", *it.Currency)
	require.Equal(t, int64(4999), *it.StoreLinks[0].PriceMinor)
}

func TestNullAndAbsentPricesAreTheSameOnCreate(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	nulled := e.addItem(token, listID, "Kettle", map[string]any{"price_minor": nil, "top_price_minor": nil, "currency": nil, "store_links": pricedLink})
	absent := e.addItem(token, listID, "Mug", map[string]any{"store_links": pricedLink})

	for _, it := range []itemView{nulled, absent} {
		require.Equal(t, int64(2500), *it.PriceMinor, it.Title)
		require.Equal(t, "EUR", *it.Currency, it.Title)
	}
}

func TestAPriceTheUserClearsStaysCleared(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": pricedLink})
	require.Equal(t, int64(2500), *it.PriceMinor)

	cleared := decode[itemView](t, e.patchItem(token, it.ID, map[string]any{"price_minor": nil}, nil))
	require.Nil(t, cleared.PriceMinor)

	// Neither a new store link with a price nor a typed price brings it back.
	added := e.postStoreLink(token, it.ID, "https://b.example/k")
	require.Equal(t, http.StatusCreated, added.StatusCode)
	require.Equal(t, int64(4999), *decode[storeLinkView](t, added).PriceMinor)
	require.Nil(t, decode[itemView](t, e.getItem(token, it.ID)).PriceMinor)
	typed := e.patchStoreLink(token, it.ID, it.StoreLinks[0].ID, map[string]any{"price_minor": 1000})
	require.Equal(t, http.StatusOK, typed.StatusCode)
	require.Nil(t, decode[itemView](t, e.getItem(token, it.ID)).PriceMinor)

	// The user can still set a price of their own.
	set := decode[itemView](t, e.patchItem(token, it.ID, map[string]any{"price_minor": 900}, nil))
	require.Equal(t, int64(900), *set.PriceMinor)
}

func TestClearingAPriceTheItemNeverHadClearsNothing(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	e.patchItem(token, it.ID, map[string]any{"price_minor": nil}, nil)
	require.Equal(t, http.StatusCreated, e.postStoreLink(token, it.ID, "https://a.example/k").StatusCode)

	require.Equal(t, int64(4999), *decode[itemView](t, e.getItem(token, it.ID)).PriceMinor, "it had no price to clear")
}

func TestAStoreLinkAddedToAnItemWithoutAPriceGivesItItsFirstPriceOnce(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		if url == "https://nopr.example/k" {
			return product(url, extract.Product{Title: "Kettle"}), nil
		}
		return anyProduct(url)
	}}
	e := newEnv(t, withSearch(shop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	require.Equal(t, http.StatusCreated, e.postStoreLink(token, it.ID, "https://nopr.example/k").StatusCode)
	require.Nil(t, decode[itemView](t, e.getItem(token, it.ID)).PriceMinor, "a link with no price sets nothing")

	require.Equal(t, http.StatusCreated, e.postStoreLink(token, it.ID, "https://a.example/k").StatusCode)
	got := decode[itemView](t, e.getItem(token, it.ID))
	require.Equal(t, int64(4999), *got.PriceMinor)
	require.Equal(t, "USD", *got.Currency)

	// Once: a later link, or a typed price, doesn't change it.
	e.patchStoreLink(token, it.ID, got.StoreLinks[0].ID, map[string]any{"price_minor": 100})
	require.Equal(t, int64(4999), *decode[itemView](t, e.getItem(token, it.ID)).PriceMinor)
}
