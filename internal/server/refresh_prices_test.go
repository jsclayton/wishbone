package server

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract"
)

// These tests cover POST /items/{id}/refresh-prices (docs/routes.md, Items).

func (e *env) refreshPrices(token, itemID string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/items/" + itemID + "/refresh-prices", header: cookie(token)})
}

// pricedShop is a shop whose prices the test changes: an address with no entry
// has a page with no price, and one in gone fails.
type pricedShop struct {
	*fakeShop
	mu     sync.Mutex
	prices map[string]string
	gone   map[string]bool
}

func newPricedShop() *pricedShop {
	p := &pricedShop{prices: map[string]string{}, gone: map[string]bool{}}
	p.fakeShop = &fakeShop{pages: func(url string) (extract.Result, error) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.gone[url] {
			return extract.Result{}, fmt.Errorf("connection refused")
		}
		return product(url, extract.Product{Title: "Kettle", Price: p.prices[url], Currency: "EUR"}), nil
	}}
	return p
}

func (p *pricedShop) set(url, price string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prices[url] = price
}

func (p *pricedShop) fail(url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gone[url] = true
}

func TestARefreshChangesTheStoreLinksPricesAndNeverTheItemPrice(t *testing.T) {
	shop := newPricedShop()
	shop.set("https://a.example/k", "30")
	shop.set("https://b.example/k", "40")
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"price_minor": 1000, "notes": "Red"})
	e.postStoreLink(token, it.ID, "https://a.example/k")
	e.postStoreLink(token, it.ID, "https://b.example/k")
	added := e.clock.now()

	shop.set("https://a.example/k", "25")
	shop.set("https://b.example/k", "45.50")
	e.clock.advance(time.Hour)
	res := e.refreshPrices(token, it.ID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, res.Header.Get("ETag"), e.getItem(token, it.ID).Header.Get("ETag"))
	got := decode[itemView](t, res)
	require.Equal(t, it.ID, got.ID, "it returns the item")
	require.Equal(t, int64(2500), *got.StoreLinks[0].PriceMinor)
	require.Equal(t, int64(4550), *got.StoreLinks[1].PriceMinor)
	require.Equal(t, "EUR", *got.StoreLinks[1].Currency)
	require.Equal(t, e.clock.now().UTC().Format(time.RFC3339), *got.StoreLinks[0].CapturedAt)
	require.NotEqual(t, added.UTC().Format(time.RFC3339), *got.StoreLinks[0].CapturedAt)
	require.Equal(t, int64(1000), *got.PriceMinor, "the item price stays")
	require.Equal(t, "Red", *got.Notes)
	require.Equal(t, "Kettle", got.Title)
}

func TestARefreshNeverGivesAnItemWithoutAPriceThePriceOfAStoreLink(t *testing.T) {
	shop := newPricedShop()
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	e.postStoreLink(token, it.ID, "https://a.example/k")
	require.Nil(t, decode[itemView](t, e.getItem(token, it.ID)).PriceMinor)

	shop.set("https://a.example/k", "30")
	got := decode[itemView](t, e.refreshPrices(token, it.ID))

	require.Equal(t, int64(3000), *got.StoreLinks[0].PriceMinor)
	require.Nil(t, got.PriceMinor, "unlike an add, a refresh doesn't set the item price")
}

func TestARefreshReadsEachPageAgainAndCountsEachReadAgainstTheHourlyLimit(t *testing.T) {
	shop := newPricedShop()
	shop.set("https://a.example/k", "30")
	shop.set("https://b.example/k", "40")
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	e.postStoreLink(token, it.ID, "https://a.example/k") // 1 read
	e.postStoreLink(token, it.ID, "https://b.example/k") // 2 reads
	require.Equal(t, 2, shop.fetches())

	shop.set("https://a.example/k", "20")
	require.Equal(t, http.StatusOK, e.refreshPrices(token, it.ID).StatusCode)
	require.Equal(t, 4, shop.fetches(), "the 24-hour cache is skipped")
	require.Equal(t, int64(2000), *decode[itemView](t, e.getItem(token, it.ID)).StoreLinks[0].PriceMinor)

	// 4 of the 30 are used. 26 searches use the rest.
	for i := range 26 {
		require.Equal(t, http.StatusOK, e.search(token, fmt.Sprintf("https://shop.example/%d", i)).StatusCode, i)
	}
	over := e.refreshPrices(token, it.ID)
	require.Equal(t, http.StatusTooManyRequests, over.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, over).Code)
	require.Equal(t, 30, shop.fetches(), "no link was read over the limit")
	after := decode[itemView](t, e.getItem(token, it.ID))
	require.Equal(t, int64(2000), *after.StoreLinks[0].PriceMinor, "a refusal before the first read changes no price")
	require.Equal(t, int64(4000), *after.StoreLinks[1].PriceMinor)
}

func TestARefreshThatReadSomethingBeforeTheLimitStoppedItIsNotA429(t *testing.T) {
	shop := newPricedShop()
	shop.set("https://b.example/k", "40")
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": links("https://a.example/k", "https://b.example/k")})
	// 29 of the 30 reads are used: the refresh gets one, and that page has no price.
	for i := range 29 {
		require.Equal(t, http.StatusOK, e.search(token, fmt.Sprintf("https://shop.example/%d", i)).StatusCode, i)
	}
	shop.set("https://b.example/k", "10")

	res := e.refreshPrices(token, it.ID)

	require.Equal(t, http.StatusOK, res.StatusCode, "one read was made and counted")
	got := decode[itemView](t, res)
	require.Nil(t, got.StoreLinks[0].PriceMinor, "the first page had no price")
	require.Nil(t, got.StoreLinks[1].PriceMinor, "the second was not reached")
}

