package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract"
)

// These tests cover the store link routes (docs/routes.md, Store links):
// adding one reads the price and counts against the hourly limit.

func (e *env) storeLinksPath(itemID string) string { return "/items/" + itemID + "/store-links" }

func (e *env) postStoreLink(token, itemID, url string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: e.storeLinksPath(itemID), body: map[string]any{"url": url}, header: cookie(token)})
}

func (e *env) getStoreLink(token, itemID, linkID string) *http.Response {
	e.t.Helper()
	return e.do(request{path: e.storeLinksPath(itemID) + "/" + linkID, header: cookie(token)})
}

func (e *env) listStoreLinks(token, itemID string) *http.Response {
	e.t.Helper()
	return e.do(request{path: e.storeLinksPath(itemID), header: cookie(token)})
}

func (e *env) patchStoreLink(token, itemID, linkID string, body any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPatch, path: e.storeLinksPath(itemID) + "/" + linkID, body: body, header: cookie(token)})
}

func (e *env) deleteStoreLink(token, itemID, linkID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodDelete, path: e.storeLinksPath(itemID) + "/" + linkID, header: cookie(token)})
}

type storeLinkList struct {
	Data       []storeLinkView `json:"data"`
	NextCursor *string         `json:"next_cursor"`
	TotalCount int             `json:"total_count"`
}

func stores(links []storeLinkView) []string {
	out := []string{}
	for _, l := range links {
		out = append(out, l.Store)
	}
	return out
}

// shopWithPrices answers a few addresses with a product page.
func shopWithPrices() *fakeShop {
	return &fakeShop{pages: func(url string) (extract.Result, error) {
		switch url {
		case "https://a.example/k":
			return product(url, extract.Product{Title: "Kettle", Price: "30", Currency: "EUR", Images: []string{"https://cdn.a.example/k.jpg"}}), nil
		case "https://b.example/k":
			return product(url, extract.Product{Title: "Kettle", Price: "20", Currency: "EUR"}), nil
		case "https://nothing.example/k":
			return product(url, extract.Product{}), nil
		}
		return anyProduct(url)
	}}
}

func TestAddingAStoreLinkReadsThePriceAndTheStoresPicture(t *testing.T) {
	shop := shopWithPrices()
	e := newEnv(t, withSearch(shop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	res := e.postStoreLink(token, it.ID, "https://a.example/k?utm_source=mail#top")
	require.Equal(t, http.StatusCreated, res.StatusCode)
	link := decode[storeLinkView](t, res)

	require.Equal(t, "/api/v2/items/"+it.ID+"/store-links/"+link.ID, res.Header.Get("Location"))
	require.NotEmpty(t, res.Header.Get("ETag"))
	require.Equal(t, "https://a.example/k", link.URL, "the address after normalizing and redirects")
	require.Equal(t, "a.example", link.Store)
	require.Equal(t, int64(3000), *link.PriceMinor)
	require.Equal(t, "EUR", *link.Currency)
	require.Equal(t, e.clock.now().UTC().Format(time.RFC3339), *link.CapturedAt)
	require.Equal(t, "https://cdn.a.example/k.jpg", *link.ImageURL)
	require.True(t, link.CanEdit)
	require.True(t, link.CanDelete)
	require.Equal(t, []string{"a.example"}, stores(decode[itemView](t, e.getItem(token, it.ID)).StoreLinks))
}

func TestAStoreLinkWhosePageHasNoPriceIsAddedWithoutOne(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	res := e.postStoreLink(token, it.ID, "https://nothing.example/k")
	require.Equal(t, http.StatusCreated, res.StatusCode)
	link := decode[storeLinkView](t, res)
	require.Nil(t, link.PriceMinor)
	require.Nil(t, link.Currency)
	require.Nil(t, link.CapturedAt)
	require.Equal(t, "https://nothing.example/k", link.URL)
}

func TestAStoreLinkIsAddedWithNoPriceWhenNoPipelineIsSetUp(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	res := e.postStoreLink(token, it.ID, "a.example/k")
	require.Equal(t, http.StatusCreated, res.StatusCode)
	link := decode[storeLinkView](t, res)
	require.Equal(t, "https://a.example/k", link.URL)
	require.Nil(t, link.PriceMinor)
}

func TestAddingAStoreLinkCountsAgainstTheHourlyLimitUnlessItIsInTheCache(t *testing.T) {
	shop := shopWithPrices()
	e := newEnv(t, withSearch(shop, nil))
	adminID, admin := e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	// 30 reads of Grace's use up her hour. A read from the cache is free.
	for i := range 30 {
		require.Equal(t, http.StatusOK, e.search(token, fmt.Sprintf("https://shop.example/%d", i)).StatusCode)
	}
	over := e.postStoreLink(token, it.ID, "https://a.example/k")
	require.Equal(t, http.StatusTooManyRequests, over.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, over).Code)
	require.Equal(t, 30, shop.fetches(), "the refused link was not fetched")
	require.Empty(t, decode[itemView](t, e.getItem(token, it.ID)).StoreLinks, "nothing was added")

	require.Equal(t, http.StatusCreated, e.postStoreLink(token, it.ID, "https://shop.example/3").StatusCode, "a cache hit costs nothing")
	// The limit is the user's own.
	other := e.addItem(admin, e.primaryListFor(admin, adminID), "Mug")
	require.Equal(t, http.StatusCreated, e.postStoreLink(admin, other.ID, "https://a.example/k").StatusCode)
}

func TestAStoreLinkIsRefusedFromABlockedStoreAndBeyondTen(t *testing.T) {
	shop := shopWithPrices()
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	res := e.postStoreLink(token, it.ID, "https://www.temu.com/p")
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "store_blocked", readProblem(t, res).Code)
	require.Zero(t, shop.fetches(), "a blocked store is refused before the fetch")

	res = e.postStoreLink(token, it.ID, "ftp://a.example/k")
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)

	for i := range 10 {
		require.Equal(t, http.StatusCreated, e.postStoreLink(token, it.ID, fmt.Sprintf("https://s%d.example/k", i)).StatusCode)
	}
	res = e.postStoreLink(token, it.ID, "https://s10.example/k")
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "too_many_store_links", readProblem(t, res).Code)
}

