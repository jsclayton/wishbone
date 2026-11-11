package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"

	"jsclayton.dev/wishbone/internal/telemetry"
)

// DefaultBrightDataEndpoint is the Web Unlocker request API.
const DefaultBrightDataEndpoint = "https://api.brightdata.com/request"

// DefaultBrightDataTimeout is longer than [DefaultTimeout] because the
// unlocker retries and solves challenges on its side.
const DefaultBrightDataTimeout = 60 * time.Second

// ErrBrightDataDisabled is returned by [BrightData.Fetch] when the API key or
// zone is not set.
var ErrBrightDataDisabled = errors.New("bright data is not configured")

// BrightDataOptions configures [BrightData].
type BrightDataOptions struct {
	APIKey   string // WISHBONE_BRIGHTDATA_API_KEY
	Zone     string // WISHBONE_BRIGHTDATA_ZONE
	Endpoint string // defaults to [DefaultBrightDataEndpoint]; tests set it
	Timeout  time.Duration
	MaxBytes int64
	Client   *http.Client // optional; defaults to a client with Timeout
	// Tracer records a span for each API call. It applies only to the default
	// client. Nil turns tracing off.
	Tracer trace.TracerProvider
}

// BrightData is stage 2. It fetches the page through the Bright Data Web
// Unlocker API and parses it with the same rules as [Direct].
type BrightData struct {
	apiKey, zone, endpoint string
	maxBytes               int64
	client                 *http.Client
	requests               atomic.Int64
}

var _ Fetcher = (*BrightData)(nil)

// NewBrightData returns a fetcher. It is disabled, see [BrightData.Enabled],
// unless both APIKey and Zone are set.
func NewBrightData(o BrightDataOptions) *BrightData {
	if o.Endpoint == "" {
		o.Endpoint = DefaultBrightDataEndpoint
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultBrightDataTimeout
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout, Transport: telemetry.Transport(o.Tracer, nil)}
	}
	return &BrightData{apiKey: o.APIKey, zone: o.Zone, endpoint: o.Endpoint, maxBytes: o.MaxBytes, client: o.Client}
}

// Enabled reports whether both the API key and the zone are set.
func (b *BrightData) Enabled() bool { return b.apiKey != "" && b.zone != "" }

// Requests returns how many requests were sent to the API. It backs the
// Bright Data request metric. Calls that fail validation or are disabled are
// not counted.
func (b *BrightData) Requests() int64 { return b.requests.Load() }

// Fetch asks the Web Unlocker for rawURL. An error means the unlocker call
// failed: it is disabled, the network failed or timed out, the API key was
// refused, or Bright Data reported its own error (x-brd-err-code header, or a
// 5xx other than 503 with no target page). A target page that is blocked or
// empty is a Result, as in [Direct].
//
// A 401 is always an error here, because it can't be told from a refused API
// key. Don't rely on StatusBlocked for a target 401.
func (b *BrightData) Fetch(ctx context.Context, rawURL string) (Result, error) {
	if !b.Enabled() {
		return Result{}, ErrBrightDataDisabled
	}
	target, err := NormalizeURL(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid URL: %w", err)
	}
	payload, err := json.Marshal(map[string]string{"zone": b.zone, "url": target, "format": "raw"})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	req.Header.Set("Content-Type", "application/json")

	b.requests.Add(1)
	resp, err := b.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("bright data request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if code := resp.Header.Get("X-Brd-Err-Code"); code != "" {
		return Result{}, fmt.Errorf("bright data error %s: %s", code, resp.Header.Get("X-Brd-Err-Msg"))
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode >= 500 && resp.StatusCode != http.StatusServiceUnavailable {
		return Result{}, fmt.Errorf("bright data request: unexpected status %d", resp.StatusCode)
	}

	// The response URL is the unlocker endpoint, so resolve page links
	// against the target instead.
	resp.Request = &http.Request{URL: mustParse(target)}
	return classify(resp, target, b.maxBytes)
}

func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return &url.URL{}
	}
	return u
}
