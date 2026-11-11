package telemetry_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"jsclayton.dev/wishbone/internal/telemetry"
)

func newRecorder() (*sdktrace.TracerProvider, *tracetest.InMemoryExporter) {
	exp := tracetest.NewInMemoryExporter()
	return sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)), exp
}

func attrs(s tracetest.SpanStub) map[string]string {
	m := map[string]string{}
	for _, kv := range s.Attributes {
		m[string(kv.Key)] = kv.Value.String()
	}
	return m
}

func TestMiddlewareRecordsRoutePatternNotToken(t *testing.T) {
	tp, exp := newRecorder()
	r := chi.NewRouter()
	r.Use(telemetry.Middleware(tp))
	r.Get("/login/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

	req := httptest.NewRequest(http.MethodGet, "/login/s3cret-token?code=abc", nil)
	req.Header.Set("Cookie", "session=cookie-secret")
	req.Header.Set("Authorization", "Bearer bearer-secret")
	r.ServeHTTP(httptest.NewRecorder(), req)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	s := spans[0]
	require.Equal(t, "GET /login/{token}", s.Name)
	a := attrs(s)
	require.Equal(t, "/login/{token}", a["http.route"])
	require.Equal(t, "418", a["http.response.status_code"])
	require.Equal(t, "GET", a["http.request.method"])
	dump := s.Name
	for k, v := range a {
		dump += " " + k + "=" + v
	}
	for _, secret := range []string{"s3cret-token", "cookie-secret", "bearer-secret", "code=abc"} {
		require.NotContains(t, dump, secret)
	}
}

func TestMiddlewareUnmatchedRouteHasNoRawPath(t *testing.T) {
	tp, exp := newRecorder()
	r := chi.NewRouter()
	r.Use(telemetry.Middleware(tp))
	r.Get("/other", func(http.ResponseWriter, *http.Request) {})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/login/s3cret-token", nil))

	s := exp.GetSpans()[0]
	require.NotContains(t, s.Name, "s3cret")
	for _, v := range attrs(s) {
		require.NotContains(t, v, "s3cret")
	}
}

func TestMiddlewareBoundsUnknownMethod(t *testing.T) {
	tp, exp := newRecorder()
	r := chi.NewRouter()
	r.Use(telemetry.Middleware(tp))
	r.Get("/", func(http.ResponseWriter, *http.Request) {})
	long := strings.Repeat("X", 500)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Method = long
	r.ServeHTTP(httptest.NewRecorder(), req)

	s := exp.GetSpans()[0]
	require.Equal(t, "_OTHER", s.Name)
	require.Equal(t, "_OTHER", attrs(s)["http.request.method"])
}

func TestMiddlewareIgnoresClientTraceContext(t *testing.T) {
	tp, exp := newRecorder()
	h := telemetry.Middleware(tp)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	s := exp.GetSpans()[0]
	require.NotEqual(t, traceID, s.SpanContext.TraceID().String())
	require.False(t, s.Parent.IsValid(), "span must be a root")
}

func TestMiddlewareNilProviderIsPassThrough(t *testing.T) {
	h := telemetry.Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestTransportRecordsHostOnly(t *testing.T) {
	tp, exp := newRecorder()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer backend.Close()

	c := &http.Client{Transport: telemetry.Transport(tp, nil)}
	resp, err := c.Get(backend.URL + "/product/12345?token=urlsecret")
	require.NoError(t, err)
	_ = resp.Body.Close()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	a := attrs(spans[0])
	require.Equal(t, "200", a["http.response.status_code"])
	require.NotEmpty(t, a["server.address"])
	for k, v := range a {
		require.NotContains(t, v, "urlsecret", k)
		require.NotContains(t, v, "12345", k)
	}
	require.Empty(t, resp.Request.Header.Get("Traceparent"), "outgoing request must not carry trace context to third parties")
}

func TestTransportBoundsUnknownMethod(t *testing.T) {
	tp, exp := newRecorder()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer backend.Close()

	req, err := http.NewRequest("PURGE", backend.URL, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: telemetry.Transport(tp, nil)}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	s := exp.GetSpans()[0]
	require.Equal(t, "HTTP _OTHER", s.Name)
	require.Equal(t, "_OTHER", attrs(s)["http.request.method"])
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestSetupWithoutEndpointStartsNoExporter(t *testing.T) {
	called := false
	factory := func(context.Context) (sdktrace.SpanExporter, error) {
		called = true
		return tracetest.NewInMemoryExporter(), nil
	}
	for name, env := range map[string]map[string]string{
		"unset":    {},
		"empty":    {"OTEL_EXPORTER_OTLP_ENDPOINT": ""},
		"disabled": {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x:4318", "OTEL_SDK_DISABLED": "true"},
		"none":     {"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x:4318", "OTEL_TRACES_EXPORTER": "none"},
	} {
		tp, shutdown, err := telemetry.Setup(context.Background(), envOf(env), "v", nil, factory)
		require.NoError(t, err, name)
		require.Nil(t, tp, name)
		require.NotNil(t, shutdown, name)
		require.NoError(t, shutdown(context.Background()))
	}
	require.False(t, called, "exporter factory ran with tracing off")
}

func TestSetupRejectsUnsupportedSettings(t *testing.T) {
	factory := func(context.Context) (sdktrace.SpanExporter, error) { return tracetest.NewInMemoryExporter(), nil }
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"grpc":        {map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}, "OTEL_EXPORTER_OTLP_PROTOCOL"},
		"traces json": {map[string]string{"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/json"}, "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"},
		"zipkin":      {map[string]string{"OTEL_TRACES_EXPORTER": "zipkin"}, "OTEL_TRACES_EXPORTER"},
	} {
		env := tc.env
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://x:4318"
		_, _, err := telemetry.Setup(context.Background(), envOf(env), "v", nil, factory)
		require.ErrorContains(t, err, tc.want, name)
	}
	for name, env := range map[string]map[string]string{
		"protobuf": {"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
		"otlp":     {"OTEL_TRACES_EXPORTER": "otlp"},
	} {
		env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://x:4318"
		tp, _, err := telemetry.Setup(context.Background(), envOf(env), "v", nil, factory)
		require.NoError(t, err, name)
		require.NotNil(t, tp, name)
	}
}

func TestSetupWithEndpointStartsExporterAndShutdownFlushes(t *testing.T) {
	for _, key := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		exp := &countingExporter{}
		calls := 0
		factory := func(context.Context) (sdktrace.SpanExporter, error) { calls++; return exp, nil }
		tp, shutdown, err := telemetry.Setup(context.Background(), envOf(map[string]string{key: "http://collector:4318"}), "v", nil, factory)
		require.NoError(t, err, key)
		require.NotNil(t, tp, key)
		require.Equal(t, 1, calls, key)
		_, span := tp.Tracer("t").Start(context.Background(), "x")
		span.End()
		require.NoError(t, shutdown(context.Background()))
		require.Len(t, exp.spans, 1, "%s: shutdown did not flush the batch", key)
	}
}

func resourceAttrs(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	exp := &countingExporter{}
	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://collector:4318"
	tp, shutdown, err := telemetry.Setup(context.Background(), envOf(env), "1.2.3", nil,
		func(context.Context) (sdktrace.SpanExporter, error) { return exp, nil })
	require.NoError(t, err)
	_, span := tp.Tracer("t").Start(context.Background(), "x")
	span.End()
	require.NoError(t, shutdown(context.Background()))
	require.Len(t, exp.spans, 1)
	m := map[string]string{}
	for _, kv := range exp.spans[0].Resource().Attributes() {
		m[string(kv.Key)] = kv.Value.String()
	}
	return m
}

func TestSetupDefaultResource(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	a := resourceAttrs(t, map[string]string{})
	require.Equal(t, "wishbone", a["service.name"])
	require.Equal(t, "1.2.3", a["service.version"])
}

func TestSetupServiceNameEnvOverrides(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "custom")
	a := resourceAttrs(t, map[string]string{})
	require.Equal(t, "custom", a["service.name"])
	require.Equal(t, "1.2.3", a["service.version"])
}

// countingExporter keeps exported spans. The in-memory exporter clears its
// spans on shutdown, so it can't show that shutdown flushed them.
type countingExporter struct{ spans []sdktrace.ReadOnlySpan }

func (c *countingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.spans = append(c.spans, spans...)
	return nil
}

func (c *countingExporter) Shutdown(context.Context) error { return nil }

func TestSetupSilencesSDKLoggerBeforeBuildingExporter(t *testing.T) {
	var captured []string
	otel.SetLogger(funcr.New(func(prefix, args string) { captured = append(captured, prefix+args) }, funcr.Options{Verbosity: 8}))
	t.Cleanup(func() {
		otel.SetLogger(logr.Discard())
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	})
	const secret = "hunter2-secret"
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "malformed"+secret)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "")
	env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1"}

	_, shutdown, err := telemetry.Setup(context.Background(), envOf(env), "v", nil, telemetry.OTLPHTTP)
	require.NoError(t, err)
	_ = shutdown(context.Background())
	require.Empty(t, captured)
	require.NotContains(t, fmt.Sprint(captured), secret)
}

