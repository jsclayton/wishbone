package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/session"
)

const (
	passkeyOptionsPath = apiPrefix + "/login/passkey/options" //nolint:gosec // a URL path, not a credential
	passkeyLoginPath   = apiPrefix + "/login/passkey"         //nolint:gosec // a URL path, not a credential

	// passkeyLoginFailed is the one message for every failed sign-in. It must
	// say nothing about which user, or which check, failed.
	passkeyLoginFailed = "That passkey didn't work. Use a login link instead."
)

func passkeyLoginProblem() error {
	return Error(http.StatusUnauthorized, "passkey_login_failed", passkeyLoginFailed)
}

type passkeyLoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Credential json.RawMessage `json:"credential" doc:"The credential from navigator.credentials.get, as JSON."`
	}
}

type passkeyLoginOutput struct {
	SetCookie string `header:"Set-Cookie"`
}

// registerPasskeyLogin adds sign-in with a passkey. Both routes work with no
// session, and the redeem rate limit covers both (see redeemLimited).
func (s *Server) registerPasskeyLogin(api huma.API) {
	svc := s.opts.Passkeys

	huma.Register(api, huma.Operation{
		OperationID: "begin-passkey-login",
		Method:      http.MethodPost,
		Path:        "/login/passkey/options",
		Summary:     "Start signing in with a passkey",
		Description: "Returns the options for navigator.credentials.get. They name no user. Finish within 5 minutes. Attempts count against `WISHBONE_RATE_LIMIT_REDEEM`.",
		Tags:        []string{"Passkeys"},
		Errors:      []int{http.StatusTooManyRequests},
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body json.RawMessage }, error) {
		opts, err := svc.BeginLogin(ctx)
		if err != nil {
			return nil, err
		}
		return &struct{ Body json.RawMessage }{Body: opts}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "finish-passkey-login",
		Method:        http.MethodPost,
		Path:          "/login/passkey",
		Summary:       "Sign in with a passkey",
		Description:   "Starts a cookie session. A passkey sign-in never gives a bearer token. If the caller's current session is a cookie session, the server revokes it. Every failure gets the same answer. Attempts count against `WISHBONE_RATE_LIMIT_REDEEM`.",
		Tags:          []string{"Passkeys"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *passkeyLoginInput) (*passkeyLoginOutput, error) {
		userID, err := svc.FinishLogin(ctx, in.Body.Credential)
		if errors.Is(err, passkey.ErrLoginFailed) {
			return nil, passkeyLoginProblem()
		}
		if err != nil {
			return nil, err
		}
		// Only a cookie session is replaced: this sign-in starts a cookie session.
		var current int64
		if caller, ok := CallerFrom(ctx); ok && caller.ViaCookie {
			current = caller.SessionID
		}
		token, sess, _, err := s.opts.Sessions.SignIn(ctx, userID, current, in.UserAgent)
		if errors.Is(err, session.ErrUserDeactivated) {
			// A deactivation that committed after the passkey was checked.
			return nil, passkeyLoginProblem()
		}
		if err != nil {
			return nil, err
		}
		c := newSessionCookie(token, sess.ExpiresAt)
		return &passkeyLoginOutput{SetCookie: c.String()}, nil
	})
}
