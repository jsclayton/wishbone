package extract

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/telemetry"
)

// RedirectsTimeout bounds one [Redirects.Follow], all hops together.
const RedirectsTimeout = 5 * time.Second

// RedirectsOptions configures [NewRedirects].
type RedirectsOptions struct {
	Timeout      time.Duration // zero means [RedirectsTimeout]
	MaxRedirects int           // zero means [DefaultMaxRedirects]
	// AllowPrivateAddrs lets Follow connect to loopback, private, and
	// link-local addresses. Tests turn it on to use httptest.
	AllowPrivateAddrs bool
	Tracer            trace.TracerProvider
}

// Redirects finds where a link leads. It reads no page: it sends a GET for
// each hop and closes the body unread.
type Redirects struct {
	client  *http.Client
	timeout time.Duration
	limit   int
}

// NewRedirects returns a [Redirects] that connects through [netguard].
func NewRedirects(o RedirectsOptions) *Redirects {
	if o.Timeout <= 0 {
		o.Timeout = RedirectsTimeout
	}
	if o.MaxRedirects <= 0 {
		o.MaxRedirects = DefaultMaxRedirects
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = dialer.DialContext
	if !o.AllowPrivateAddrs {
		tr.DialContext = netguard.DialContext(dialer)
	}
	return &Redirects{
		timeout: o.Timeout,
		limit:   o.MaxRedirects,
		client: &http.Client{
			Transport:     telemetry.Transport(o.Tracer, tr),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Follow returns each address rawURL redirects to, in order, the final one
// last. It starts from rawURL in the form [NormalizeURL] gives. It is empty
// when the link doesn't redirect. Only a 301, 302, 303, 307, or 308 response
// is a redirect. On an error it returns the addresses reached so far with
// the error.
func (r *Redirects) Follow(ctx context.Context, rawURL string) ([]string, error) {
	cur, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	var chain []string
	for range r.limit + 1 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur, nil)
		if err != nil {
			return chain, err
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return chain, fmt.Errorf("follow redirects: unsupported scheme %q", req.URL.Scheme)
		}
		req.Header.Set("User-Agent", DefaultUserAgent)
		res, err := r.client.Do(req)
		if err != nil {
			return chain, err
		}
		_ = res.Body.Close()
		if !isRedirect(res.StatusCode) {
			return chain, nil
		}
		loc, err := res.Location()
		if err != nil { // no Location: the link ends here
			return chain, nil
		}
		cur = loc.String()
		chain = append(chain, cur)
	}
	return chain, ErrTooManyRedirects
}

// isRedirect reports whether a response with this status leads on to its
// Location, as [http.Client] follows it.
func isRedirect(code int) bool {
	switch code {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}
