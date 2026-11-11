package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/llm"
	"jsclayton.dev/wishbone/internal/extract/pipeline"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/money"
)

// Searcher reads a link or a photo into suggested item details, and keeps the
// hourly limit. [pipeline.Service] is one.
type Searcher interface {
	// Extract reads a link. A cache hit costs no part of the hourly limit.
	Extract(ctx context.Context, userID int64, rawURL string, opts ...pipeline.Option) (pipeline.Result, error)
	// PhotoLimitReached says whether the user is at the hourly limit, without
	// counting an attempt. A request that would cost a decode answers first.
	PhotoLimitReached(userID int64) bool
	// GuessPhoto asks the LLM about a photo (as JPEG). With no LLM set up it
	// guesses nothing and counts nothing.
	GuessPhoto(ctx context.Context, userID int64, jpeg []byte) (llm.PhotoGuess, error)
}

const (
	// searchBodyLimit is the number of bytes at which huma stops reading a
	// search form and answers 413. It leaves room for the form around a photo of
	// [imagestore.MaxBytes], and it is one byte past that, so a form of exactly
	// that size still passes.
	searchBodyLimit = imagestore.MaxBytes + 1<<20 + 1
	// searchTimeout bounds one link. The pipeline's stages can add up to about
	// 140 s, but a user won't wait that long, and a stage that hits the deadline
	// counts as a failure, so the form still opens with what the search has.
	searchTimeout = 60 * time.Second
	// photoTimeout bounds one guess at a photo.
	photoTimeout = 30 * time.Second
	// maxSearchImages is the most image addresses a result carries.
	maxSearchImages = 5
)

// SearchStoreLink is the store link in a search result: the fields of an item's
// store link that a client sends on with `POST /lists/{id}/items`.
type SearchStoreLink struct {
	URL        string  `json:"url" doc:"The normalized address, after redirects."`
	Store      string  `json:"store" example:"shop.example" doc:"The host of the address, with no www. and no trailing dot."`
	PriceMinor *int64  `json:"price_minor" nullable:"true" doc:"The page's price in the currency's minor unit, or null."`
	Currency   *string `json:"currency" nullable:"true" doc:"An ISO 4217 code. It is null exactly when there is no price."`
	ImageURL   *string `json:"image_url" nullable:"true" doc:"The page's best image, or null."`
}

// SearchOption is one choice a page offers, such as a size.
type SearchOption struct {
	Name     string   `json:"name" example:"Size"`
	Values   []string `json:"values" nullable:"false" doc:"What the page lists, at most 30. Empty when the page names the option and lists no values."`
	Selected *string  `json:"selected" nullable:"true" doc:"One of values: the one the page or the address picks, or null."`
}

// SearchResult is one suggestion. Its fields have the names of the
// `POST /lists/{id}/items` body, so a client sends it on with the user's changes.
type SearchResult struct {
	Title         *string           `json:"title" nullable:"true" doc:"The title, or null. For free text, the text as typed, cut to the longest title."`
	Notes         *string           `json:"notes" nullable:"true" doc:"For free text, the text as typed, up to the longest notes. Otherwise null."`
	NotesCut      bool              `json:"notes_cut" doc:"True when the free text was longer than the longest notes."`
	PriceMinor    *int64            `json:"price_minor" nullable:"true" doc:"The item price, or null. A photo gives one only when it shows a price. For a link, the page's price is on the store link."`
	TopPriceMinor *int64            `json:"top_price_minor" nullable:"true" doc:"Always null today."`
	Currency      *string           `json:"currency" nullable:"true" doc:"An ISO 4217 code. It is null exactly when price_minor is."`
	StoreLinks    []SearchStoreLink `json:"store_links" nullable:"false" doc:"For a link, one store link. Otherwise empty."`
	ImageURLs     []string          `json:"image_urls" nullable:"false" doc:"Up to 5 of the page's images, best first, with no repeats. A client saves a picked one with POST /images and {\"url\"}. Otherwise empty."`
	Options       []SearchOption    `json:"options" nullable:"false" doc:"The page's options, at most 4. Otherwise empty."`
	Found         bool              `json:"found" doc:"False when nothing was read from the input: always for free text, and for a link or a photo when no step found anything. The result then has only what the input gave."`
}