func TestSetupValidatesOnlyWhenEnabled(t *testing.T) {
	called := false
	factory := func(context.Context) (sdktrace.SpanExporter, error) {
		called = true
		return tracetest.NewInMemoryExporter(), nil
	}
	tp, _, err := telemetry.Setup(context.Background(), envOf(map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}), "v", nil, factory)
	require.NoError(t, err)
	require.Nil(t, tp)
	require.False(t, called)
}

func TestEnabledIsCaseInsensitiveAndTrims(t *testing.T) {
	require.False(t, telemetry.Enabled(envOf(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_TRACES_EXPORTER": "NONE"})))
	require.False(t, telemetry.Enabled(envOf(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_SDK_DISABLED": "TRUE"})))
	require.False(t, telemetry.Enabled(envOf(map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "  ", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "\t"})))
	require.True(t, telemetry.Enabled(envOf(map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": " http://x "})))
}

func TestSetupAcceptsMixedCaseEnums(t *testing.T) {
	factory := func(context.Context) (sdktrace.SpanExporter, error) { return tracetest.NewInMemoryExporter(), nil }
	env := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://x", "OTEL_EXPORTER_OTLP_PROTOCOL": "HTTP/PROTOBUF", "OTEL_TRACES_EXPORTER": "OTLP"}
	tp, _, err := telemetry.Setup(context.Background(), envOf(env), "v", nil, factory)
	require.NoError(t, err)
	require.NotNil(t, tp)
}

func TestTransportTruncatesLongHost(t *testing.T) {
	tp, exp := newRecorder()
	rt := telemetry.Transport(tp, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	}))
	req, err := http.NewRequest(http.MethodGet, "http://"+strings.Repeat("a", 400)+".example/", nil)
	require.NoError(t, err)
	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	require.Len(t, attrs(exp.GetSpans()[0])["server.address"], 253)
}

func TestTransportHostIsValidUTF8(t *testing.T) {
	for name, host := range map[string]string{"invalid": "\xff.example", "multibyte": strings.Repeat("é", 200)} {
		t.Run(name, func(t *testing.T) {
			tp, exp := newRecorder()
			rt := telemetry.Transport(tp, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			}))
			req := &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "http", Host: host, Path: "/"}, Header: http.Header{}}
			_, err := rt.RoundTrip(req.WithContext(context.Background()))
			require.NoError(t, err)
			got := attrs(exp.GetSpans()[0])["server.address"]
			require.True(t, utf8.ValidString(got))
			require.LessOrEqual(t, len(got), 253)
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// bodyTransport returns a 200 response whose body is r.
func bodyTransport(r io.Reader) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(r)}, nil
	})
}

func getVia(t *testing.T, rt http.RoundTripper) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://host.example/secret/path?token=urlsecret", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	return resp
}

func TestTransportSpanWaitsForBody(t *testing.T) {
	tp, exp := newRecorder()
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write([]byte("hello"))
	}))
	defer backend.Close()
	defer free() // a failed assertion must not hang backend.Close

	resp, err := (&http.Client{Transport: telemetry.Transport(tp, nil)}).Get(backend.URL)
	require.NoError(t, err)
	require.Empty(t, exp.GetSpans(), "span must stay open while the body is unread")

	free()
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, exp.GetSpans(), 1)
	_ = resp.Body.Close()
	require.Len(t, exp.GetSpans(), 1)
}

