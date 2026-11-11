// Package server wires the HTTP router: the JSON API under /api/v2, a 410 for
// every retired API version, and the embedded single-page app for everything
// else.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"

	"jsclayton.dev/wishbone/internal/blocklist"
	"jsclayton.dev/wishbone/internal/clientip"
	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/ratelimit"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/setup"
	"jsclayton.dev/wishbone/internal/telemetry"
	"jsclayton.dev/wishbone/internal/user"
)

const (
	// apiPrefix is where the API lives. Routes in the route table
	// (docs/routes.md) are relative to it.
	apiPrefix = "/api/v2"
)

// activeAPIVersions lists the API versions that the server registers. A
// request to a version below the lowest one answers 410 (retiredAPIVersions).
// A version above the highest one is not registered, so it answers as any
// unknown path does. apiPrefix is the path of the highest version.
var activeAPIVersions = []int{2}

// Pinger is the slice of the store the health check needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options are the dependencies for New.
type Options struct {
	Logger  *slog.Logger
	Store   Pinger
	Version string
	Commit  string
	// Metrics serves GET /metrics. Nil leaves the route unregistered.
	Metrics http.Handler
	// Tracer records a span for each request. Nil turns tracing off.
	Tracer trace.TracerProvider

	// The domain services the routes call.
	Users      *user.Service
	Groups     *group.Service
	Blocked    *blocklist.Service
	Lists      *list.Service
	Items      *item.Service
	Occasions  *occasion.Service
	Gifts      *gift.Service
	Sessions   *session.Service
	Setup      *setup.Service
	LoginLinks *loginlink.Service
	Passkeys   *passkey.Service
	Images     *image.Service
	// Search reads a link or a photo for POST /search. Nil leaves everything
	// unread: a search then finds nothing, and counts nothing.
	Search Searcher

	// PublicURL is the instance's address (WISHBONE_PUBLIC_URL), with no
	// trailing slash. A login link's URL is this and /login/{token}.
	PublicURL string

	// ClientIP finds the client's address for the rate limit. Nil trusts no proxy.
	ClientIP *clientip.Resolver
	// RateLimitRedeem is how many attempts at a secret a client IP gets each
	// minute (WISHBONE_RATE_LIMIT_REDEEM). Zero means 10.
	RateLimitRedeem int
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Server is the root HTTP handler.
type Server struct {
	opts   Options
	router chi.Router
}

// New builds the router.
func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RateLimitRedeem < 1 {
		opts.RateLimitRedeem = defaultRedeemLimit
	}
	if opts.ClientIP == nil {
		opts.ClientIP, _ = clientip.New(nil)
	}

	r := chi.NewRouter()
	r.Use(telemetry.Middleware(opts.Tracer)) // outside the recoverer, so a panic shows as a 500
	r.Use(middleware.RequestID)
	r.Use(recoverer(opts.Logger))
	r.Use(securityHeaders)
	r.Use(apiNoStore)
	r.Use(requestLogger(opts.Logger))
	r.Use(rateLimit(redeemLimited, opts.ClientIP, ratelimit.New(opts.RateLimitRedeem, redeemWindow, opts.Now)))
	r.Use(rateLimit(checkLimited, opts.ClientIP, ratelimit.New(opts.RateLimitRedeem, redeemWindow, opts.Now)))

	s := &Server{opts: opts, router: r}
	r.Use(s.authenticate)
	r.Use(func(next http.Handler) http.Handler { return retiredAPIVersions(activeAPIVersions, next) })

	if opts.Metrics != nil {
		r.Method(http.MethodGet, "/metrics", opts.Metrics)
	}

	humaCfg := huma.DefaultConfig("wishbone", opts.Version)
	humaCfg.OpenAPIPath = apiPrefix + "/openapi"
	humaCfg.DocsPath = apiPrefix + "/docs"
	humaCfg.SchemasPath = apiPrefix + "/schemas"
	humaCfg.Components.SecuritySchemes = securitySchemes
	// A PATCH body is JSON Merge Patch. It is JSON, so it uses the JSON format.
	humaCfg.Formats[mergePatchContentType] = huma.DefaultJSONFormat
	api := humachi.New(r, humaCfg)
	// huma serves the document as openapi.json and openapi.yaml. The route
	// table also names the bare path.
	r.Get(humaCfg.OpenAPIPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/openapi+json")
		_ = json.NewEncoder(w).Encode(api.OpenAPI())
	})

	// A route goes on one of two groups. Routes on `public` need no sign-in. On
	// `signedIn`, a request with no valid session gets a 401, the operation is
	// documented with the two security schemes, and the route gets its caller
	// from CallerFrom. List 401 in the operation's Errors: huma reads Errors
	// before the group's modifier runs.
	public := huma.NewGroup(api, apiPrefix)
	signedIn := huma.NewGroup(api, apiPrefix)
	signedIn.UseSimpleModifier(func(op *huma.Operation) { op.Security = signedInSecurity })
	signedIn.UseMiddleware(requireSignedIn(api))

	s.registerHealth(public)
	s.registerSetup(public)
	s.registerUsers(signedIn)
	s.registerGroups(signedIn)
	s.registerBlockedStores(signedIn)
	s.registerLists(signedIn)
	s.registerInvitations(signedIn)
	s.registerItems(signedIn)
	s.registerGifts(signedIn)
	s.registerOccasions(signedIn)
	s.registerOccasionGroups(signedIn)
	s.registerStoreLinks(signedIn)
	s.registerImages(signedIn)
	s.registerSearch(signedIn)
	s.registerLoginLinks(public, signedIn)
	s.registerSessions(signedIn)
	s.registerPasskeys(signedIn)
	s.registerPasskeyLogin(public)

	spa := spaHandler()
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api" || strings.HasPrefix(req.URL.Path, "/api/") {
			writeProblem(w, http.StatusNotFound, "not_found", "There is nothing at this address.")
			return
		}
		spa.ServeHTTP(w, req)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "This address does not take this method.")
	})

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// retiredAPIVersions answers 410 Gone to every request under /api/v{N}, where
// N is a whole number below the lowest active version (docs/spec.md, Versions
// of the API). A client that still calls a retired version, such as a web app
// that a phone cached, gets a clear answer and can reload. Any other path goes
// on to next, and so does a version that is malformed (vx, v01, v-1) or at or
// above the lowest active one.
func retiredAPIVersions(active []int, next http.Handler) http.Handler {
	oldest := slices.Min(active)
	newest := slices.Max(active)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n, ok := apiVersionOf(r.URL.Path); ok && n < oldest {
			writeProblem(w, http.StatusGone, "api_version_removed",
				"Version "+strconv.Itoa(n)+" of the API has been removed. Use /api/v"+strconv.Itoa(newest)+".")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// apiVersionOf reads N from a path that is /api/v{N} or starts with /api/v{N}/.
// N is a whole number with no sign and no leading zero. Anything else is not a
// version.
func apiVersionOf(path string) (int, bool) {
	rest, ok := strings.CutPrefix(path, "/api/")
	if !ok {
		return 0, false
	}
	segment, _, _ := strings.Cut(rest, "/")
	digits, ok := strings.CutPrefix(segment, "v")
	if !ok || digits == "" || (len(digits) > 1 && digits[0] == '0') {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false // too large to be a version that was ever active
	}
	return n, true
}

type healthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
}

func (s *Server) registerHealth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
		Tags:        []string{"System"},
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		if err := s.opts.Store.Ping(ctx); err != nil {
			return nil, huma.Error503ServiceUnavailable("database unavailable")
		}
		out := &healthOutput{}
		out.Body.Status = "ok"
		out.Body.Version = s.opts.Version
		out.Body.Commit = s.opts.Commit
		return out, nil
	})
}