// SearchBody is what `POST /search` returns.
type SearchBody struct {
	Results []SearchResult `json:"results" nullable:"false" doc:"Today a search gives exactly one result."`
}

type searchInput struct {
	RawBody     []byte
	contentType string
}

// Resolve reads the type of the body, which carries the form's boundary.
func (i *searchInput) Resolve(ctx huma.Context) []error {
	i.contentType = ctx.Header("Content-Type")
	return nil
}

// urlish reports whether free text is a web address: it has an http or https
// scheme, starts with "www.", or is a host with a path such as
// "shop.example/kettle". The last label of a host is letters, so "1.5/2" and
// "e.g./this" are text.
var urlish = regexp.MustCompile(`(?i)^(https?://\S+|www\.\S+|([a-z0-9-]+\.)+[a-z]{2,}/\S*)$`)

func newSearchResult() SearchResult {
	return SearchResult{StoreLinks: []SearchStoreLink{}, ImageURLs: []string{}, Options: []SearchOption{}}
}

func invalidSearch(detail string) error {
	return Error(http.StatusUnprocessableEntity, "invalid_input", detail)
}

var errPhotoLimit = Error(http.StatusTooManyRequests, "rate_limited", "You have searched a lot of links and photos. Try again later, or fill the item in yourself.")

// searchForm is what a search form holds: exactly one of text or a photo.
type searchForm struct {
	text  string
	photo []byte
	isPic bool
}

// parseSearchForm reads the multipart body. It answers 422 invalid_input for
// anything but exactly one of `text` (a string field) and `image` (a file), and
// 413 image_too_large for a file over the limit of an image.
func parseSearchForm(contentType string, raw []byte) (searchForm, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return searchForm{}, invalidSearch("Send multipart/form-data with exactly one of text or image.")
	}
	var f searchForm
	var seenText bool
	r := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return searchForm{}, invalidSearch("The form couldn't be read.")
		}
		isFile := part.FileName() != ""
		switch name := part.FormName(); {
		case name == "text" && !isFile && !seenText && !f.isPic:
			b, err := io.ReadAll(part)
			if err != nil {
				return searchForm{}, invalidSearch("The form couldn't be read.")
			}
			f.text, seenText = strings.TrimSpace(string(b)), true
		case name == "image" && isFile && !f.isPic && !seenText:
			b, err := io.ReadAll(io.LimitReader(part, imagestore.MaxBytes+1))
			if err != nil {
				return searchForm{}, invalidSearch("The form couldn't be read.")
			}
			if len(b) > imagestore.MaxBytes {
				return searchForm{}, imageTooLarge()
			}
			f.photo, f.isPic = b, true
		default:
			return searchForm{}, invalidSearch("Send exactly one of text (a string) or image (a file).")
		}
	}
	if !seenText && !f.isPic {
		return searchForm{}, invalidSearch("Send exactly one of text (a string) or image (a file).")
	}
	if seenText && f.text == "" {
		return searchForm{}, invalidSearch("Paste a link or type what you want.")
	}
	return f, nil
}

func imageTooLarge() error {
	return Error(http.StatusRequestEntityTooLarge, "image_too_large", fmt.Sprintf("Use an image smaller than %d MB.", imagestore.MaxBytes>>20))
}

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// storeBlocked is the 422 for a link from a blocked store.
func storeBlocked(store, location string) error {
	return Error(http.StatusUnprocessableEntity, "store_blocked",
		"Links from "+store+" can't be added. Ask the admin if you think this is a mistake.",
		&huma.ErrorDetail{Location: location, Message: store + " is a blocked store"})
}

// unblockedAddress is the one way a route that takes a web address from a user
// asks whether it may use it (docs/architecture.md, Blocked stores). It
// normalizes and parses the address first, because [blocklist.Service.Match]
// treats an address it can't parse as not blocked. An address that can't be
// parsed is a 422 invalid_input, never a pass. It returns the normalized
// address, or a 422 store_blocked.
func (s *Server) unblockedAddress(ctx context.Context, rawURL, location string) (string, error) {
	norm, err := extract.NormalizeURL(rawURL)
	if err != nil {
		return "", Error(http.StatusUnprocessableEntity, "invalid_input", "Enter a web address that starts with http or https.",
			&huma.ErrorDetail{Location: location, Message: "is not an http or https address"})
	}
	store, blocked, err := s.opts.Blocked.Match(ctx, norm)
	if err != nil {
		return "", err
	}
	if blocked {
		return "", storeBlocked(store, location)
	}
	return norm, nil
}