func TestAStoreLinkThatRedirectsToABlockedStoreIsRefusedAfterTheFetch(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		return product("https://temu.com/p", extract.Product{Title: "Gadget", Price: "5"}), nil
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")
	token, listID := e.grace()
	it := e.addItem(token, listID, "Gadget")

	res := e.postStoreLink(token, it.ID, "https://short.example/x")
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "store_blocked", readProblem(t, res).Code)
	require.Equal(t, 1, shop.fetches(), "it was found only after the fetch")
	require.Empty(t, decode[itemView](t, e.getItem(token, it.ID)).StoreLinks)
}

func TestAStoreLinkIsAddedByTheUsersWhoCanEditTheListOnly(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	f := e.family()
	it := e.addItem(f.mom, f.momList, "Kettle")

	// Sue sees the item and can't change it. Hedy can't see it at all.
	res := e.postStoreLink(f.sue, it.ID, "https://a.example/k")
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	res = e.postStoreLink(f.hedy, it.ID, "https://a.example/k")
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	res = e.postStoreLink(f.mom, "999", "https://a.example/k")
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{method: http.MethodPost, path: e.storeLinksPath(it.ID), body: map[string]any{"url": "https://a.example/k"}}).StatusCode)

	// Dad is a recipient of "Our house" with Mom: either adds to an item there.
	shared := e.addItem(f.mom, f.house, "Grill")
	require.Equal(t, http.StatusCreated, e.postStoreLink(f.dad, shared.ID, "https://a.example/k").StatusCode)
}

