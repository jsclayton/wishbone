package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/telemetry"
)

// Status tells a later pipeline stage whether it needs to run.
type Status int

const (
	// StatusOK means the page has a product signal: a JSON-LD Product, an
	// og:title or og:image, or a price. A page with only a <title> is NoData,
	// but Result still carries that title.
	StatusOK Status = iota
	// StatusBlocked means the site refused the request (401, 403, 429, 451, 503).
	StatusBlocked
	// StatusNoData means the page loaded but had nothing useful.
	StatusNoData
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusBlocked:
		return "blocked"
	case StatusNoData:
		return "no_data"
	}
	return "unknown"
}

// Result is the outcome of one fetch.
type Result struct {
	Product
	URL        string // normalized URL after redirects
	Store      string // from the domain of URL
	Status     Status
	HTTPStatus int
	// Body is the page HTML the product came from, capped at the fetch's byte
	// limit. It is empty when the page was not read (blocked, non-HTML). The LLM
	// stage reads it.
	Body []byte
}

// NeedsFallback reports whether a later stage (Bright Data, LLM) should run.
func (r Result) NeedsFallback() bool { return r.Status != StatusOK }

// Fetcher fetches a page and extracts item details. Direct is stage 1. Other
// backends implement the same interface.
//
// An error means the fetch itself failed (bad URL, network error, timeout,
// too many redirects). A blocked or empty page is a Result, not an error.
// The pipeline must treat an error from this stage the same as
// [Result.NeedsFallback]: run the next stage. Some sites stall bots instead
// of refusing them. Two errors end the pipeline: [netguard.ErrPrivateAddress],
// and a [net.DNSError] with IsNotFound, which stays reachable with errors.As.
// The DNS not-found ends it only when it comes from the direct stage.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) (Result, error)
}

// ErrTooManyRedirects is returned when a URL redirects more than
// [Options.MaxRedirects] times.
var ErrTooManyRedirects = errors.New("too many redirects")

// Defaults for [Options].
const (
	DefaultTimeout      = 10 * time.Second
	DefaultMaxBytes     = 2 << 20
	DefaultMaxRedirects = 10
	DefaultUserAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	// PreviewUserAgent is the string iMessage sends for link previews. Some
	// shops that block a browser user agent serve the real page to it.
	PreviewUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_11_1) AppleWebKit/601.2.4 (KHTML, like Gecko) Version/9.0.1 Safari/601.2.4 facebookexternalhit/1.1 Facebot Twitterbot/1.0"
)

// Options configures [Direct]. Zero values mean the defaults.
type Options struct {
	Timeout      time.Duration // whole request, including redirects
	MaxBytes     int64         // response body cap; the rest is ignored
	MaxRedirects int
	UserAgent    string
	// PreviewUserAgent is used for the one retry after a blocked or no-data
	// page. The default is [PreviewUserAgent].
	PreviewUserAgent string
	// AllowPrivateAddrs lets the fetcher connect to loopback, private, and
	// link-local addresses. Off by default so a pasted URL can't reach the
	// host's network. Tests turn it on to use httptest.
	AllowPrivateAddrs bool
	// Tracer records a span for each request, redirects included. The span
	// has the host and status, never the URL. Nil turns tracing off.
	Tracer trace.TracerProvider
}

// Direct fetches pages over HTTP with no proxy or unblocker.
type Direct struct {
	client           *http.Client
	maxBytes         int64
	userAgent        string
	previewUserAgent string
}

var _ Fetcher = (*Direct)(nil)

// NewDirect returns a Direct fetcher.
func NewDirect(o Options) *Direct {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = DefaultMaxRedirects
	}
	if o.UserAgent == "" {
		o.UserAgent = DefaultUserAgent
	}
	if o.PreviewUserAgent == "" {
		o.PreviewUserAgent = PreviewUserAgent
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = dialer.DialContext
	if !o.AllowPrivateAddrs {
		tr.DialContext = netguard.DialContext(dialer)
	}
	limit := o.MaxRedirects
	return &Direct{
		maxBytes:         o.MaxBytes,
		userAgent:        o.UserAgent,
		previewUserAgent: o.PreviewUserAgent,
		client: &http.Client{
			Timeout:   o.Timeout,
			Transport: telemetry.Transport(o.Tracer, tr),
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) > limit {
					return ErrTooManyRedirects
				}
				return nil
			},
		},
	}
}

// Fetch normalizes rawURL, follows redirects, and parses the page. Result.URL
// is the normalized final URL, so short links resolve to the real product.
//
// If the first request gives a blocked or no-data result, Fetch makes one more
// request from the start URL with [Options.PreviewUserAgent] and keeps the
// better result (see [betterResult]). An error from the first request is
// returned as is, with no retry. An error from the retry keeps the first
// result, except [netguard.ErrPrivateAddress], which Fetch returns with an empty
// Result.
func (d *Direct) Fetch(ctx context.Context, rawURL string) (Result, error) {
	start, err := NormalizeURL(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid URL: %w", err)
	}
	first, err := d.fetchOnce(ctx, start, d.userAgent)
	if err != nil || !first.NeedsFallback() {
		return first, err
	}
	second, err := d.fetchOnce(ctx, start, d.previewUserAgent)
	if errors.Is(err, netguard.ErrPrivateAddress) {
		return Result{}, err
	}
	if err != nil {
		return first, nil
	}
	return betterResult(first, second), nil
}

// betterResult picks between the Chrome result and the retry. Fetch returns
// early when the first result is StatusOK, so it is never OK here. A StatusOK
// retry wins. Otherwise the longer Body wins, and a tie keeps the first.
func betterResult(first, second Result) Result {
	switch {
	case second.Status == StatusOK:
		return second
	case len(second.Body) > len(first.Body):
		return second
	}
	return first
}

func (d *Direct) fetchOnce(ctx context.Context, start, userAgent string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, start, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := d.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("fetch %s: %w", start, err)
	}
	defer func() { _ = resp.Body.Close() }()

	final := start
	if n, err := NormalizeURL(resp.Request.URL.String()); err == nil {
		final = n
	}
	return classify(resp, final, d.maxBytes)
}

// classify turns an HTTP response into a Result. It holds the rules shared by
// every fetcher: blocked and non-2xx statuses, non-HTML bodies, the body cap,
// and the "useful data" signal from parse. final is the normalized page URL.
func classify(resp *http.Response, final string, maxBytes int64) (Result, error) {
	res := Result{URL: final, Store: StoreFromURL(final), HTTPStatus: resp.StatusCode}
	switch {
	case isBlockedStatus(resp.StatusCode):
		res.Status = StatusBlocked
		return res, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		res.Status = StatusNoData
		return res, nil
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") {
		res.Status = StatusNoData
		return res, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", final, err)
	}
	var signal bool
	res.Body = body
	res.Product, signal = parse(body, resp.Request.URL.String())
	if !signal {
		res.Status = StatusNoData
	}
	return res, nil
}

func isBlockedStatus(code int) bool {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests,
		http.StatusUnavailableForLegalReasons, http.StatusServiceUnavailable:
		return true
	}
	return false
}
