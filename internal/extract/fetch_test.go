package extract

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"jsclayton.dev/wishbone/internal/netguard"
)

const productPage = `<html><head><meta property="og:title" content="Widget">
<meta property="og:image" content="/w.jpg"></head></html>`

func newDirect(o Options) *Direct {
	o.AllowPrivateAddrs = true
	return NewDirect(o)
}

func TestFetchOK(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(productPage))
	}))
	defer srv.Close()

	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL+"/p?utm_source=x&id=1")
	require.NoError(t, err)
	require.Equal(t, StatusOK, res.Status)
	require.Equal(t, "Widget", res.Title)
	require.Equal(t, srv.URL+"/p?id=1", res.URL)
	require.Equal(t, []string{srv.URL + "/w.jpg"}, res.Images)
	require.Equal(t, "127.0.0.1", res.Store)
	require.Equal(t, productPage, string(res.Body))
	require.Contains(t, ua, "Mozilla/5.0")
}

func TestFetchFollowsRedirectsAndNormalizesFinalURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/short", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/long/product?utm_campaign=z&sku=9", http.StatusFound)
	})
	mux.HandleFunc("/long/product", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(productPage))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL+"/short")
	require.NoError(t, err)
	require.Equal(t, srv.URL+"/long/product?sku=9", res.URL)
}

func TestFetchRedirectLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()

	_, err := newDirect(Options{MaxRedirects: 3}).Fetch(context.Background(), srv.URL+"/loop")
	require.ErrorIs(t, err, ErrTooManyRedirects)
}

func TestFetchTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	_, err := newDirect(Options{Timeout: 50 * time.Millisecond}).Fetch(context.Background(), srv.URL)
	var ne net.Error
	require.True(t, errors.As(err, &ne) && ne.Timeout(), "err = %v, want timeout", err)
}

func TestFetchSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<meta property="og:title" content="Early">`))
		_, _ = w.Write([]byte(strings.Repeat(" ", 10_000)))
		_, _ = w.Write([]byte(`<meta property="og:image" content="https://late.test/i.jpg">`))
	}))
	defer srv.Close()

	res, err := newDirect(Options{MaxBytes: 1000}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, "Early", res.Title)
	require.Empty(t, res.Images, "read past the cap")
}

func TestFetchBlocked(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusUnauthorized} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(productPage)) // block pages must not be parsed
		}))
		res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
		srv.Close()
		require.NoError(t, err)
		require.Equal(t, StatusBlocked, res.Status, code)
		require.Equal(t, code, res.HTTPStatus)
		require.Empty(t, res.Title)
		require.True(t, res.NeedsFallback())
	}
}

func TestFetchNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>hi</body></html>`))
	}))
	defer srv.Close()

	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
	require.True(t, res.NeedsFallback())
}

