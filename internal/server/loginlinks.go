package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/initials"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/user"
)

// invalidLinkDetail is the one answer for every link that doesn't work. It says
// nothing about why.
const invalidLinkDetail = "This login link is invalid or has expired."

func invalidLink() error {
	return Error(http.StatusUnauthorized, "login_link_invalid", invalidLinkDetail)
}

const (
	sessionKindCookie = "cookie"
	sessionKindBearer = "bearer"
)

type createLoginLinkOutput struct {
	Body struct {
		URL       string    `json:"url" example:"https://wishbone.example/login/Zk3p..." doc:"The link. It is in this response and nowhere else, because the server keeps only a hash of its token."`
		ExpiresAt time.Time `json:"expires_at"`
	}
}

type checkLoginLinkInput struct {
	Body struct {
		// No length rules, so an empty token gets the same answer as any other
		// bad link.
		Token string `json:"token" doc:"The token from the login link's URL, after /login/."`
	}
}

type checkLoginLinkOutput struct {
	Body struct {
		ExpiresAt      time.Time `json:"expires_at"`
		ForCurrentUser bool      `json:"for_current_user" doc:"True when the link is for the user whose session makes this request."`
	}
}

type redeemLoginLinkInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Token   string `json:"token" minLength:"1" doc:"The token from the login link's URL, after /login/."`
		Session string `json:"session,omitempty" required:"false" enum:"cookie,bearer" default:"cookie" doc:"How to start the session. cookie, for the web app, sets the session cookie. bearer, for a native app, puts the token in the body."`
	}
}

type redeemLoginLinkOutput struct {
	// SetCookie is empty, and so not sent, for a bearer session.
	SetCookie string `header:"Set-Cookie"`
	Body      struct {
		Token           string `json:"token,omitempty" required:"false" doc:"The new session's token. Only a bearer session has one, and this response is the only place it appears."`
		Name            string `json:"name" doc:"The first name of the user the link signs in."`
		Initials        string `json:"initials"`
		ReplacedSession bool   `json:"replaced_session" doc:"True when the server revoked the caller's current session of this kind."`
		SwitchedUser    bool   `json:"switched_user" doc:"True when that session was another user's."`
	}
}