// contentSecurityPolicy is sent on every response. Why the less obvious
// directives are set this way:
//
//   - script-src, style-src and connect-src stay at 'self', so one script
//     injection cannot run or change what the service worker cached.
//   - img-src allows any http or https address, because the item form shows
//     the shop's own picture (offer.image_url) before the item is saved.
//     http: is there because extraction keeps http image URLs and the e2e
//     fixture serves its pictures over http. data: is for the QR code of a
//     login link. blob: is for a picture the member adds to an item that
//     isn't saved yet: the browser shows its own copy of the file until the
//     item is made and the picture uploads.
//   - worker-src and manifest-src fall back to 'self', which the service
//     worker and the web manifest need.
//   - /api/v2/docs sets its own policy with Header().Set, which replaces this
//     one, so the page can load its scripts. TestDocsPageKeepsHumasPolicy
//     keeps this true.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data: blob: http: https:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		// A page that opens Wishbone keeps no handle on the window, so it can't
		// steer it from one /add?url= load to the next.
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// apiNoStore sets Cache-Control: no-store on everything under /api/, so member
// data never depends on a browser's caching heuristics. It runs before the
// rate limiters, so 401s, 429s and Huma's errors carry it too. The image
// handlers replace it with private, no-cache and an ETag.
func apiNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// loggedPath is the path the request logger writes. A login link carries a
// usable token, so every path under /login/ logs as /login/{token}.
func loggedPath(path string) string {
	if strings.HasPrefix(path, "/login/") {
		return "/login/{token}"
	}
	return path
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.LogAttrs(r.Context(), slog.LevelDebug, "request",
				slog.String("method", r.Method),
				slog.String("path", loggedPath(r.URL.Path)),
				slog.Int("status", ww.Status()),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}