// imageAddresses returns the first [maxSearchImages] distinct addresses that a
// download can use, in order. A cached reading can hold one an older reader
// kept, so every address is checked here.
func imageAddresses(images []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, img := range images {
		if len(out) == maxSearchImages {
			break
		}
		norm, err := extract.NormalizeURL(img)
		if err != nil || !strings.Contains(img, "://") || seen[norm] {
			continue
		}
		// The same image can come with and without a tracking parameter or a
		// fragment. Compare the normalized address, but keep the raw one: the
		// shop may need its query to serve the image.
		seen[norm] = true
		out = append(out, img)
	}
	return out
}

// minorPrice reads a decimal price into minor units and the currency it is in,
// with the default currency for a price that names none. ok is false for no
// price, and for text that is not a price.
func minorPrice(price, currency string) (minor int64, code string, ok bool) {
	if strings.TrimSpace(price) == "" {
		return 0, "", false
	}
	code = strings.ToUpper(strings.TrimSpace(currency))
	if len(code) != 3 {
		code = item.DefaultCurrency
	}
	minor, ok = money.Minor(price, code)
	return minor, code, ok
}

// readLink reads a page that [Server.unblockedAddress] has already passed, for a
// route that spends the caller's hourly limit: search, adding a store link, and
// refreshing prices. It returns the reading and the address it ended at. limited
// is the answer when the caller has used the hour. The address after redirects is
// checked again, so a short link can't hide a blocked store, and a cached
// reading goes through the same check. That refusal is found only after the
// read, so it counted. The caller has set up the search pipeline.
func (s *Server) readLink(ctx context.Context, userID int64, norm, location string, limited error, opts ...pipeline.Option) (pipeline.Result, string, error) {
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	res, err := s.opts.Search.Extract(ctx, userID, norm, opts...)
	switch {
	case errors.Is(err, pipeline.ErrRateLimited):
		return res, "", limited
	case errors.Is(err, pipeline.ErrInvalidURL):
		return res, "", invalidSearch("Enter a web address that starts with http or https.")
	case err != nil:
		return res, "", err
	}
	final, err := s.unblockedAddress(ctx, res.URL, location)
	if err != nil {
		return res, "", err
	}
	return res, final, nil
}

func (s *Server) searchText(ctx context.Context, userID int64, text string) (SearchResult, error) {
	out := newSearchResult()
	if !urlish.MatchString(text) {
		title, notes := truncate(text, item.MaxTitleLength), truncate(text, item.MaxNotesLength)
		out.Title, out.Notes = &title, &notes
		out.NotesCut = utf8.RuneCountInString(text) > item.MaxNotesLength
		return out, nil
	}

	// A blocked link costs no read: it is refused before anything is fetched, so
	// it does not count against the hourly limit.
	norm, err := s.unblockedAddress(ctx, text, "body.text")
	if err != nil {
		return out, err
	}
	link := SearchStoreLink{URL: norm, Store: extract.StoreFromURL(norm)}
	if s.opts.Search == nil {
		out.StoreLinks = append(out.StoreLinks, link)
		return out, nil
	}
	res, final, err := s.readLink(ctx, userID, norm, "body.text",
		Error(http.StatusTooManyRequests, "rate_limited", "You have looked up a lot of links and photos. Try again later, or fill the item in yourself."))
	if err != nil {
		return out, err
	}
	link.URL, link.Store = final, extract.StoreFromURL(final)
	out.Found = res.Found()
	if res.Title != "" {
		title := truncate(res.Title, item.MaxTitleLength)
		out.Title = &title
	}
	if minor, code, ok := minorPrice(res.Price, res.Currency); ok {
		link.PriceMinor, link.Currency = &minor, &code
	}
	out.ImageURLs = imageAddresses(res.Images)
	if len(out.ImageURLs) > 0 {
		link.ImageURL = &out.ImageURLs[0]
	}
	out.StoreLinks = append(out.StoreLinks, link)
	for _, o := range res.Options {
		so := SearchOption{Name: o.Name, Values: append([]string{}, o.Values...)}
		if o.Selected != "" {
			selected := o.Selected
			so.Selected = &selected
		}
		out.Options = append(out.Options, so)
	}
	return out, nil
}