func (s *Server) registerLoginLinks(public, signedIn huma.API) {
	huma.Register(signedIn, huma.Operation{
		OperationID:   "create-login-link",
		Method:        http.MethodPost,
		Path:          "/users/{id}/login-links",
		Summary:       "Make a login link",
		Description:   "`me` stands for the caller. A login link has no address, so the response has no `Location` header. The link works once and expires after 7 days. The user, their parents, and the admin make one.",
		Tags:          []string{"Login links"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *userInput) (*createLoginLinkOutput, error) {
		target, err := s.userToWriteFor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		token, expires, err := s.opts.LoginLinks.Create(ctx, target.ID)
		if err != nil {
			return nil, err
		}
		out := &createLoginLinkOutput{}
		out.Body.URL = s.opts.PublicURL + "/login/" + token
		out.Body.ExpiresAt = expires.UTC().Truncate(time.Second)
		return out, nil
	})

	huma.Register(public, huma.Operation{
		OperationID: "check-login-link",
		Method:      http.MethodPost,
		Path:        "/login-links/check",
		Summary:     "Check a login link without redeeming it",
		Description: "Says when the link expires and whether it is for the user signed in on this request. It never names a user or uses up the link. Like any request with a session, it moves that session forward. It has a rate limit of its own.",
		Tags:        []string{"Login links"},
		Errors:      []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *checkLoginLinkInput) (*checkLoginLinkOutput, error) {
		var currentUser int64
		if caller, ok := CallerFrom(ctx); ok {
			currentUser = caller.User.ID
		}
		check, err := s.opts.LoginLinks.Check(ctx, in.Body.Token, currentUser)
		if errors.Is(err, loginlink.ErrInvalid) {
			return nil, invalidLink()
		}
		if err != nil {
			return nil, err
		}
		out := &checkLoginLinkOutput{}
		out.Body.ExpiresAt = check.ExpiresAt.Truncate(time.Second)
		out.Body.ForCurrentUser = check.ForCurrentUser
		return out, nil
	})

	huma.Register(public, huma.Operation{
		OperationID: "redeem-login-link",
		Method:      http.MethodPost,
		Path:        "/login-links/redeem",
		Summary:     "Redeem a login link and sign in",
		Description: "Uses the link and starts a session. If the caller's current session is of the same kind as the new one, the server revokes it. Attempts count against `WISHBONE_RATE_LIMIT_REDEEM`.",
		Tags:        []string{"Login links"},
		Errors:      []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *redeemLoginLinkInput) (*redeemLoginLinkOutput, error) {
		kind := in.Body.Session
		if kind == "" {
			kind = sessionKindCookie
		}
		// The caller's current session is replaced only by a new session of its
		// own kind: a cookie caller redeeming for a bearer token keeps the cookie.
		var current int64
		if caller, ok := CallerFrom(ctx); ok && caller.ViaCookie == (kind == sessionKindCookie) {
			current = caller.SessionID
		}
		res, err := s.opts.LoginLinks.Redeem(ctx, in.Body.Token, current, in.UserAgent)
		if errors.Is(err, loginlink.ErrInvalid) {
			return nil, invalidLink()
		}
		if err != nil {
			return nil, err
		}
		out := &redeemLoginLinkOutput{}
		if kind == sessionKindBearer {
			out.Body.Token = res.Token
		} else {
			c := newSessionCookie(res.Token, res.Session.ExpiresAt)
			out.SetCookie = c.String()
		}
		last := ""
		if res.User.LastName != nil {
			last = *res.User.LastName
		}
		out.Body.Name = res.User.Name
		out.Body.Initials = initials.Of(res.User.Name, last)
		out.Body.ReplacedSession = res.Replaced
		out.Body.SwitchedUser = res.Switched
		return out, nil
	})
}

// actsFor says whether the caller holds a role through which they may act for a
// user: the user, their parents, and the admin. These roles give every read of
// the user, whether or not the user is active (docs/routes.md, Roles).
// target comes with its parents (see [user.Service.Get]). Only a direct parent
// counts: a parent's parent has no power over the child.
func actsFor(caller, target user.User) bool {
	return caller.ID == target.ID || caller.IsAdmin || target.HasParent(caller.ID)
}

// canActFor is the one rule of the power to act for a user: make them a login
// link, manage their sessions, delete their passkeys, and edit their profile.
// The user, their parents, and the admin have it ([actsFor]) while the user is
// active. A deactivated user's roles give no write, and every `can_*` through
// them is false. Every route that writes through such a role, and every `can_*`
// field, calls this function and repeats nothing.
func canActFor(caller, target user.User) bool {
	return actsFor(caller, target) && target.Active()
}

// userDeactivated is the answer to a write through a deactivated user.
func userDeactivated() error {
	return Error(http.StatusForbidden, "user_deactivated", "This user is deactivated, so you can't do this for them until the admin reactivates them.")
}

// userToActFor loads the user at a path that the caller reads sessions and
// passkeys of. A user the caller has no role for looks the same as one who
// doesn't exist. The roles read a deactivated user too.
func (s *Server) userToActFor(ctx context.Context, rawID string) (user.User, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return user.User{}, notFound()
	}
	u, err := s.opts.Users.Get(ctx, id)
	if errors.Is(err, user.ErrNotFound) || (err == nil && !actsFor(caller.User, u)) {
		return user.User{}, notFound()
	}
	return u, err
}

// writeAccess says whether the caller may write through a role for the user.
// It is nil when they may ([canActFor]), a 404 when they have no role for the
// user, and a 403 user_deactivated when they have one and the user is
// deactivated. A route checks it when it loads the user ([Server.userToWriteFor])
// and again inside the write's transaction ([Server.stillMayWriteFor]).
func writeAccess(caller, target user.User) error {
	switch {
	case !actsFor(caller, target):
		return notFound()
	case !target.Active():
		return userDeactivated()
	}
	return nil
}

// userToWriteFor is [Server.userToActFor] for a write: a deactivated user is a
// 403 user_deactivated.
func (s *Server) userToWriteFor(ctx context.Context, rawID string) (user.User, error) {
	u, err := s.userToActFor(ctx, rawID)
	if err != nil {
		return user.User{}, err
	}
	return u, writeAccess(callerUser(ctx), u)
}

// stillMayWriteFor loads the user again inside the transaction of a write, and
// checks [writeAccess] against what it finds. Between the route's first look and
// its write, an unlink or a deactivation can commit, and the write must not
// succeed after it. q is the transaction.
func stillMayWriteFor(ctx context.Context, q sqlx.QueryerContext, id int64) error {
	current, err := user.Get(ctx, q, id)
	if errors.Is(err, user.ErrNotFound) {
		return notFound()
	}
	if err != nil {
		return err
	}
	return writeAccess(callerUser(ctx), current)
}

// callerUser is the user who makes the request.
func callerUser(ctx context.Context) user.User {
	c, _ := CallerFrom(ctx)
	return c.User
}
