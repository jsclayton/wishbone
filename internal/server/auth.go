package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/clientip"
	"jsclayton.dev/wishbone/internal/ratelimit"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/user"
)

// sessionCookieName is the name of the session cookie. Huma struct tags can't
// use a constant, so keep any `cookie:"wishbone_session"` tag in sync.
const sessionCookieName = "wishbone_session"

// Caller is the signed-in user behind a request, and the session they used.
// The sign-in middleware finds it before any route runs. A route gets it with
// [CallerFrom] and never reads the cookie or the Authorization header.
type Caller struct {
	User      user.User
	SessionID int64
	// ViaCookie is true when the session came in the cookie, and false when it
	// came as a bearer token. Only cookie sessions have a cookie to renew or clear.
	ViaCookie bool
}

type callerKey struct{}

// CallerFrom returns the caller of a request, and false when nobody is signed
// in. A route registered on the signed-in group always has one.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// requireAdmin is the one check of a route that only the admin may call: it
// refuses any other caller with a 403 forbidden. what finishes the sentence
// "Only the admin ...". A route that also loads a resource for the caller loads
// it first, so a resource the caller can't see is still a 404.
func requireAdmin(ctx context.Context, what string) error {
	if caller, _ := CallerFrom(ctx); !caller.User.IsAdmin {
		return forbidden("Only the admin " + what + ".")
	}
	return nil
}

// credentials returns the session token of a request. A bearer token wins over
// the cookie: if the request has one, the cookie is not looked at, even when the
// token turns out to be no good.
func credentials(r *http.Request) (token string, viaCookie bool) {
	if scheme, rest, ok := strings.Cut(r.Header.Get("Authorization"), " "); ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(rest), false
	}
	if c, err := r.Cookie(sessionCookieName); err == nil {
		return c.Value, true
	}
	return "", false
}

// resolveAliases replaces the reserved IDs in a path with the real ones: `me`
// after `users` is the caller's user ID, and `current` after `sessions` is the
// caller's session ID. Routes never see `me` or `current`.
func resolveAliases(path string, c Caller) string {
	segs := strings.Split(path, "/")
	for i := 1; i < len(segs); i++ {
		switch {
		case segs[i-1] == "users" && segs[i] == "me":
			segs[i] = strconv.FormatInt(c.User.ID, 10)
		case segs[i-1] == "sessions" && segs[i] == "current":
			segs[i] = strconv.FormatInt(c.SessionID, 10)
		}
	}
	return strings.Join(segs, "/")
}

// authenticate is the one place that finds the caller. It runs before routing,
// for every API path, so that the reserved IDs are resolved before a route
// matches. It never refuses a request: a request with no valid session goes on
// with no caller, and a route that needs one answers 401 (see requireSignedIn).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, viaCookie := credentials(r)
		if token == "" || !strings.HasPrefix(r.URL.Path, apiPrefix+"/") {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		sess, err := s.opts.Sessions.Authenticate(ctx, token)
		if errors.Is(err, session.ErrNone) {
			// No Set-Cookie to clear the cookie: a late 401 from an old tab would
			// remove the cookie of a session signed in since.
			next.ServeHTTP(w, r)
			return
		}
		var u user.User
		if err == nil {
			u, err = s.opts.Users.Get(ctx, sess.UserID)
		}
		if errors.Is(err, user.ErrNotFound) {
			// The session outlived its user. It is no session.
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			s.opts.Logger.ErrorContext(ctx, "find the caller failed", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "internal_error", "Something went wrong on our side. Try again.")
			return
		}
		caller := Caller{User: u, SessionID: sess.ID, ViaCookie: viaCookie}

		if viaCookie {
			// The cookie lasts a year from when it was sent. A session lasts a year
			// from its last use, so the cookie is sent again, but at most once a day.
			renew, err := s.opts.Sessions.ClaimCookieRenewal(ctx, sess.ID)
			if err != nil {
				s.opts.Logger.ErrorContext(ctx, "renew the session cookie failed", slog.Any("error", err))
			} else if renew {
				c := newSessionCookie(token, sess.ExpiresAt)
				w.Header().Add("Set-Cookie", c.String())
			}
		}

		r = r.Clone(context.WithValue(ctx, callerKey{}, caller))
		r.URL.Path = resolveAliases(r.URL.Path, caller)
		r.URL.RawPath = ""
		next.ServeHTTP(w, r)
	})
}

// requireSignedIn answers 401 on an operation when the request has no caller.
// The signed-in group applies it to every operation registered on it.
func requireSignedIn(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if _, ok := CallerFrom(ctx.Context()); !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "Sign in to do this.")
			return
		}
		next(ctx)
	}
}

// signedInSecurity is the security requirement of a route that needs a caller.
// Either scheme will do.
var signedInSecurity = []map[string][]string{{"cookieAuth": {}}, {"bearerAuth": {}}}

var securitySchemes = map[string]*huma.SecurityScheme{
	"cookieAuth": {Type: "apiKey", In: "cookie", Name: sessionCookieName, Description: "The session cookie. The web app signs in with it."},
	"bearerAuth": {Type: "http", Scheme: "bearer", Description: "A session token in the Authorization header. A native app signs in with it. It wins over the cookie."},
}

// newSessionCookie builds the session cookie. Every use of a session moves its
// end forward to a full lifetime, so Max-Age is always the lifetime.
func newSessionCookie(token string, expires time.Time) http.Cookie {
	return http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(session.Lifetime / time.Second),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// clearedSessionCookie tells the browser to drop the session cookie.
func clearedSessionCookie() string {
	c := http.Cookie{
		Name:     sessionCookieName,
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	return c.String()
}

const (
	defaultRedeemLimit = 10
	redeemWindow       = time.Minute
)

// redeemLimited are the signed-out routes that share one limit per client IP,
// WISHBONE_RATE_LIMIT_REDEEM, because each one lets a guess at a secret through:
// the setup code, a login link, and a passkey sign-in (both of its routes).
var redeemLimited = map[string]bool{
	http.MethodPost + " " + apiPrefix + "/setup":              true,
	http.MethodPost + " " + apiPrefix + "/login-links/redeem": true,
	http.MethodPost + " " + passkeyOptionsPath:                true,
	http.MethodPost + " " + passkeyLoginPath:                  true,
}

// checkLimited is the route that checks a login link. It has a limit of its
// own, of the same size: a login link page checks before it redeems,
// and the check must not use up the attempts of a person who is signing in.
var checkLimited = map[string]bool{
	http.MethodPost + " " + apiPrefix + "/login-links/check": true,
}

// rateLimit counts each attempt at one of the routes against the client's IP
// before the route runs, and answers 429 over the limit.
func rateLimit(routes map[string]bool, ips *clientip.Resolver, l *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if routes[r.Method+" "+r.URL.Path] && !l.Allow(ips.IP(r)) {
				w.Header().Set("Retry-After", strconv.Itoa(int(redeemWindow/time.Second)))
				writeProblem(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Wait a minute and try again.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