func (s *Server) searchPhoto(ctx context.Context, userID int64, photo []byte) (SearchResult, error) {
	out := newSearchResult()
	// Check the limit before the decode, which costs memory and a slot.
	// GuessPhoto counts the attempt, after the file proves to be an image.
	if s.opts.Search != nil && s.opts.Search.PhotoLimitReached(userID) {
		return out, errPhotoLimit
	}
	// Decode and re-encode first: the model gets a small JPEG with none of the
	// original's metadata, and a file that isn't an image costs no call.
	jpeg, err := imagestore.Normalize(bytes.NewReader(photo))
	if err != nil {
		return out, imageProblem(err)
	}
	if s.opts.Search == nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, photoTimeout)
	defer cancel()
	guess, err := s.opts.Search.GuessPhoto(ctx, userID, jpeg)
	switch {
	case errors.Is(err, pipeline.ErrRateLimited):
		return out, errPhotoLimit
	case err != nil:
		// A failed guess is not a failure of the search: the form opens blank.
		s.opts.Logger.Warn("photo guess failed", slog.Any("error", err))
		return out, nil
	}
	if title := truncate(strings.TrimSpace(guess.Title), item.MaxTitleLength); title != "" {
		out.Title, out.Found = &title, true
	}
	if minor, code, ok := minorPrice(guess.Price, guess.Currency); ok && out.Found {
		out.PriceMinor, out.Currency = &minor, &code
	}
	return out, nil
}

func (s *Server) registerSearch(api huma.API) {
	formSchema := &huma.Schema{
		Type: "object",
		Properties: map[string]*huma.Schema{
			"text":  {Type: "string", Description: "A link or free text."},
			"image": {Type: "string", Format: "binary", Description: "A photo: JPEG, PNG, GIF, or WebP, at most 10 MiB."},
		},
	}
	huma.Register(api, huma.Operation{
		OperationID: "search",
		Method:      http.MethodPost,
		Path:        "/search",
		Summary:     "Turn a link, typed words, or a photo into suggested item details",
		Description: "The body is `multipart/form-data` with exactly one of `text` (a link or free text) or `image` (a photo). " +
			"It saves nothing: the client checks the result with the user, then adds the item with `POST /lists/{id}/items`. It always returns `{ results }`, and today one result. " +
			"A link is read: a direct fetch, then Bright Data Web Unlocker, then the LLM, and a reading is kept for 24 hours. A link on the instance's own network is not sent anywhere else. " +
			"Free text is the title and the notes as typed. A photo gets a rough title from the LLM, and a price only when the photo shows one. The server keeps no copy of the photo. " +
			"When nothing is read, `found` is false. A link from a blocked store is a 422 `store_blocked`, before the fetch (it doesn't count against the hourly limit), and after it for a link that redirects there (it counts). " +
			"Each user can search 30 links and photos in any rolling 60 minutes. Over it is a 429 `rate_limited`. Free text, a link read from the cache, a request that fails its checks before any fetch or LLM call, and a photo when the LLM is off don't count.",
		Tags:          []string{"Search"},
		DefaultStatus: http.StatusOK,
		MaxBodyBytes:  searchBodyLimit,
		// The handler reads the form itself: huma would try to parse it as a
		// body it knows.
		SkipValidateBody: true,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
			"multipart/form-data": {Schema: formSchema, Encoding: map[string]*huma.Encoding{
				"image": {ContentType: "image/jpeg, image/png, image/gif, image/webp"},
			}},
		}},
		Errors: []int{http.StatusUnauthorized, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *searchInput) (*struct{ Body SearchBody }, error) {
		caller, _ := CallerFrom(ctx)
		f, err := parseSearchForm(in.contentType, in.RawBody)
		if err != nil {
			return nil, err
		}
		var result SearchResult
		if f.isPic {
			result, err = s.searchPhoto(ctx, caller.User.ID, f.photo)
		} else {
			result, err = s.searchText(ctx, caller.User.ID, f.text)
		}
		if err != nil {
			return nil, err
		}
		return &struct{ Body SearchBody }{SearchBody{Results: []SearchResult{result}}}, nil
	})
}