func TestTransportBodyReadErrorMarksSpan(t *testing.T) {
	tp, exp := newRecorder()
	resp := getVia(t, telemetry.Transport(tp, bodyTransport(
		io.MultiReader(strings.NewReader("part"), iotest.ErrReader(errors.New("boom urlsecret")))),
	))
	_, err := io.ReadAll(resp.Body)
	require.Error(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status.Code)
	require.Equal(t, "read failed", spans[0].Status.Description)
	require.Equal(t, "HTTP GET", spans[0].Name)
	for k, v := range attrs(spans[0]) {
		require.NotContains(t, v, "urlsecret", k)
		require.NotContains(t, v, "boom", k)
		require.NotContains(t, v, "/secret/path", k)
	}
	_ = resp.Body.Close()
	require.Len(t, exp.GetSpans(), 1)
}

func TestTransportCloseBeforeEOFEndsSpanWithoutError(t *testing.T) {
	tp, exp := newRecorder()
	resp := getVia(t, telemetry.Transport(tp, bodyTransport(strings.NewReader("a long body"))))
	_, err := resp.Body.Read(make([]byte, 1))
	require.NoError(t, err)
	require.Empty(t, exp.GetSpans())

	require.NoError(t, resp.Body.Close())
	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Unset, spans[0].Status.Code)
}