// A 200 challenge page has a <title> but no product signal. It is NoData so
// stage 2 runs, but the title stays available as a last resort.
func TestFetchChallengePageIsNoDataButKeepsTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Robot or human?</title></head><body>Verify</body></html>`))
	}))
	defer srv.Close()

	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
	require.True(t, res.NeedsFallback())
	require.Equal(t, "Robot or human?", res.Title)
}

func TestFetchNotFoundIsNoData(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
	require.Equal(t, 404, res.HTTPStatus)
}

func TestFetchNonHTMLIsNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte(productPage))
	}))
	defer srv.Close()
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
}

func TestFetchInvalidURL(t *testing.T) {
	_, err := newDirect(Options{}).Fetch(context.Background(), "ftp://x.test/")
	require.Error(t, err)
}

func TestFetchBlocksPrivateAddressesByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(productPage))
	}))
	defer srv.Close()
	_, err := NewDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.Error(t, err)
	require.ErrorIs(t, err, netguard.ErrPrivateAddress)
}

// guardedDirect returns a Direct that refuses private addresses as the server
// does, except allow, so a test server can answer and redirect to one.
func guardedDirect(allow string) *Direct {
	d := NewDirect(Options{})
	dialer := &net.Dialer{
		Timeout: time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if address == allow {
				return nil
			}
			return netguard.RefusePrivate(network, address, c)
		},
	}
	d.client.Transport = &http.Transport{DialContext: dialer.DialContext}
	return d
}

const privateRedirect = "http://10.255.255.1/secret"

func TestFetchRefusesRedirectToPrivateAddress(t *testing.T) {
	srv := newRetryServer(t, func(_ int, _, _ string, w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, privateRedirect, http.StatusFound)
	})
	_, err := guardedDirect(srv.Listener.Addr().String()).Fetch(context.Background(), srv.URL)
	require.ErrorIs(t, err, netguard.ErrPrivateAddress)
	require.Equal(t, []string{DefaultUserAgent}, srv.seen())
}

func TestFetchRefusesRetryRedirectToPrivateAddress(t *testing.T) {
	for name, first := range map[string]func(http.ResponseWriter){
		"blocked": func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) },
		"no data": func(w http.ResponseWriter) { serveHTML(w, fixture(t, "captcha_stub.html")) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := newRetryServer(t, func(_ int, ua, _ string, w http.ResponseWriter, r *http.Request) {
				if ua == DefaultUserAgent {
					first(w)
					return
				}
				http.Redirect(w, r, privateRedirect, http.StatusFound)
			})
			res, err := guardedDirect(srv.Listener.Addr().String()).Fetch(context.Background(), srv.URL)
			require.ErrorIs(t, err, netguard.ErrPrivateAddress)
			require.Equal(t, Result{}, res)
			require.Equal(t, []string{DefaultUserAgent, PreviewUserAgent}, srv.seen())
		})
	}
}

func TestFetchTracesRequestWithoutURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(productPage))
	}))
	defer srv.Close()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))

	_, err := newDirect(Options{Tracer: tp}).Fetch(context.Background(), srv.URL+"/p/secret-path?t=secret-query")
	require.NoError(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	for _, kv := range spans[0].Attributes {
		require.NotContains(t, kv.Value.String(), "secret")
	}
	require.NotContains(t, spans[0].Name, "secret")
}

// retryServer records the user agent and path of each request, then calls a
// handler that writes the response. The handler gets the request number (from
// 1), the user agent, and the path.
type retryServer struct {
	*httptest.Server
	mu    sync.Mutex
	uas   []string
	paths []string
}

// seenPaths returns the paths of the requests so far, in order.
func (rs *retryServer) seenPaths() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.paths...)
}

// seen returns the user agents of the requests so far, in order.
func (rs *retryServer) seen() []string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]string(nil), rs.uas...)
}

func newRetryServer(t *testing.T, h func(n int, ua, path string, w http.ResponseWriter, r *http.Request)) *retryServer {
	t.Helper()
	rs := &retryServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		rs.uas = append(rs.uas, r.UserAgent())
		rs.paths = append(rs.paths, r.URL.Path)
		n := len(rs.uas)
		rs.mu.Unlock()
		h(n, r.UserAgent(), r.URL.Path, w, r)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(b)
}

func serveHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func TestRetryNotNeededForOKPage(t *testing.T) {
	srv := newRetryServer(t, func(_ int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		serveHTML(w, productPage)
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusOK, res.Status)
	require.Equal(t, []string{DefaultUserAgent}, srv.seen())
}

func TestRetryCaptchaThenProductPage(t *testing.T) {
	stub, product := fixture(t, "captcha_stub.html"), fixture(t, "product_no_metadata.html")
	srv := newRetryServer(t, func(_ int, ua, _ string, w http.ResponseWriter, _ *http.Request) {
		if ua == DefaultUserAgent {
			serveHTML(w, stub)
			return
		}
		serveHTML(w, product)
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL+"/p")
	require.NoError(t, err)
	require.Equal(t, []string{DefaultUserAgent, PreviewUserAgent}, srv.seen())
	require.Equal(t, product, string(res.Body))
	require.Equal(t, StatusNoData, res.Status)
	require.Equal(t, "Acme Trail Lantern, Rechargeable Camping Light", res.Title)
}

func TestRetryUsesConfiguredPreviewUserAgent(t *testing.T) {
	srv := newRetryServer(t, func(_ int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := newDirect(Options{UserAgent: "A/1", PreviewUserAgent: "B/2"}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"A/1", "B/2"}, srv.seen())
}

func TestRetryAfter403GivesGoodPage(t *testing.T) {
	srv := newRetryServer(t, func(n int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		serveHTML(w, productPage)
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusOK, res.Status)
	require.Equal(t, "Widget", res.Title)
	require.Len(t, srv.seen(), 2)
}

func TestRetry403TwiceIsBlockedAfterTwoRequests(t *testing.T) {
	srv := newRetryServer(t, func(_ int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, res.Status)
	require.Len(t, srv.seen(), 2)
}

func TestRetryNoDataTwiceKeepsLongerBody(t *testing.T) {
	short, long := fixture(t, "captcha_stub.html"), fixture(t, "product_no_metadata.html")
	for name, c := range map[string]struct{ first, second, want string }{
		"retry longer":    {short, long, long},
		"first longer":    {long, short, long},
		"tie keeps first": {short, strings.ToUpper(short), short},
	} {
		t.Run(name, func(t *testing.T) {
			srv := newRetryServer(t, func(n int, _, _ string, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					serveHTML(w, c.first)
					return
				}
				serveHTML(w, c.second)
			})
			res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
			require.NoError(t, err)
			require.Equal(t, StatusNoData, res.Status)
			require.Equal(t, c.want, string(res.Body))
			require.Len(t, srv.seen(), 2)
		})
	}
}

func TestRetryOKPageWinsOverLongerNoDataBody(t *testing.T) {
	// The first result is NoData with the longer body; the retry is OK with a
	// shorter one. StatusOK must win over length.
	long := fixture(t, "product_no_metadata.html")
	require.Greater(t, len(long), len(productPage))
	srv := newRetryServer(t, func(n int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			serveHTML(w, long)
			return
		}
		serveHTML(w, productPage)
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusOK, res.Status)
	require.Equal(t, "Widget", res.Title)
	require.Len(t, srv.seen(), 2)
}

func TestRetryNoDataBodyBeatsBlockedFirstResult(t *testing.T) {
	// A blocked first result has no body, so a no-data retry with a body wins.
	stub := fixture(t, "captcha_stub.html")
	srv := newRetryServer(t, func(n int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		serveHTML(w, stub)
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
	require.NotEmpty(t, res.Body)
}

func TestNoRetryAfterFirstRequestError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	_, err := newDirect(Options{Timeout: 50 * time.Millisecond}).Fetch(context.Background(), srv.URL)
	require.Error(t, err)
	time.Sleep(400 * time.Millisecond)
	require.EqualValues(t, 1, n.Load())
}

func TestRetryErrorKeepsFirstResult(t *testing.T) {
	stub := fixture(t, "captcha_stub.html")
	srv := newRetryServer(t, func(n int, _, _ string, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			serveHTML(w, stub)
			return
		}
		time.Sleep(300 * time.Millisecond)
	})
	// The first request is fast; the retry times out.
	d := newDirect(Options{Timeout: 100 * time.Millisecond})
	res, err := d.Fetch(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, StatusNoData, res.Status)
	require.Equal(t, stub, string(res.Body))
	require.Len(t, srv.seen(), 2)
}

func TestRetryStartsFromStartURLNotChallengeRedirect(t *testing.T) {
	product, stub := fixture(t, "product_no_metadata.html"), fixture(t, "captcha_stub.html")
	srv := newRetryServer(t, func(_ int, ua, path string, w http.ResponseWriter, r *http.Request) {
		switch {
		case path == "/p" && ua == DefaultUserAgent:
			http.Redirect(w, r, "/challenge", http.StatusFound)
		case path == "/challenge":
			serveHTML(w, stub)
		default:
			serveHTML(w, product)
		}
	})
	res, err := newDirect(Options{}).Fetch(context.Background(), srv.URL+"/p")
	require.NoError(t, err)
	require.Equal(t, []string{"/p", "/challenge", "/p"}, srv.seenPaths())
	require.Equal(t, product, string(res.Body))
	require.Equal(t, srv.URL+"/p", res.URL)
}
