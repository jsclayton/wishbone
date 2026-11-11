package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestServer(t *testing.T, p Pinger) *httptest.Server {
	t.Helper()
	opts, _, _ := testOptions(t)
	opts.Store = p
	ts := httptest.NewServer(New(opts))
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestClientRoutesServeIndex(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := get(t, ts.URL+"/some/client/route")
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Contains(t, res.Header.Get("Content-Type"), "text/html")
}

func TestRequestsAreTracedByRoutePattern(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	opts, _, _ := testOptions(t)
	opts.Tracer = tp
	ts := httptest.NewServer(New(opts))
	t.Cleanup(ts.Close)

	get(t, ts.URL+"/api/v2/health")
	get(t, ts.URL+"/login/secret-login-token")
	ts.Close() // wait for in-flight handlers, so every span has ended

	spans := exp.GetSpans()
	require.Len(t, spans, 2)
	require.Equal(t, "GET /api/v2/health", spans[0].Name)
	for _, s := range spans {
		require.NotContains(t, s.Name, "secret-login-token")
		for _, kv := range s.Attributes {
			require.False(t, strings.Contains(kv.Value.String(), "secret-login-token"), "%s leaks the token", kv.Key)
		}
	}
}

func TestRequestLogMasksLoginTokens(t *testing.T) {
	var buf bytes.Buffer
	opts, _, _ := testOptions(t)
	opts.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ts := httptest.NewServer(New(opts))
	t.Cleanup(ts.Close)

	get(t, ts.URL+"/login/secret-login-token")
	get(t, ts.URL+"/login/secret-login-token/extra")
	get(t, ts.URL+"/login")
	get(t, ts.URL+"/api/v2/health")
	ts.Close() // wait for in-flight handlers, so every line is written

	out := buf.String()
	require.NotContains(t, out, "secret-login-token")
	require.NotContains(t, out, "extra")
	require.Equal(t, 2, strings.Count(out, "path=/login/{token}"), out)
	require.Contains(t, out, "path=/login ") // the bare route is not masked
	require.Contains(t, out, "path=/api/v2/health")
}

func TestContentSecurityPolicy(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, path := range []string{"/", "/list", "/api/v2/health", "/api/v2/nope", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			res := get(t, ts.URL+path)
			require.Equal(t, contentSecurityPolicy, res.Header.Get("Content-Security-Policy"))
		})
	}
}

func TestContentSecurityPolicyIsTheExactPolicy(t *testing.T) {
	require.Equal(t, "default-src 'self'; script-src 'self'; style-src 'self'; "+
		"img-src 'self' data: blob: http: https:; connect-src 'self'; object-src 'none'; "+
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'", contentSecurityPolicy)
}

func TestDocsPageKeepsHumasPolicy(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := get(t, ts.URL+"/api/v2/docs")
	require.Equal(t, http.StatusOK, res.StatusCode)
	csp := res.Header.Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	require.NotEqual(t, contentSecurityPolicy, csp)
}

func TestOtherSecurityHeadersAreUnchanged(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	res := get(t, ts.URL+"/")
	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "same-origin", res.Header.Get("Referrer-Policy"))
	require.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
}

func TestCrossOriginOpenerPolicy(t *testing.T) {
	ts := newTestServer(t, fakePinger{})

	for _, path := range []string{"/", "/list", "/api/v2/health", "/api/v2/nope", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			res := get(t, ts.URL+path)
			require.Equal(t, "same-origin", res.Header.Get("Cross-Origin-Opener-Policy"))
		})
	}
}

func TestAppShellAndAssetsAreNotNoStore(t *testing.T) {
	ts := newTestServer(t, fakePinger{})
	for _, path := range []string{"/", "/assets/missing.js"} {
		res := get(t, ts.URL+path)
		require.Equal(t, http.StatusOK, res.StatusCode, path)
		require.NotContains(t, res.Header.Get("Cache-Control"), "no-store", path)
	}
}

func TestAPINoStoreSkipsNonAPIPaths(t *testing.T) {
	handler := apiNoStore(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, path := range []string{"/", "/assets/app-abc123.js", "/metrics", "/sw.js", "/apiary"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Empty(t, rec.Header().Get("Cache-Control"), path)
	}
}