func TestARefreshDoesNotSaveALinkThatNowRedirectsToABlockedStore(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		if url == "https://short.example/k" {
			// The link now ends at another store.
			return product("https://temu.com/p", extract.Product{Title: "Gadget", Price: "5", Currency: "USD"}), nil
		}
		return product(url, extract.Product{Title: "Kettle", Price: "20", Currency: "USD"}), nil
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": []map[string]any{
		{"url": "https://short.example/k", "price_minor": 3000}, {"url": "https://b.example/k", "price_minor": 4000},
	}})
	e.blockStore(admin, "temu.com")

	got := decode[itemView](t, e.refreshPrices(token, it.ID))

	require.Equal(t, int64(3000), *got.StoreLinks[0].PriceMinor, "the price from the blocked store is not saved")
	require.Equal(t, int64(2000), *got.StoreLinks[1].PriceMinor, "the other link is refreshed")
}

func TestAnHourlyLimitThatStopsARefreshHalfwayKeepsThePricesAlreadyRead(t *testing.T) {
	shop := newPricedShop()
	for _, h := range []string{"a", "b", "c"} {
		shop.set("https://"+h+".example/k", "30")
	}
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": links("https://a.example/k", "https://b.example/k", "https://c.example/k")})
	// 28 of the 30 reads are used, so the refresh can read two of its three links.
	for i := range 28 {
		require.Equal(t, http.StatusOK, e.search(token, fmt.Sprintf("https://shop.example/%d", i)).StatusCode, i)
	}
	for _, h := range []string{"a", "b", "c"} {
		shop.set("https://"+h+".example/k", "10")
	}

	res := e.refreshPrices(token, it.ID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[itemView](t, res)
	require.Equal(t, int64(1000), *got.StoreLinks[0].PriceMinor)
	require.Equal(t, int64(1000), *got.StoreLinks[1].PriceMinor)
	require.Nil(t, got.StoreLinks[2].PriceMinor, "the third link was not reached")
}

func TestALinkThatCantBeReadOrHasNoPriceKeepsItsPriceAndDate(t *testing.T) {
	shop := newPricedShop()
	shop.set("https://a.example/k", "30")
	shop.set("https://b.example/k", "40")
	shop.set("https://c.example/k", "50")
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	for _, h := range []string{"a", "b", "c"} {
		e.postStoreLink(token, it.ID, "https://"+h+".example/k")
	}
	before := decode[itemView](t, e.getItem(token, it.ID))

	shop.fail("https://a.example/k")
	shop.set("https://b.example/k", "")
	shop.set("https://c.example/k", "55")
	e.clock.advance(time.Hour)
	got := decode[itemView](t, e.refreshPrices(token, it.ID))

	require.Equal(t, before.StoreLinks[0], got.StoreLinks[0], "a read that fails")
	require.Equal(t, before.StoreLinks[1], got.StoreLinks[1], "a page with no price")
	require.Equal(t, int64(5500), *got.StoreLinks[2].PriceMinor)
}

func TestARefreshDoesNotReadALinkInAStoreBlockedSinceItWasAdded(t *testing.T) {
	shop := newPricedShop()
	shop.set("https://a.example/k", "30")
	shop.set("https://b.example/k", "40")
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	_, admin := e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": links("https://a.example/k", "https://b.example/k")})
	e.blockStore(admin, "a.example")
	shop.set("https://a.example/k", "1")
	shop.set("https://b.example/k", "2")

	got := decode[itemView](t, e.refreshPrices(token, it.ID))

	require.Equal(t, 1, shop.fetches(), "only the other store was fetched")
	require.Nil(t, got.StoreLinks[0].PriceMinor)
	require.Equal(t, int64(200), *got.StoreLinks[1].PriceMinor)
}

func TestOnlyAnEditorRefreshesPrices(t *testing.T) {
	shop := newPricedShop()
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	f := e.family()
	it := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"store_links": links("https://a.example/k")})

	require.Equal(t, http.StatusForbidden, e.refreshPrices(f.sue, it.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.refreshPrices(f.hedy, it.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.refreshPrices(f.mom, "999").StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{method: http.MethodPost, path: "/items/" + it.ID + "/refresh-prices"}).StatusCode)
	require.Zero(t, shop.fetches())
}

func TestARefreshOfAnItemWithoutStoreLinksOrWithoutAPipelineChangesNothing(t *testing.T) {
	shop := newPricedShop()
	e := newEnv(t, withSearch(shop.fakeShop, nil))
	e.signUp()
	token, listID := e.grace()
	bare := e.addItem(token, listID, "Kettle")
	require.Equal(t, http.StatusOK, e.refreshPrices(token, bare.ID).StatusCode)
	require.Zero(t, shop.fetches())

	plain := newEnv(t)
	plain.signUp()
	t2, l2 := plain.grace()
	linked := plain.addItem(t2, l2, "Kettle", map[string]any{"store_links": links("https://a.example/k")})
	res := plain.refreshPrices(t2, linked.ID)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Len(t, decode[itemView](t, res).StoreLinks, 1)
}