func TestTransportEOFThenCloseExportsOneSpan(t *testing.T) {
	tp, exp := newRecorder()
	resp := getVia(t, telemetry.Transport(tp, bodyTransport(strings.NewReader("body"))))
	_, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, exp.GetSpans(), 1)
	require.Equal(t, codes.Unset, exp.GetSpans()[0].Status.Code)
}

func TestTransportNoBodyEndsSpanInRoundTrip(t *testing.T) {
	cases := map[string]*http.Response{
		"no body":  {StatusCode: 204, Body: http.NoBody},
		"nil body": {StatusCode: 200},
		"upgrade": {StatusCode: 101, Body: struct {
			io.Reader
			io.Writer
			io.Closer
		}{strings.NewReader(""), io.Discard, io.NopCloser(nil)}},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			tp, exp := newRecorder()
			rt := telemetry.Transport(tp, roundTripFunc(func(*http.Request) (*http.Response, error) { return r, nil }))
			resp := getVia(t, rt)
			require.Len(t, exp.GetSpans(), 1)
			if r.StatusCode == 101 {
				_, ok := resp.Body.(io.Writer)
				require.True(t, ok, "101 body must stay writable")
			}
		})
	}
}

func TestTransportNoBodyOverRealServerEndsSpanInRoundTrip(t *testing.T) {
	cases := map[string]struct {
		method  string
		handler http.HandlerFunc
	}{
		"204": {http.MethodGet, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }},
		"content-length 0": {http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "0")
		}},
		"head": {http.MethodHead, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ignored")) }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tp, exp := newRecorder()
			backend := httptest.NewServer(c.handler)
			defer backend.Close()

			req, err := http.NewRequest(c.method, backend.URL, nil)
			require.NoError(t, err)
			resp, err := telemetry.Transport(tp, nil).RoundTrip(req)
			require.NoError(t, err)
			require.Len(t, exp.GetSpans(), 1, "span must end before any Close")
			_ = resp.Body.Close()
			require.Len(t, exp.GetSpans(), 1)
		})
	}
}

func TestTransportNilProviderReturnsBase(t *testing.T) {
	base := &http.Transport{}
	require.Same(t, base, telemetry.Transport(nil, base))
}

func TestTransportRedirectGivesSpanPerHop(t *testing.T) {
	tp, exp := newRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/end", http.StatusFound) // has a body
	})
	mux.HandleFunc("/end", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("done")) })
	backend := httptest.NewServer(mux)
	defer backend.Close()

	resp, err := (&http.Client{Transport: telemetry.Transport(tp, nil)}).Get(backend.URL + "/start")
	require.NoError(t, err)
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Len(t, exp.GetSpans(), 2)
}

func TestTransportServerErrorAndFailedRequestKeepBehavior(t *testing.T) {
	tp, exp := newRecorder()
	resp := getVia(t, telemetry.Transport(tp, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("x"))}, nil
	})))
	_ = resp.Body.Close()
	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status.Code)
	require.Empty(t, spans[0].Status.Description)

	tp, exp = newRecorder()
	req, err := http.NewRequest(http.MethodGet, "http://host.example/", nil)
	require.NoError(t, err)
	_, err = telemetry.Transport(tp, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial urlsecret")
	})).RoundTrip(req)
	require.Error(t, err)
	spans = exp.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "request failed", spans[0].Status.Description)
}
