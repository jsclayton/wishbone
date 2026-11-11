package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/llm"
	"jsclayton.dev/wishbone/internal/extract/pipeline"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/store"
)

// fakeShop is the page fetcher of the pipeline: it answers from a function and
// counts the fetches.
type fakeShop struct {
	mu    sync.Mutex
	pages func(url string) (extract.Result, error)
	urls  []string
}

func (f *fakeShop) Fetch(_ context.Context, rawURL string) (extract.Result, error) {
	f.mu.Lock()
	f.urls = append(f.urls, rawURL)
	f.mu.Unlock()
	return f.pages(rawURL)
}

func (f *fakeShop) fetches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.urls)
}

// product is a page with a product on it, ending at the address it was asked for.
func product(url string, p extract.Product) extract.Result {
	return extract.Result{Product: p, URL: url, Store: extract.StoreFromURL(url), Status: extract.StatusOK}
}

// anyProduct answers every address with a product page.
func anyProduct(url string) (extract.Result, error) {
	return product(url, extract.Product{Title: "Kettle", Price: "49.99", Currency: "USD"}), nil
}

// fakeEyes is the LLM that reads a photo.
type fakeEyes struct {
	mu    sync.Mutex
	guess llm.PhotoGuess
	err   error
	seen  [][]byte
}

func (f *fakeEyes) GuessPhoto(_ context.Context, jpeg []byte) (llm.PhotoGuess, llm.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, jpeg)
	return f.guess, llm.Usage{}, f.err
}

// withSearch gives the server the real extraction pipeline, on the test clock and
// the test database, with a fake page fetcher and a fake LLM for photos. A nil
// eyes leaves the photo guess out, as no LLM does.
func withSearch(shop *fakeShop, eyes *fakeEyes) func(*Options) {
	return func(o *Options) {
		opts := pipeline.Options{DB: o.Store.(*store.Store).DB(), Direct: shop, Now: o.Now}
		if eyes != nil {
			opts.Photo = eyes
		}
		o.Search = pipeline.New(opts)
	}
}

type searchResult struct {
	Title         *string `json:"title"`
	Notes         *string `json:"notes"`
	NotesCut      bool    `json:"notes_cut"`
	PriceMinor    *int64  `json:"price_minor"`
	TopPriceMinor *int64  `json:"top_price_minor"`
	Currency      *string `json:"currency"`
	StoreLinks    []struct {
		URL        string  `json:"url"`
		Store      string  `json:"store"`
		PriceMinor *int64  `json:"price_minor"`
		Currency   *string `json:"currency"`
		ImageURL   *string `json:"image_url"`
	} `json:"store_links"`
	ImageURLs []string `json:"image_urls"`
	Options   []struct {
		Name     string   `json:"name"`
		Values   []string `json:"values"`
		Selected *string  `json:"selected"`
	} `json:"options"`
	Found bool `json:"found"`
}

type searchView struct {
	Results []searchResult `json:"results"`
}