func TestStoreLinksAreListedAndReadInTheOrderAddedForAnEditorAndByPriceForOthers(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	f := e.family()
	it := e.addItem(f.mom, f.momList, "Kettle")
	a := decode[storeLinkView](t, e.postStoreLink(f.mom, it.ID, "https://a.example/k"))
	b := decode[storeLinkView](t, e.postStoreLink(f.mom, it.ID, "https://b.example/k"))
	e.postStoreLink(f.mom, it.ID, "https://nothing.example/k")

	asMom := decode[storeLinkList](t, e.listStoreLinks(f.mom, it.ID))
	require.Equal(t, []string{"a.example", "b.example", "nothing.example"}, stores(asMom.Data))
	require.Equal(t, 3, asMom.TotalCount)
	require.Nil(t, asMom.NextCursor)
	asSue := decode[storeLinkList](t, e.listStoreLinks(f.sue, it.ID))
	require.Equal(t, []string{"b.example", "a.example", "nothing.example"}, stores(asSue.Data), "the lowest price first, no price last")
	require.False(t, asSue.Data[0].CanEdit)
	require.False(t, asSue.Data[0].CanDelete)
	require.Equal(t, http.StatusNotFound, e.listStoreLinks(f.hedy, it.ID).StatusCode)

	one := e.getStoreLink(f.sue, it.ID, a.ID)
	require.Equal(t, http.StatusOK, one.StatusCode)
	require.NotEmpty(t, one.Header.Get("ETag"))
	require.Equal(t, a.URL, decode[storeLinkView](t, one).URL)
	require.Equal(t, http.StatusNotFound, e.getStoreLink(f.hedy, it.ID, a.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getStoreLink(f.sue, it.ID, "999").StatusCode)
	require.Equal(t, http.StatusNotFound, e.getStoreLink(f.sue, it.ID, "abc").StatusCode)
	// A link is read under its own item only.
	other := e.addItem(f.mom, f.momList, "Mug")
	require.Equal(t, http.StatusNotFound, e.getStoreLink(f.mom, other.ID, b.ID).StatusCode)
}

func TestAnEditorSetsChangesAndClearsAStoreLinksPrice(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"price_minor": 1000})
	link := decode[storeLinkView](t, e.postStoreLink(token, it.ID, "https://nothing.example/k"))
	require.Nil(t, link.CapturedAt)

	e.clock.advance(time.Hour)
	res := e.patchStoreLink(token, it.ID, link.ID, map[string]any{"price_minor": 8950, "currency": "GBP"})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotEmpty(t, res.Header.Get("ETag"))
	got := decode[storeLinkView](t, res)
	require.Equal(t, int64(8950), *got.PriceMinor)
	require.Equal(t, "GBP", *got.Currency)
	require.Equal(t, e.clock.now().UTC().Format(time.RFC3339), *got.CapturedAt, "captured at the edit time")
	require.Equal(t, int64(1000), *decode[itemView](t, e.getItem(token, it.ID)).PriceMinor, "the item keeps its own price")

	// A price with no currency is in USD, and a change of the price alone keeps the currency.
	got = decode[storeLinkView](t, e.patchStoreLink(token, it.ID, link.ID, map[string]any{"price_minor": 9000}))
	require.Equal(t, "GBP", *got.Currency)

	e.clock.advance(time.Hour)
	cleared := decode[storeLinkView](t, e.patchStoreLink(token, it.ID, link.ID, map[string]any{"price_minor": nil}))
	require.Nil(t, cleared.PriceMinor)
	require.Nil(t, cleared.Currency)
	require.Equal(t, e.clock.now().UTC().Format(time.RFC3339), *cleared.CapturedAt, "clearing a price is captured at the edit time")

	// A save that changes nothing on a link with no price captures nothing.
	again := decode[storeLinkView](t, e.patchStoreLink(token, it.ID, link.ID, map[string]any{"price_minor": nil}))
	require.Equal(t, *cleared.CapturedAt, *again.CapturedAt)
}

func TestAStoreLinkPriceHasToBeValid(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	link := decode[storeLinkView](t, e.postStoreLink(token, it.ID, "https://a.example/k"))

	for name, body := range map[string]map[string]any{
		"negative":           {"price_minor": -1},
		"bad currency":       {"currency": "euro"},
		"currency and clear": {"price_minor": nil, "currency": "EUR"},
		"url":                {"url": "https://b.example/k"},
		"id":                 {"id": "7"},
	} {
		res := e.patchStoreLink(token, it.ID, link.ID, body)
		require.GreaterOrEqual(t, res.StatusCode, 400, name)
		require.Less(t, res.StatusCode, 500, name)
	}
	require.Equal(t, int64(3000), *decode[storeLinkView](t, e.getStoreLink(token, it.ID, link.ID)).PriceMinor, "nothing changed")
}

func TestOnlyAnEditorChangesOrDeletesAStoreLink(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	f := e.family()
	it := e.addItem(f.mom, f.momList, "Kettle")
	link := decode[storeLinkView](t, e.postStoreLink(f.mom, it.ID, "https://a.example/k"))

	require.Equal(t, http.StatusForbidden, e.patchStoreLink(f.sue, it.ID, link.ID, map[string]any{"price_minor": 1}).StatusCode)
	require.Equal(t, http.StatusForbidden, e.deleteStoreLink(f.sue, it.ID, link.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchStoreLink(f.hedy, it.ID, link.ID, map[string]any{"price_minor": 1}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.deleteStoreLink(f.hedy, it.ID, link.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchStoreLink(f.mom, it.ID, "999", map[string]any{"price_minor": 1}).StatusCode)

	require.Equal(t, http.StatusNoContent, e.deleteStoreLink(f.mom, it.ID, link.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getStoreLink(f.mom, it.ID, link.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.deleteStoreLink(f.mom, it.ID, link.ID).StatusCode)
	require.Empty(t, decode[itemView](t, e.getItem(f.mom, it.ID)).StoreLinks)
	// The item stays, with the price it took.
	require.Equal(t, int64(3000), *decode[itemView](t, e.getItem(f.mom, it.ID)).PriceMinor)
}

func TestACanRefreshPricesFieldFollowsTheEditRightAndTheStoreLinks(t *testing.T) {
	e := newEnv(t, withSearch(shopWithPrices(), nil))
	f := e.family()
	it := e.addItem(f.mom, f.momList, "Kettle")
	require.False(t, decode[itemView](t, e.getItem(f.mom, it.ID)).CanRefreshPrices, "no store link")

	e.postStoreLink(f.mom, it.ID, "https://a.example/k")
	require.True(t, decode[itemView](t, e.getItem(f.mom, it.ID)).CanRefreshPrices)
	require.False(t, decode[itemView](t, e.getItem(f.sue, it.ID)).CanRefreshPrices)
}
