// Package pipeline runs the link extraction stages in order, with the spec's
// cost guardrails: a 24-hour cache by normalized URL and a per-member sliding
// window limit. It exposes [Service.Extract], which the paste box and the
// price refresh call.
//
// Stage order: direct fetch, then Bright Data, then the LLM. A stage that
// returns an error, a blocked page, or no useful data runs the next one. A
// stage that is not configured (nil) is skipped. Two exceptions end the
// pipeline, so no later stage receives the link: a refused private address
// ([netguard.ErrPrivateAddress]), and a DNS "not found" answer from the direct
// fetch. A DNS timeout or other DNS error is a plain failure.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/llm"
	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/ratelimit"
)

// Stage names, as used in the Result and in metric labels.
const (
	StageDirect     = "direct"
	StageBrightData = "brightdata"
	StageLLM        = "llm"
)

const (
	// CacheTTL is how long an extraction result is reused.
	CacheTTL = 24 * time.Hour
	// MemberLimit is the most extractions, searches, and taps on a search
	// result a member can run, combined, in any Window.
	MemberLimit = 30
	// Window is the sliding window of the member limit.
	Window = 60 * time.Minute
)

var (
	// ErrRateLimited means the member has used all of their limit in the window.
	ErrRateLimited = errors.New("extraction limit reached: try again later")
	// ErrInvalidURL means the input is not an http or https URL.
	ErrInvalidURL = errors.New("invalid URL")
)

// Filler is the LLM stage. [llm.Extractor] implements it.
type Filler interface {
	Fill(ctx context.Context, p extract.Product, page []byte, pageURL string) (extract.Product, llm.Usage, error)
}

// PhotoReader guesses an item from a photo. [llm.Extractor] implements it.
type PhotoReader interface {
	GuessPhoto(ctx context.Context, jpeg []byte) (llm.PhotoGuess, llm.Usage, error)
}

// Options configures a [Service]. Nil stages are skipped.
type Options struct {
	DB         *sqlx.DB
	Direct     extract.Fetcher
	BrightData extract.Fetcher
	LLM        Filler
	Photo      PhotoReader // nil leaves photo guesses out
	Logger     *slog.Logger
	Now        func() time.Time      // defaults to time.Now
	Registerer prometheus.Registerer // metrics go here. Nil registers nothing.
	// BrightDataRequests reports the Bright Data request count, usually
	// [extract.BrightData.Requests]. The metric reads it, so there is one
	// source. Nil leaves the metric out.
	BrightDataRequests func() int64
}

// Result is the pre-filled form data for a pasted URL.
type Result struct {
	extract.Product
	URL   string // normalized URL, after redirects when a stage fetched the page
	Store string
	// Stage is the last stage that added data: "direct", "brightdata", or
	// "llm". It is empty when no stage found anything.
	Stage string
	// Cached is true when the result came from the cache.
	Cached bool
}

// Found reports whether any stage found something to pre-fill. A result that
// is not found still carries the URL, and the form opens with blank fields.
func (r Result) Found() bool { return r.Title != "" || r.Price != "" || len(r.Images) > 0 }

// Service is the extraction pipeline.
type Service struct {
	direct, brightData extract.Fetcher
	llm                Filler
	photo              PhotoReader
	cache              *cache
	limiter            *ratelimit.Limiter
	now                func() time.Time
	log                *slog.Logger
	m                  *metrics
}

// New builds a Service. It panics if a metric is already registered on
// o.Registerer.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		direct:     o.Direct,
		brightData: o.BrightData,
		llm:        o.LLM,
		photo:      o.Photo,
		cache:      &cache{db: o.DB, now: o.Now},
		limiter:    ratelimit.New(MemberLimit, Window, o.Now),
		now:        o.Now,
		log:        o.Logger,
		m:          newMetrics(o.Registerer, o.BrightDataRequests),
	}
}

// Option changes one call of [Service.Extract].
type Option func(*callOptions)

type callOptions struct{ bypassCache bool }

// BypassCache skips the cache read, so the stages run again. The new result
// still replaces the cached one, and the call still counts against the
// member's limit. Price refresh uses it.
func BypassCache() Option { return func(o *callOptions) { o.bypassCache = true } }

// Extract returns pre-filled item details for rawURL on behalf of memberID.
//
// A cache hit returns at once and doesn't count against the member's limit.
// Any other call counts, even if every stage fails. If every stage fails, the
// result has the URL and blank fields, with a nil error. A refused private
// address, or a host name the direct fetch's DNS says does not exist, gives the
// same result. The error is [ErrInvalidURL] or
// [ErrRateLimited] otherwise, or a database error.
//
// The pipeline sets no time limit of its own. The stages' own timeouts add up
// to about 140 seconds in the worst case (direct 20 s, Bright Data 60 s, LLM
// 60 s). The caller should set a deadline on ctx that fits its request, for
// example the reverse proxy's timeout. A stage that hits the deadline counts
// as a failure.
func (s *Service) Extract(ctx context.Context, memberID int64, rawURL string, opts ...Option) (Result, error) {
	var co callOptions
	for _, o := range opts {
		o(&co)
	}
	key, err := extract.NormalizeURL(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}
	if !co.bypassCache {
		if res, ok, err := s.cache.get(ctx, key); err != nil {
			s.log.Warn("extraction cache read failed", slog.Any("error", err))
		} else if ok {
			res.Cached = true
			return res, nil
		}
	}
	if !s.limiter.Allow(fmt.Sprint(memberID)) {
		return Result{}, ErrRateLimited
	}

	res := s.run(ctx, key)
	if res.Found() {
		if err := s.cache.put(ctx, key, res); err != nil {
			s.log.Warn("extraction cache write failed", slog.Any("error", err))
		}
	}
	return res, nil
}

