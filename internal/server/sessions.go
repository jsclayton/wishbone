package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/user"
)

// sessionBody is a session as the sessions list shows it. A session is a
// signed-in browser or app: a cookie session and a bearer token look the same.
// A session has no address of its own to GET, so the list is where its version
// is (see [sessionRow]).
type sessionBody struct {
	ID         string    `json:"id" example:"42" doc:"An opaque string. Don't parse it."`
	UserAgent  string    `json:"user_agent" doc:"The User-Agent that started the session. Empty when it sent none."`
	LastUsedAt time.Time `json:"last_used_at"`
	CreatedAt  time.Time `json:"created_at"`
	IsCurrent  bool      `json:"is_current" doc:"True for the session that makes this request."`
	CanDelete  bool      `json:"can_delete"`
}

// sessionRow is a session in the sessions collection: the session with its
// version. The version changes when the session is used.
type sessionRow struct {
	sessionBody
	Version string `json:"version" doc:"The version of this session as this caller sees it. Send it in If-Match on the DELETE."`
}

func newSessionBody(info session.Info, caller Caller, owner user.User) sessionBody {
	return sessionBody{
		ID:         formatID(info.ID),
		UserAgent:  info.UserAgent,
		LastUsedAt: info.LastUsedAt.UTC().Truncate(time.Second),
		CreatedAt:  info.CreatedAt.UTC().Truncate(time.Second),
		IsCurrent:  info.ID == caller.SessionID,
		CanDelete:  canActFor(caller.User, owner),
	}
}

type listSessionsInput struct {
	ID string `path:"id" doc:"A user's ID, or me for the caller."`
	PageParams
}

// sessionRules are the sort keys and the filters of a user's sessions.
var sessionRules = collectionRules{Name: "sessions", Sorts: session.SortKeys}

type revokeSessionInput struct {
	ID        string `path:"id" doc:"A user's ID, or me for the caller."`
	SessionID string `path:"session_id" doc:"A session's ID, or current for the caller's own."`
	IfMatch   string `header:"If-Match" doc:"The session's version as you loaded it. If the session changed since (it was used), the answer is 412 resource_changed and the session stays."`
}

type revokeSessionOutput struct {
	// SetCookie is empty, and so not sent, unless the caller revoked the
	// session behind their own cookie.
	SetCookie string `header:"Set-Cookie"`
}

func (s *Server) registerSessions(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-sessions",
		Method:      http.MethodGet,
		Path:        "/users/{id}/sessions",
		Summary:     "A user's sessions",
		Description: "The live sessions, cookie and bearer, the most recently used first. The user, their parents, and the admin see them.",
		Tags:        []string{"Sessions"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}
	sessionRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listSessionsInput) (*collectionOutput[sessionRow], error) {
		target, err := s.userToActFor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(sessionRules)
		if err != nil {
			return nil, err
		}
		infos, total, next, err := s.opts.Sessions.Page(ctx, target.ID, req)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[sessionRow]{}
		out.Body.Data = make([]sessionRow, len(infos))
		for i, info := range infos {
			body := newSessionBody(info, caller, target)
			version, err := versionOf(body)
			if err != nil {
				return nil, err
			}
			out.Body.Data[i] = sessionRow{sessionBody: body, Version: version}
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "revoke-session",
		Method:        http.MethodDelete,
		Path:          "/users/{id}/sessions/{session_id}",
		Summary:       "Revoke a session",
		Description:   "`DELETE /users/me/sessions/current` signs out. The user, their parents, and the admin revoke a user's sessions, while the user is active: for a deactivated user it is a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:          []string{"Sessions"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *revokeSessionInput) (*revokeSessionOutput, error) {
		target, err := s.userToWriteFor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		caller, _ := CallerFrom(ctx)
		sessionID, ok := parseID(in.SessionID)
		if !ok {
			return nil, notFound()
		}
		err = s.opts.Sessions.Revoke(ctx, target.ID, sessionID, func(ctx context.Context, q sqlx.QueryerContext, current session.Info) error {
			if err := stillMayWriteFor(ctx, q, target.ID); err != nil {
				return err
			}
			version, err := versionOf(newSessionBody(current, caller, target))
			if err != nil {
				return err
			}
			return checkIfMatch(in.IfMatch, version)
		})
		if errors.Is(err, session.ErrNone) {
			return nil, notFound()
		}
		if err != nil {
			return nil, err
		}
		out := &revokeSessionOutput{}
		if caller.ViaCookie && sessionID == caller.SessionID {
			out.SetCookie = clearedSessionCookie()
		}
		return out, nil
	})
}