// form is a multipart form: text fields, and files by field name.
func form(t *testing.T, fields map[string]string, files map[string][]byte) (body []byte, contentType string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	for k, data := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="photo"`, k))
		h.Set("Content-Type", "application/octet-stream")
		part, err := w.CreatePart(h)
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return b.Bytes(), w.FormDataContentType()
}

func (e *env) searchForm(token string, fields map[string]string, files map[string][]byte) *http.Response {
	e.t.Helper()
	body, contentType := form(e.t, fields, files)
	return e.do(request{method: http.MethodPost, path: "/search", raw: body, contentType: contentType, header: cookie(token)})
}

func (e *env) search(token, text string) *http.Response {
	e.t.Helper()
	return e.searchForm(token, map[string]string{"text": text}, nil)
}

func (e *env) searchPhoto(token string, photo []byte) *http.Response {
	e.t.Helper()
	return e.searchForm(token, nil, map[string][]byte{"image": photo})
}

// only returns the one result of a search that gave exactly one.
func only(t *testing.T, res *http.Response) searchResult {
	t.Helper()
	require.Equal(t, http.StatusOK, res.StatusCode)
	got := decode[searchView](t, res)
	require.Len(t, got.Results, 1, "today a search gives exactly one result")
	return got.Results[0]
}

func TestSearchNeedsASignInAndExactlyOneOfTextOrImage(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, &fakeEyes{}))
	_, admin := e.signUp()

	body, contentType := form(t, map[string]string{"text": "a kettle"}, nil)
	res := e.do(request{method: http.MethodPost, path: "/search", raw: body, contentType: contentType})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	for name, tc := range map[string]struct {
		fields map[string]string
		files  map[string][]byte
	}{
		"neither":           {},
		"both":              {map[string]string{"text": "kettle"}, map[string][]byte{"image": pngOf(t, 4, 3)}},
		"blank text":        {map[string]string{"text": "   "}, nil},
		"an unknown field":  {map[string]string{"text": "kettle", "extra": "x"}, nil},
		"text as a file":    {nil, map[string][]byte{"text": []byte("kettle")}},
		"image as a string": {map[string]string{"image": "kettle"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.searchForm(admin, tc.fields, tc.files)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "invalid_input", readProblem(t, res).Code)
		})
	}

	// A body that is not a form at all.
	res = e.do(request{method: http.MethodPost, path: "/search", raw: []byte(`{"text":"kettle"}`), header: cookie(admin)})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}

func TestFreeTextIsTheTitleAndTheNotesAsTypedAndNeverFoundOrCounted(t *testing.T) {
	shop := &fakeShop{pages: anyProduct}
	e := newEnv(t, withSearch(shop, &fakeEyes{}))
	_, admin := e.signUp()

	r := only(t, e.search(admin, "  a book about sharks  "))

	require.NotNil(t, r.Title)
	require.Equal(t, "a book about sharks", *r.Title)
	require.NotNil(t, r.Notes)
	require.Equal(t, "a book about sharks", *r.Notes)
	require.False(t, r.NotesCut)
	require.False(t, r.Found, "the words are what the input gave, not something read")
	require.Nil(t, r.PriceMinor)
	require.Nil(t, r.TopPriceMinor)
	require.Nil(t, r.Currency)
	require.NotNil(t, r.StoreLinks)
	require.Empty(t, r.StoreLinks)
	require.NotNil(t, r.ImageURLs)
	require.Empty(t, r.ImageURLs)
	require.NotNil(t, r.Options)
	require.Empty(t, r.Options)
	require.Zero(t, shop.fetches())

	// Free text is not counted: 40 of them, and a link still reads.
	for range 40 {
		require.Equal(t, http.StatusOK, e.search(admin, "more words").StatusCode)
	}
	require.Equal(t, http.StatusOK, e.search(admin, "https://shop.example/kettle").StatusCode)
}

func TestLongFreeTextIsCutForTheTitleAndTheNotes(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, nil))
	_, admin := e.signUp()

	r := only(t, e.search(admin, strings.Repeat("é", 2500)))

	require.Len(t, []rune(*r.Title), 300, "the longest item title")
	require.Len(t, []rune(*r.Notes), 2000, "the longest notes")
	require.True(t, r.NotesCut)

	r = only(t, e.search(admin, strings.Repeat("x", 2000)))
	require.False(t, r.NotesCut, "text of exactly the longest notes is not cut")
}

func TestALinkIsReadIntoATitleOneStoreLinkImagesAndOptions(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		return product("https://www.shop.example/kettle?color=red", extract.Product{
			Title:    "Copper kettle",
			Price:    "1,299.50",
			Currency: "EUR",
			Images: []string{
				"https://cdn.shop.example/a.jpg", "https://cdn.shop.example/b.jpg", "https://cdn.shop.example/a.jpg#again",
				"/relative.jpg", "https://cdn.shop.example/c.jpg", "https://cdn.shop.example/d.jpg",
				"https://cdn.shop.example/e.jpg", "https://cdn.shop.example/f.jpg",
			},
			Options: []extract.Option{{Name: "Color", Values: []string{"Red", "Blue"}, Selected: "Red"}},
		}), nil
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()

	r := only(t, e.search(admin, "https://shop.example/kettle?utm_source=mail&color=red"))

	require.True(t, r.Found)
	require.Equal(t, "Copper kettle", *r.Title)
	require.Nil(t, r.Notes)
	require.False(t, r.NotesCut)
	require.Nil(t, r.PriceMinor, "the page's price is on the store link")
	require.Nil(t, r.TopPriceMinor)
	require.Nil(t, r.Currency)
	require.Len(t, r.StoreLinks, 1)
	link := r.StoreLinks[0]
	require.Equal(t, "https://www.shop.example/kettle?color=red", link.URL, "the address after redirects")
	require.Equal(t, "shop.example", link.Store)
	require.Equal(t, int64(129950), *link.PriceMinor)
	require.Equal(t, "EUR", *link.Currency)
	require.Equal(t, "https://cdn.shop.example/a.jpg", *link.ImageURL)
	require.Equal(t, []string{
		"https://cdn.shop.example/a.jpg", "https://cdn.shop.example/b.jpg", "https://cdn.shop.example/c.jpg",
		"https://cdn.shop.example/d.jpg", "https://cdn.shop.example/e.jpg",
	}, r.ImageURLs, "up to 5, best first, with no repeats and no relative address")
	require.Len(t, r.Options, 1)
	require.Equal(t, "Color", r.Options[0].Name)
	require.Equal(t, []string{"Red", "Blue"}, r.Options[0].Values)
	require.Equal(t, "Red", *r.Options[0].Selected)
	require.Equal(t, []string{"https://shop.example/kettle?color=red"}, shop.urls, "the fetch got the normalized address")
}

func TestALinkWithAPriceAndNoCurrencyIsInUSDAndOneWithNoPriceHasNone(t *testing.T) {
	prices := map[string]extract.Product{
		"https://shop.example/a": {Title: "A", Price: "10"},
		"https://shop.example/b": {Title: "B"},
	}
	shop := &fakeShop{pages: func(url string) (extract.Result, error) { return product(url, prices[url]), nil }}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()

	a := only(t, e.search(admin, "https://shop.example/a")).StoreLinks[0]
	require.Equal(t, int64(1000), *a.PriceMinor)
	require.Equal(t, "USD", *a.Currency)
	b := only(t, e.search(admin, "https://shop.example/b")).StoreLinks[0]
	require.Nil(t, b.PriceMinor)
	require.Nil(t, b.Currency)
	require.Nil(t, b.ImageURL)
}

func TestALinkThatYieldsNothingIsNotFoundAndKeepsTheLinkAsAStoreLink(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		return extract.Result{URL: url, Store: extract.StoreFromURL(url), Status: extract.StatusNoData}, nil
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()

	r := only(t, e.search(admin, "www.shop.example/nothing"))

	require.False(t, r.Found)
	require.Nil(t, r.Title)
	require.Len(t, r.StoreLinks, 1)
	require.Equal(t, "https://www.shop.example/nothing", r.StoreLinks[0].URL)
	require.Equal(t, "shop.example", r.StoreLinks[0].Store)
	require.Nil(t, r.StoreLinks[0].PriceMinor)
	require.Empty(t, r.ImageURLs)
}

func TestALinkOnTheInstancesOwnNetworkIsNotFoundAndGoesNowhereElse(t *testing.T) {
	shop := &fakeShop{pages: func(string) (extract.Result, error) {
		return extract.Result{}, fmt.Errorf("fetch: %w 10.0.0.5", netguard.ErrPrivateAddress)
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()

	r := only(t, e.search(admin, "http://10.0.0.5/admin"))

	require.False(t, r.Found)
	require.Equal(t, "http://10.0.0.5/admin", r.StoreLinks[0].URL)
	require.Equal(t, 1, shop.fetches())
}

func TestAnAddressThatCantBeParsedIs422AndFetchesNothing(t *testing.T) {
	shop := &fakeShop{pages: anyProduct}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()

	res := e.search(admin, "http://[::1")

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
	require.Zero(t, shop.fetches())
}

func TestALinkReadAgainWithin24HoursComesFromTheCacheAndIsNotCounted(t *testing.T) {
	shop := &fakeShop{pages: anyProduct}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()

	first := only(t, e.search(admin, "https://shop.example/kettle"))
	for range 40 {
		again := only(t, e.search(admin, "https://shop.example/kettle?utm_campaign=x"))
		require.Equal(t, first, again)
	}
	require.Equal(t, 1, shop.fetches())

	// After 24 hours it is read again.
	e.clock.advance(25 * time.Hour)
	only(t, e.search(admin, "https://shop.example/kettle"))
	require.Equal(t, 2, shop.fetches())
}

func TestAPastedLinkFromABlockedStoreIs422BeforeAnyFetchAndIsNotCounted(t *testing.T) {
	shop := &fakeShop{pages: anyProduct}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")

	for _, link := range []string{
		"https://temu.com/p/1", "https://m.temu.com./p/1", "HTTP://WWW.TEMU.COM:80/p/1?utm_source=x", "temu.com/p/1", "https://ｔｅｍｕ.com/p/1",
	} {
		res := e.search(admin, link)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, link)
		p := readProblem(t, res)
		require.Equal(t, "store_blocked", p.Code, link)
		require.Contains(t, p.Detail, "temu.com")
	}
	require.Zero(t, shop.fetches())

	// None of them counted: the hourly limit is still whole.
	for i := range pipeline.MemberLimit {
		require.Equal(t, http.StatusOK, e.search(admin, fmt.Sprintf("https://shop.example/%d", i)).StatusCode)
	}
}

func TestALinkThatRedirectsToABlockedStoreIs422AfterTheFetchAndIsCounted(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		if url == "https://bit.example/x" {
			return product("https://www.temu.com/p/1?utm_source=x", extract.Product{Title: "Gadget"}), nil
		}
		return anyProduct(url)
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	e.blockStore(admin, "temu.com")

	res := e.search(admin, "https://bit.example/x")

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "store_blocked", readProblem(t, res).Code)
	require.Equal(t, 1, shop.fetches(), "it was found only by the fetch")
	for i := range pipeline.MemberLimit - 1 {
		require.Equal(t, http.StatusOK, e.search(admin, fmt.Sprintf("https://shop.example/%d", i)).StatusCode)
	}
	over := e.search(admin, "https://shop.example/over")
	require.Equal(t, http.StatusTooManyRequests, over.StatusCode, "the refused redirect used one of the 30")
}

func TestAShortLinkWhoseReadingEndedAtAStoreBlockedLaterIsRefusedWithNoFetch(t *testing.T) {
	shop := &fakeShop{pages: func(url string) (extract.Result, error) {
		return product("https://www.temu.com/p/1", extract.Product{Title: "Gadget"}), nil
	}}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	only(t, e.search(admin, "https://bit.example/x"))
	e.blockStore(admin, "temu.com")

	res := e.search(admin, "https://bit.example/x")

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "store_blocked", readProblem(t, res).Code)
	require.Equal(t, 1, shop.fetches())
}

func TestEachUserCanSearch30LinksInAnyRollingHourAndThenGetsA429(t *testing.T) {
	shop := &fakeShop{pages: anyProduct}
	e := newEnv(t, withSearch(shop, nil))
	_, admin := e.signUp()
	_, grace := e.otherUser("Grace")

	for i := range 30 {
		require.Equal(t, http.StatusOK, e.search(admin, fmt.Sprintf("https://shop.example/%d", i)).StatusCode, i)
		if i < 29 {
			e.clock.advance(time.Minute)
		}
	}
	over := e.search(admin, "https://shop.example/31")
	require.Equal(t, http.StatusTooManyRequests, over.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, over).Code)
	require.Equal(t, 30, shop.fetches(), "the refused link was not fetched")

	// The limit is the user's own.
	require.Equal(t, http.StatusOK, e.search(grace, "https://shop.example/grace").StatusCode)
	// A link that was read already is still free, and so are words.
	require.Equal(t, http.StatusOK, e.search(admin, "https://shop.example/3").StatusCode)
	require.Equal(t, http.StatusOK, e.search(admin, "some words").StatusCode)

	// The window rolls: the first search was 29 minutes ago, 31 minutes from now it is over an hour.
	e.clock.advance(32 * time.Minute)
	require.Equal(t, http.StatusOK, e.search(admin, "https://shop.example/32").StatusCode)
}

func TestAPhotoGetsATitleAndAPriceOnlyWhenItShowsOneAndIsKeptNowhere(t *testing.T) {
	eyes := &fakeEyes{guess: llm.PhotoGuess{Title: "Blue enamel mug", Price: "12.50", Currency: "EUR"}}
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, eyes))
	_, admin := e.signUp()

	r := only(t, e.searchPhoto(admin, pngOf(t, 2400, 1800)))

	require.True(t, r.Found)
	require.Equal(t, "Blue enamel mug", *r.Title)
	require.Nil(t, r.Notes)
	require.Equal(t, int64(1250), *r.PriceMinor)
	require.Equal(t, "EUR", *r.Currency)
	require.Empty(t, r.StoreLinks)
	require.Empty(t, r.ImageURLs)
	require.Len(t, eyes.seen, 1)
	cfg, err := jpegSize(eyes.seen[0])
	require.NoError(t, err)
	require.Equal(t, [2]int{1200, 900}, cfg, "the model gets the photo as the server shrinks it")
	require.Zero(t, e.imageRows(), "the server keeps no copy of the photo")

	eyes.guess = llm.PhotoGuess{Title: "Red scarf"}
	r = only(t, e.searchPhoto(admin, pngOf(t, 4, 3)))
	require.True(t, r.Found)
	require.Nil(t, r.PriceMinor, "no price unless the photo shows one")
	require.Nil(t, r.Currency)

	eyes.guess = llm.PhotoGuess{Title: "Tag", Price: "34"}
	r = only(t, e.searchPhoto(admin, pngOf(t, 4, 3)))
	require.Equal(t, int64(3400), *r.PriceMinor)
	require.Equal(t, "USD", *r.Currency, "a price with no currency is in USD")
}

func TestAPhotoTheModelCantReadIsNotFoundAndStillCounts(t *testing.T) {
	eyes := &fakeEyes{err: errors.New("model is down")}
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, eyes))
	_, admin := e.signUp()

	r := only(t, e.searchPhoto(admin, pngOf(t, 4, 3)))
	require.False(t, r.Found)
	require.Nil(t, r.Title)
	require.Nil(t, r.PriceMinor)

	for range 29 {
		require.Equal(t, http.StatusOK, e.searchPhoto(admin, pngOf(t, 4, 3)).StatusCode)
	}
	require.Equal(t, http.StatusTooManyRequests, e.searchPhoto(admin, pngOf(t, 4, 3)).StatusCode)
}

func TestWithTheLLMOffAPhotoIsNotFoundAndNotCounted(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, nil))
	_, admin := e.signUp()

	for range 35 {
		r := only(t, e.searchPhoto(admin, pngOf(t, 4, 3)))
		require.False(t, r.Found)
		require.Nil(t, r.Title)
	}
	require.Equal(t, http.StatusOK, e.search(admin, "https://shop.example/kettle").StatusCode)
}

func TestPhotosAndLinksShareTheHourlyLimitAndAPhotoOverItIs429BeforeTheModelIsAsked(t *testing.T) {
	eyes := &fakeEyes{guess: llm.PhotoGuess{Title: "Mug"}}
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, eyes))
	_, admin := e.signUp()
	for i := range 20 {
		require.Equal(t, http.StatusOK, e.search(admin, fmt.Sprintf("https://shop.example/%d", i)).StatusCode)
	}
	for range 10 {
		require.Equal(t, http.StatusOK, e.searchPhoto(admin, pngOf(t, 4, 3)).StatusCode)
	}
	asked := len(eyes.seen)

	res := e.searchPhoto(admin, pngOf(t, 4, 3))

	require.Equal(t, http.StatusTooManyRequests, res.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, res).Code)
	require.Len(t, eyes.seen, asked)
	// A download of an image address is not counted, and not refused.
}

func TestAFileThatIsNotAnImageIs422AndIsNotCounted(t *testing.T) {
	eyes := &fakeEyes{guess: llm.PhotoGuess{Title: "Mug"}}
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, eyes))
	_, admin := e.signUp()

	for range 35 {
		res := e.searchPhoto(admin, []byte("this is not a photo"))
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
		require.Equal(t, "image_invalid", readProblem(t, res).Code)
	}
	require.Empty(t, eyes.seen)
	require.Equal(t, http.StatusOK, e.searchPhoto(admin, pngOf(t, 4, 3)).StatusCode)
}

func TestAPhotoOverTheImageLimitIsA413(t *testing.T) {
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, &fakeEyes{}))
	_, admin := e.signUp()

	// Over the limit of the file, but inside what the server reads of a form.
	res := e.searchPhoto(admin, make([]byte, imagestore.MaxBytes+1))
	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
	require.Equal(t, "image_too_large", readProblem(t, res).Code)

	// Far over: the server stops reading.
	res = e.searchPhoto(admin, make([]byte, imagestore.MaxBytes+(1<<20)))
	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
	require.Equal(t, "image_too_large", readProblem(t, res).Code)

	// A file of exactly the limit is read. It isn't an image, so it is a 422.
	res = e.searchPhoto(admin, make([]byte, imagestore.MaxBytes))
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "image_invalid", readProblem(t, res).Code)
}

func TestADownloadOfAnImageIsNotCountedAgainstTheHourlyLimit(t *testing.T) {
	shop := &fakeShop{pages: anyProduct}
	e := newEnv(t, withSearch(shop, nil), privateImages(t))
	_, admin := e.signUp()
	pics := pictureShop(t)
	for i := range pipeline.MemberLimit {
		require.Equal(t, http.StatusOK, e.search(admin, fmt.Sprintf("https://shop.example/%d", i)).StatusCode)
	}

	for range 3 {
		require.Equal(t, http.StatusCreated, e.downloadImage(admin, pics.URL+"/pic.png").StatusCode)
	}
	require.Equal(t, http.StatusTooManyRequests, e.search(admin, "https://shop.example/over").StatusCode)
}

func TestSearchWithNoPipelineFindsNothingButStillAnswers(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	r := only(t, e.search(admin, "https://shop.example/kettle"))
	require.False(t, r.Found)
	require.Equal(t, "https://shop.example/kettle", r.StoreLinks[0].URL)
	require.False(t, only(t, e.searchPhoto(admin, pngOf(t, 4, 3))).Found)
}

// jpegSize is the width and height of JPEG bytes.
func jpegSize(data []byte) ([2]int, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	return [2]int{cfg.Width, cfg.Height}, err
}
