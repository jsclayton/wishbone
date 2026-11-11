package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests cover the store links and the images that a new item takes
// (docs/spec.md, Items, Store links and Images on an item).

type storeLinkView struct {
	ID         string  `json:"id"`
	URL        string  `json:"url"`
	Store      string  `json:"store"`
	PriceMinor *int64  `json:"price_minor"`
	Currency   *string `json:"currency"`
	CapturedAt *string `json:"captured_at"`
	ImageURL   *string `json:"image_url"`
	CreatedAt  string  `json:"created_at"`
	CanEdit    bool    `json:"can_edit"`
	CanDelete  bool    `json:"can_delete"`
}

type itemImageView struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func links(urls ...string) []map[string]any {
	out := make([]map[string]any, len(urls))
	for i, u := range urls {
		out[i] = map[string]any{"url": u}
	}
	return out
}

func imageIDs(views ...imageView) []string {
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = v.ID
	}
	return out
}

func (it itemView) imageIDs() []string {
	out := []string{}
	for _, img := range it.Images {
		out = append(out, img.ID)
	}
	return out
}

func TestAnItemWithoutStoreLinksOrImagesHasEmptyOnes(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	it := e.addItem(token, listID, "Kettle")

	require.NotNil(t, it.StoreLinks)
	require.Empty(t, it.StoreLinks)
	require.NotNil(t, it.Images)
	require.Empty(t, it.Images)
	require.Nil(t, it.PrimaryImage)
	body := readBody(t, e.getItem(token, it.ID))
	require.Contains(t, body, `"store_links":[]`)
	require.Contains(t, body, `"images":[]`)
	require.Contains(t, body, `"primary_image":null`)
}

func TestAnItemIsAddedWithItsStoreLinksInTheOrderGiven(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": []map[string]any{
		{"url": "https://www.shop.example/kettle?utm_source=mail&color=red#top", "price_minor": 4999, "currency": "EUR", "image_url": "https://cdn.shop.example/k.jpg?w=300"},
		{"url": "other.example/k"},
		{"url": "https://third.example/k", "price_minor": 5100},
	}})

	require.Len(t, it.StoreLinks, 3)
	a, b, c := it.StoreLinks[0], it.StoreLinks[1], it.StoreLinks[2]
	require.Equal(t, "https://www.shop.example/kettle?color=red", a.URL, "the address is normalized")
	require.Equal(t, "shop.example", a.Store)
	require.Equal(t, int64(4999), *a.PriceMinor)
	require.Equal(t, "EUR", *a.Currency)
	require.Equal(t, e.clock.now().UTC().Format(time.RFC3339), *a.CapturedAt, "the price was captured when the link was added")
	require.Equal(t, "https://cdn.shop.example/k.jpg?w=300", *a.ImageURL)
	require.Equal(t, e.clock.now().UTC().Format(time.RFC3339), a.CreatedAt)
	require.True(t, a.CanEdit)
	require.True(t, a.CanDelete)
	require.Equal(t, "https://other.example/k", b.URL)
	require.Equal(t, "other.example", b.Store)
	require.Nil(t, b.PriceMinor)
	require.Nil(t, b.Currency)
	require.Nil(t, b.CapturedAt, "no price, nothing captured")
	require.Nil(t, b.ImageURL)
	require.Equal(t, "USD", *c.Currency, "a price with no currency is in USD")
	require.NotEqual(t, a.ID, b.ID)

	// The item reads back the same, in the item and in its list.
	got := decode[itemView](t, e.getItem(token, it.ID))
	require.Equal(t, it.StoreLinks, got.StoreLinks)
	require.Equal(t, it.StoreLinks, decode[itemList](t, e.listItems(token, listID, "")).Data[0].StoreLinks)
}

func TestTheItemTakesThePriceOfItsFirstPricedStoreLinkWhenItHasNone(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	two := []map[string]any{
		{"url": "https://a.example/k"},
		{"url": "https://b.example/k", "price_minor": 2500, "currency": "EUR"},
		{"url": "https://c.example/k", "price_minor": 100},
	}

	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": two})
	require.Equal(t, int64(2500), *it.PriceMinor)
	require.Equal(t, "EUR", *it.Currency)
	require.Nil(t, it.TopPriceMinor)

	// An item that has a price, or a range, keeps it.
	own := e.addItem(token, listID, "Mug", map[string]any{"price_minor": 900, "top_price_minor": 1200, "store_links": two})
	require.Equal(t, int64(900), *own.PriceMinor)
	require.Equal(t, int64(1200), *own.TopPriceMinor)
	require.Equal(t, "USD", *own.Currency)

	none := e.addItem(token, listID, "Plate", map[string]any{"store_links": links("https://a.example/p")})
	require.Nil(t, none.PriceMinor)
	require.Nil(t, none.Currency)
}