// PhotoLimitReached reports whether memberID is at the limit that
// [Service.GuessPhoto] enforces, without counting an attempt. A caller uses it
// to refuse a request before it does costly work. It is false with no LLM set
// up, because GuessPhoto then costs nothing.
func (s *Service) PhotoLimitReached(memberID int64) bool {
	return s.photo != nil && s.limiter.Full(fmt.Sprint(memberID))
}

// GuessPhoto asks the LLM for a rough title and any visible price from a photo,
// on behalf of memberID. jpeg is the photo as the image store re-encodes it.
// Nothing is cached, because a photo has no URL to key on and nothing is stored.
//
// With no LLM set up, it returns an empty guess and a nil error, and the call
// doesn't count. Any other call counts against the member's limit, which it
// shares with [Service.Extract], even if the model fails. Its tokens add to the
// LLM token metric. The error is [ErrRateLimited] or the model's failure.
func (s *Service) GuessPhoto(ctx context.Context, memberID int64, jpeg []byte) (llm.PhotoGuess, error) {
	if s.photo == nil {
		return llm.PhotoGuess{}, nil
	}
	if !s.limiter.Allow(fmt.Sprint(memberID)) {
		return llm.PhotoGuess{}, ErrRateLimited
	}
	guess, usage, err := s.photo.GuessPhoto(ctx, jpeg)
	s.m.tokens(usage)
	s.m.record(StageLLM, err == nil)
	if err != nil {
		return llm.PhotoGuess{}, err
	}
	return guess, nil
}

// textLen is how much text the LLM would read from a page. It has no cap, so
// two long pages don't tie at the LLM's limit.
func textLen(page []byte) int {
	return len(llm.CleanText(page, math.MaxInt))
}

func (s *Service) run(ctx context.Context, key string) Result {
	res := Result{URL: key, Store: extract.StoreFromURL(key)}

	// last is the page the LLM will read: a fetch that returned a page, even
	// without a product. A later non-OK page replaces it only if it has at
	// least as much text, so a thin challenge page can't hide a better one.
	var last extract.Result
	var haveLast, fetched bool
	for _, st := range []struct {
		name string
		f    extract.Fetcher
	}{{StageDirect, s.direct}, {StageBrightData, s.brightData}} {
		if st.f == nil {
			continue
		}
		r, err := st.f.Fetch(ctx, key)
		switch {
		case errors.Is(err, netguard.ErrPrivateAddress):
			// The link points at the host's own network, so no later stage
			// may see it. The LLM needs a page, and none was fetched.
			s.log.Debug("extraction stopped: private address", slog.String("stage", st.name))
			s.m.record(st.name, false)
			return res
		case st.name == StageDirect && isDNSNotFound(err):
			// The instance's DNS says the host doesn't exist, as for a LAN
			// name. Bright Data would send the link and its path off the
			// network. Only the direct stage's answer counts: it uses the
			// instance's resolver. Bright Data's own failure leaves the LLM
			// a page to read from the direct stage, if there is one.
			s.log.Debug("extraction stopped: host not found", slog.String("stage", st.name))
			s.m.record(st.name, false)
			return res
		case err != nil:
			s.log.Debug("extraction stage failed", slog.String("stage", st.name), slog.Any("error", err))
			s.m.record(st.name, false)
			continue
		case r.Status != extract.StatusOK:
			s.m.record(st.name, false)
			if !haveLast || (len(r.Body) > 0 && textLen(r.Body) >= textLen(last.Body)) {
				last, haveLast = r, true
			}
			continue
		}
		s.m.record(st.name, true)
		res.Product, res.Stage, fetched = r.Product, st.name, true
		if r.URL != "" {
			res.URL, res.Store = r.URL, r.Store
		}
		last, haveLast = r, true
		break
	}

	// A no-data page can still have a <title>, such as "Robot or human?". The
	// LLM never overwrites a title, so hold it back and use it only if the
	// LLM finds nothing better.
	var fallbackTitle string
	if !fetched && haveLast {
		fallbackTitle = last.Title
		if last.URL != "" {
			res.URL, res.Store = last.URL, last.Store
		}
	}

	if s.llm != nil && len(last.Body) > 0 && (!fetched || incomplete(res.Product)) {
		in := res.Product
		filled, usage, err := s.llm.Fill(ctx, in, last.Body, res.URL)
		s.m.tokens(usage)
		switch {
		case err != nil:
			s.log.Debug("extraction stage failed", slog.String("stage", StageLLM), slog.Any("error", err))
			s.m.record(StageLLM, false)
		case !changed(in, filled):
			s.m.record(StageLLM, false)
		default:
			s.m.record(StageLLM, true)
			res.Product, res.Stage = filled, StageLLM
		}
	}
	if res.Title == "" {
		res.Title = fallbackTitle
	}
	return res
}

// isDNSNotFound reports whether err is a DNS "no such host" answer, as opposed
// to a timeout or a server failure.
func isDNSNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func incomplete(p extract.Product) bool { return p.Title == "" || p.Price == "" || len(p.Images) == 0 }

func changed(a, b extract.Product) bool {
	return a.Title != b.Title || a.Price != b.Price || a.Currency != b.Currency || len(a.Images) != len(b.Images)
}
