// Package telemetry sets up optional OpenTelemetry tracing. Tracing is off
// unless an OTLP endpoint is set. When it is off, no exporter starts.
//
// Spans never carry URLs, query strings, or headers: login links, session
// cookies, and tokens travel in those. Server spans use the route pattern, and
// client spans use only the method, host, and status.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "jsclayton.dev/wishbone"

// ExporterFactory builds the span exporter. Tests pass an in-memory one.
type ExporterFactory func(context.Context) (sdktrace.SpanExporter, error)

// OTLPHTTP is the [ExporterFactory] that the server uses. The exporter reads the
// standard OTEL_EXPORTER_OTLP_* settings (endpoint, headers, timeout,
// compression, certificates) itself.
func OTLPHTTP(ctx context.Context) (sdktrace.SpanExporter, error) {
	return otlptracehttp.New(ctx)
}

// enum returns a trimmed, lower-case setting.
func enum(getenv func(string) string, k string) string {
	return strings.ToLower(strings.TrimSpace(getenv(k)))
}

// Enabled reports whether the environment turns tracing on: a non-blank OTLP
// endpoint is set, OTEL_SDK_DISABLED is not "true", and OTEL_TRACES_EXPORTER
// is not "none". Enum values are case-insensitive.
func Enabled(getenv func(string) string) bool {
	if enum(getenv, "OTEL_TRACES_EXPORTER") == "none" || enum(getenv, "OTEL_SDK_DISABLED") == "true" {
		return false
	}
	for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		if strings.TrimSpace(getenv(k)) != "" {
			return true
		}
	}
	return false
}

// validate rejects settings Wishbone does not support, so a wrong setup fails
// at startup instead of silently sending nothing.
func validate(getenv func(string) string) error {
	if v := enum(getenv, "OTEL_TRACES_EXPORTER"); v != "" && v != "otlp" && v != "none" {
		return fmt.Errorf("OTEL_TRACES_EXPORTER=%q is not supported: use otlp or none", getenv("OTEL_TRACES_EXPORTER"))
	}
	for _, k := range []string{"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"} {
		if v := enum(getenv, k); v != "" && v != "http/protobuf" {
			return fmt.Errorf("%s=%q is not supported: use http/protobuf", k, getenv(k))
		}
	}
	return nil
}

// Setup returns a tracer provider and a shutdown function that flushes spans.
// With tracing off it returns a nil provider and a no-op shutdown, it skips
// validation, it never calls newExporter, and it leaves the OTel globals
// alone. With tracing on, it validates the settings, then routes SDK errors to
// logger with a fixed message and silences the SDK's own logger, which can
// print raw settings such as OTLP headers. It does this before it builds the
// exporter. The default service name is "wishbone" and the default service
// version is version. OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES override
// them. The SDK reads OTEL_TRACES_SAMPLER itself.
func Setup(ctx context.Context, getenv func(string) string, version string, logger *slog.Logger, newExporter ExporterFactory) (trace.TracerProvider, func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if !Enabled(getenv) {
		return nil, noop, nil
	}
	if err := validate(getenv); err != nil {
		return nil, noop, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	otel.SetLogger(logr.Discard())
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("tracing error", slog.Any("error", err))
	}))
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "wishbone"), attribute.String("service.version", version)),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, noop, err
	}
	exp, err := newExporter(ctx)
	if err != nil {
		return nil, noop, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	return tp, tp.Shutdown, nil
}

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
	http.MethodConnect: true, http.MethodTrace: true,
}

// method bounds the method to a fixed set, since clients choose it.
func method(m string) string {
	if knownMethods[m] {
		return m
	}
	return "_OTHER"
}

// Middleware traces each request. The span is named after the method and the
// chi route pattern, and ends after routing, when the pattern is known. A nil
// provider returns a pass-through. Install it with r.Use.
func Middleware(tp trace.TracerProvider) func(http.Handler) http.Handler {
	if tp == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	tracer := tp.Tracer(tracerName)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Clients are untrusted: ignore their trace context. Every span is a root.
			m := method(r.Method)
			ctx, span := tracer.Start(r.Context(), m, trace.WithSpanKind(trace.SpanKindServer), trace.WithNewRoot())
			defer span.End()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			attrs := []attribute.KeyValue{
				attribute.String("http.request.method", m),
				attribute.Int("http.response.status_code", status),
			}
			name := m
			// An unmatched request has no pattern. Its raw path stays out.
			if rc := chi.RouteContext(r.Context()); rc != nil {
				if p := rc.RoutePattern(); p != "" {
					name = m + " " + p
					attrs = append(attrs, attribute.String("http.route", p))
				}
			}
			span.SetName(name)
			span.SetAttributes(attrs...)
			if status >= 500 {
				span.SetStatus(codes.Error, "")
			}
		})
	}
}

// Transport wraps base so each request makes a client span with the method,
// host, and status. It records no path or query, and it sends no trace
// headers to the remote host. A nil provider returns base unchanged. A nil
// base means [http.DefaultTransport].
//
// A span ends when the caller finishes with the response body: at EOF, at a
// read error, or at Close, whichever comes first. A read error sets the status
// to "read failed". Callers must close the body, as net/http already requires,
// or the span is never exported. A failed request ends its span at once. So
// does a response with no body to wait for: [http.NoBody], a nil body, or
// status 101.
func Transport(tp trace.TracerProvider, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if tp == nil {
		return base
	}
	return &transport{base: base, tracer: tp.Tracer(tracerName)}
}

type transport struct {
	base   http.RoundTripper
	tracer trace.Tracer
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	m := method(req.Method)
	ctx, span := t.tracer.Start(req.Context(), "HTTP "+m, trace.WithSpanKind(trace.SpanKindClient))
	span.SetAttributes(
		attribute.String("http.request.method", m),
		attribute.String("server.address", truncate(req.URL.Hostname(), 253)),
	)
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		span.SetStatus(codes.Error, "request failed")
		span.End()
		return nil, err
	}
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= 500 {
		span.SetStatus(codes.Error, "")
	}
	// A 101 body must stay writable, which a wrapper would hide.
	if resp.Body == nil || resp.Body == http.NoBody || resp.StatusCode == http.StatusSwitchingProtocols {
		span.End()
		return resp, nil
	}
	resp.Body = &spanBody{ReadCloser: resp.Body, span: span}
	return resp, nil
}

// spanBody ends the client span at the first of EOF, a read error, or Close.
type spanBody struct {
	io.ReadCloser
	span trace.Span
	once sync.Once
}

func (b *spanBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.end(false)
	} else if err != nil {
		b.end(true)
	}
	return n, err
}

func (b *spanBody) Close() error {
	err := b.ReadCloser.Close()
	b.end(false)
	return err
}

// end ends the span once. The status text is fixed: error text can hold a URL.
func (b *spanBody) end(failed bool) {
	b.once.Do(func() {
		if failed {
			b.span.SetStatus(codes.Error, "read failed")
		}
		b.span.End()
	})
}

// truncate cuts s to at most n bytes, the DNS name limit for hosts.
func truncate(s string, n int) string {
	if len(s) > n {
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}