func TestAnItemTakesAtMostTenStoreLinks(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	urls := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("https://shop.example/%d", i)
		}
		return out
	}

	ten := e.addItem(token, listID, "Ten", map[string]any{"store_links": links(urls(10)...)})
	require.Len(t, ten.StoreLinks, 10)

	res := e.postItem(token, listID, map[string]any{"title": "Eleven", "store_links": links(urls(11)...)})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "too_many_store_links", readProblem(t, res).Code)
	require.Equal(t, []string{"Ten"}, e.order(token, listID), "the refused item was not added")
}

func TestAStoreLinkFromABlockedStoreRefusesTheWholeItem(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")
	token, listID := e.grace()

	for _, blocked := range []string{"https://temu.com/p/1", "https://M.TEMU.COM./p/1?utm_source=x", "www.temu.com/p/1", "HTTP://ｔｅｍｕ.com:80/p/1"} {
		res := e.postItem(token, listID, map[string]any{"title": "Gadget", "store_links": links("https://shop.example/g", blocked)})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, blocked)
		p := readProblem(t, res)
		require.Equal(t, "store_blocked", p.Code, blocked)
		require.Contains(t, p.Detail, "temu.com")
	}
	require.Empty(t, e.order(token, listID))

	// A store that is not blocked, with a name that contains the blocked one, is fine.
	e.addItem(token, listID, "Fine", map[string]any{"store_links": links("https://notemu.com/p", "https://temu.com.shop.example/p")})
}

func TestAStoreLinkThatIsNotAWebAddressIsRefusedAndNeverPassedAsNotBlocked(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")
	token, listID := e.grace()

	for _, bad := range []string{"", "   ", "not a url", "ftp://temu.com/p", "javascript:alert(1)", "http://[::1"} {
		res := e.postItem(token, listID, map[string]any{"title": "Gadget", "store_links": links(bad)})
		require.Contains(t, []int{http.StatusUnprocessableEntity}, res.StatusCode, bad)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, bad)
	}
	require.Empty(t, e.order(token, listID))

	for name, link := range map[string]map[string]any{
		"a negative price":     {"url": "https://a.example/p", "price_minor": -1},
		"a currency, no price": {"url": "https://a.example/p", "currency": "EUR"},
		"a bad currency":       {"url": "https://a.example/p", "price_minor": 5, "currency": "euro"},
	} {
		res := e.postItem(token, listID, map[string]any{"title": "Gadget", "store_links": []map[string]any{link}})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, name)
	}
}

func TestAStoreLinkKeepsOnlyAnImageAddressThatIsAWebAddress(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	it := e.addItem(token, listID, "Kettle", map[string]any{"store_links": []map[string]any{
		{"url": "https://a.example/k", "image_url": "javascript:alert(1)"},
		{"url": "https://b.example/k", "image_url": "not an address"},
	}})

	require.Nil(t, it.StoreLinks[0].ImageURL, "a picture is best effort: one that can't be used is dropped")
	require.Nil(t, it.StoreLinks[1].ImageURL)
}

func TestAnItemTakesUploadedImagesAndTheFirstIsThePrimaryImage(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b, c := e.uploaded(token), e.uploaded(token), e.uploaded(token)

	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(b, a, c)})

	require.Equal(t, []string{b.ID, a.ID, c.ID}, it.imageIDs(), "the order given")
	require.Equal(t, itemImageView{ID: b.ID, URL: "/api/v2/images/" + b.ID, Width: b.Width, Height: b.Height}, *it.PrimaryImage)
	require.Equal(t, it.PrimaryImage.ID, it.Images[0].ID)
	got := decode[itemView](t, e.getItem(token, it.ID))
	require.Equal(t, it.Images, got.Images)
	require.Equal(t, it.PrimaryImage, got.PrimaryImage)

	picked := e.addItem(token, listID, "Mug", map[string]any{"image_ids": imageIDs(e.uploaded(token), e.uploaded(token)), "primary_image_id": nil})
	require.Equal(t, picked.Images[0].ID, picked.PrimaryImage.ID)
}

func TestThePrimaryImageIsTheOneNamedAndTheOrderStaysAsGiven(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b, c := e.uploaded(token), e.uploaded(token), e.uploaded(token)

	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a, b, c), "primary_image_id": c.ID})

	require.Equal(t, []string{a.ID, b.ID, c.ID}, it.imageIDs(), "a change of the primary image doesn't change the order")
	require.Equal(t, c.ID, it.PrimaryImage.ID)
}

func TestThePrimaryImageMustBeOneOfTheItemsImages(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b := e.uploaded(token), e.uploaded(token)

	res := e.postItem(token, listID, map[string]any{"title": "Kettle", "image_ids": imageIDs(a), "primary_image_id": b.ID})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
	res = e.postItem(token, listID, map[string]any{"title": "Kettle", "primary_image_id": a.ID})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code, "a primary image with no images")
	require.Empty(t, e.order(token, listID))
}

func TestAnItemTakesAtMostFiveImagesAndEachOnlyOnce(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	var imgs []imageView
	for range 6 {
		imgs = append(imgs, e.uploaded(token))
	}

	five := e.addItem(token, listID, "Five", map[string]any{"image_ids": imageIDs(imgs[:5]...)})
	require.Len(t, five.Images, 5)

	res := e.postItem(token, listID, map[string]any{"title": "Six", "image_ids": imageIDs(imgs...)})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "too_many_images", readProblem(t, res).Code)

	res = e.postItem(token, listID, map[string]any{"title": "Twice", "image_ids": []string{imgs[5].ID, imgs[5].ID}})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
	require.Equal(t, []string{"Five"}, e.order(token, listID))
}

func TestAnImageThatAnotherResourceHoldsIsA409AndOneTheCallerCantSeeIsA422(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	_, hedy := e.otherUser("Hedy")
	held := e.uploaded(token)
	e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(held)})

	res := e.postItem(token, listID, map[string]any{"title": "Mug", "image_ids": imageIDs(held)})
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "image_attached", readProblem(t, res).Code)

	// Hedy's upload is hers alone: Grace can't see it.
	theirs := e.uploaded(hedy)
	res = e.postItem(token, listID, map[string]any{"title": "Mug", "image_ids": imageIDs(theirs)})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "reference_not_found", readProblem(t, res).Code)
	for _, id := range []string{"999", "abc"} {
		res = e.postItem(token, listID, map[string]any{"title": "Mug", "image_ids": []string{id}})
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, id)
		require.Equal(t, "reference_not_found", readProblem(t, res).Code, id)
	}
	require.Equal(t, []string{"Kettle"}, e.order(token, listID))
}

func TestARefusedItemLeavesItsImagesFreeToUseAgain(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")
	token, listID := e.grace()
	img := e.uploaded(token)

	res := e.postItem(token, listID, map[string]any{"title": "Gadget", "image_ids": imageIDs(img), "store_links": links("https://temu.com/p")})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)

	it := e.addItem(token, listID, "Gadget", map[string]any{"image_ids": imageIDs(img)})
	require.Equal(t, img.ID, it.PrimaryImage.ID)
}

func TestAnItemsImagesFollowTheVisibilityOfItsList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	img := e.uploaded(f.mom)
	e.addItem(f.mom, f.momList, "Kettle", map[string]any{"image_ids": imageIDs(img)})

	// Mom uploaded it. Sue sees Mom's list, so she sees the image. Hedy doesn't.
	require.Equal(t, http.StatusOK, e.getImage(f.mom, img.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.hedy, img.ID).StatusCode)
	// Not even the admin, whose access does not reach the list.
	require.Equal(t, http.StatusNotFound, e.getImage(f.admin, img.ID).StatusCode)
}

func TestDeletingAnItemFreesItsImagesForTheSweep(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	a, b := e.uploaded(token), e.uploaded(token)
	it := e.addItem(token, listID, "Kettle", map[string]any{"image_ids": imageIDs(a, b)})
	rowA, rowB := e.row(a.ID), e.row(b.ID)
	require.Zero(t, e.sweep(), "held images are not swept")

	require.Equal(t, http.StatusNoContent, e.deleteItem(token, it.ID, nil).StatusCode)

	require.Equal(t, 2, e.sweep())
	require.False(t, e.hasFile(rowA))
	require.False(t, e.hasFile(rowB))
	require.Zero(t, e.imageRows())
}

func TestAnItemsStoreLinksAreInTheOrderAddedForAnEditorAndByPriceForEveryoneElse(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	it := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"store_links": []map[string]any{
		{"url": "https://a.example/k", "price_minor": 3000},
		{"url": "https://b.example/k"},
		{"url": "https://c.example/k", "price_minor": 2000},
		{"url": "https://d.example/k", "price_minor": 2500},
	}})
	stores := func(v itemView) []string {
		out := []string{}
		for _, l := range v.StoreLinks {
			out = append(out, l.Store)
		}
		return out
	}

	require.Equal(t, []string{"a.example", "b.example", "c.example", "d.example"}, stores(it))
	require.Equal(t, []string{"a.example", "b.example", "c.example", "d.example"}, stores(decode[itemView](t, e.getItem(f.mom, it.ID))))
	asSue := decode[itemView](t, e.getItem(f.sue, it.ID))
	require.Equal(t, []string{"c.example", "d.example", "a.example", "b.example"}, stores(asSue), "the lowest price first, no price last")
	require.False(t, asSue.StoreLinks[0].CanEdit)
	require.False(t, asSue.StoreLinks[0].CanDelete)
	require.Equal(t, stores(asSue), stores(decode[itemList](t, e.listItems(f.sue, f.momList, "")).Data[0]))

	// Prices in different currencies can't be compared: the order added stays.
	mixed := e.addItem(f.mom, f.momList, "Mug", map[string]any{"store_links": []map[string]any{
		{"url": "https://a.example/m", "price_minor": 3000, "currency": "USD"},
		{"url": "https://b.example/m", "price_minor": 100, "currency": "EUR"},
	}})
	require.Equal(t, []string{"a.example", "b.example"}, stores(decode[itemView](t, e.getItem(f.sue, mixed.ID))))
}
