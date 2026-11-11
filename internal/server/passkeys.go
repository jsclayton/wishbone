package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/user"
)

// passkeyBody is a passkey as the passkeys list shows it. A passkey is a part
// of its user: its address is under the user's, and it has no GET of its own.
// The list is where its version is (see [passkeyRow]).
type passkeyBody struct {
	ID         string     `json:"id" example:"42" doc:"An opaque string. Don't parse it."`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at" nullable:"true" doc:"Null until the passkey signs in."`
	CanEdit    bool       `json:"can_edit" doc:"True when the caller can rename the passkey: only the user."`
	CanDelete  bool       `json:"can_delete" doc:"True when the caller can delete the passkey."`
}

// passkeyRow is a passkey in the passkeys collection: the passkey with its
// version. The version changes when the passkey is renamed or signs in.
type passkeyRow struct {
	passkeyBody
	Version string `json:"version" doc:"The same value as the ETag of the passkey for this caller. Send it in If-Match."`
}

// canEditPasskey says whether the caller may rename the passkeys of a user:
// only the user, while they are active. Nobody else adds or renames a passkey.
func canEditPasskey(caller, owner user.User) bool {
	return caller.ID == owner.ID && owner.Active()
}

// canDeletePasskey says whether the caller may delete the passkeys of a user:
// the user, their parents, and the admin.
func canDeletePasskey(caller, owner user.User) bool {
	return canActFor(caller, owner)
}

func newPasskeyBody(p passkey.Passkey, caller, owner user.User) passkeyBody {
	b := passkeyBody{
		ID:        formatID(p.ID),
		Name:      p.Name,
		CreatedAt: p.CreatedAt.UTC().Truncate(time.Second),
		CanEdit:   canEditPasskey(caller, owner),
		CanDelete: canDeletePasskey(caller, owner),
	}
	if p.LastUsedAt != nil {
		t := p.LastUsedAt.UTC().Truncate(time.Second)
		b.LastUsedAt = &t
	}
	return b
}

func passkeyPath(owner, id int64) string {
	return "/users/" + formatID(owner) + "/passkeys/" + formatID(id)
}

// Why each passkey problem has its own code: a client acts on them. The
// registration ones are the browser's answer to the options, so they say what
// to do next, not what the server checked.
const (
	passkeyNameDetail = "Enter a name of 1 to 100 characters."
)

func passkeyProblem(err error) error {
	switch {
	case errors.Is(err, passkey.ErrNoChallenge):
		return Error(http.StatusBadRequest, "passkey_registration_expired", "The passkey request expired. Try again.")
	case errors.Is(err, passkey.ErrVerification):
		return Error(http.StatusBadRequest, "passkey_not_verified", "That passkey could not be verified.")
	case errors.Is(err, passkey.ErrDuplicate):
		return Error(http.StatusConflict, "passkey_already_added", "That passkey is already added.")
	case errors.Is(err, passkey.ErrInvalidName):
		return Error(http.StatusUnprocessableEntity, "invalid_input", passkeyNameDetail,
			&huma.ErrorDetail{Location: "body.name", Message: passkeyNameDetail})
	case errors.Is(err, passkey.ErrNotFound):
		return notFound()
	}
	return err
}

type listPasskeysInput struct {
	ID string `path:"id" doc:"A user's ID, or me for the caller."`
	PageParams
}

// passkeyRules are the sort keys and the filters of a user's passkeys.
var passkeyRules = collectionRules{Name: "passkeys", Sorts: passkey.SortKeys}

type finishPasskeyInput struct {
	ID   string `path:"id" doc:"A user's ID, or me for the caller."`
	Body struct {
		Name       string          `json:"name,omitempty" required:"false" maxLength:"100" doc:"Optional. Defaults to \"Passkey\"."`
		Credential json.RawMessage `json:"credential" doc:"The credential from navigator.credentials.create, as JSON."`
	}
}

// passkeyOwner loads the user at a path, for a caller who adds or renames a
// passkey for them: only the user. The admin and a parent see the user, so for
// them it is a 403 forbidden and not a 404, also while the user is deactivated:
// the role is what stops them, not the deactivation. A deactivated user can't sign
// in to do it themselves, so the 403 user_deactivated below is for a request that
// raced the deactivation.
func (s *Server) passkeyOwner(ctx context.Context, rawID string) (user.User, error) {
	target, err := s.userToActFor(ctx, rawID)
	if err != nil {
		return user.User{}, err
	}
	caller, _ := CallerFrom(ctx)
	switch {
	case caller.User.ID != target.ID:
		return user.User{}, forbidden("Only the user adds or renames their own passkeys.")
	case !target.Active():
		return user.User{}, userDeactivated()
	}
	return target, nil
}

// passkeyChanges are the fields of a passkey that the user may edit with PATCH.
type passkeyChanges struct {
	Name Field[string] `json:"name,omitempty" minLength:"1" maxLength:"100" doc:"The name. It can't be null or blank."`
}

// passkeyChangeKeys are the keys of passkeyChanges.
var passkeyChangeKeys = []string{"name"}

type patchPasskeyInput struct {
	ID        string `path:"id" doc:"A user's ID, or me for the caller."`
	PasskeyID string `path:"passkey_id"`
	IfMatch   string `header:"If-Match" doc:"The ETag (or a row's version) of the passkey as you loaded it. If the passkey changed since, the answer is 412 resource_changed and nothing changes."`
	Body      MergePatch[passkeyChanges]
}

type deletePasskeyInput struct {
	ID        string `path:"id" doc:"A user's ID, or me for the caller."`
	PasskeyID string `path:"passkey_id"`
	IfMatch   string `header:"If-Match" doc:"The ETag (or a row's version) of the passkey as you loaded it. If the passkey changed since, the answer is 412 resource_changed and nothing is deleted."`
}

func (s *Server) registerPasskeys(api huma.API) {
	svc := s.opts.Passkeys

	listOp := huma.Operation{
		OperationID: "list-passkeys",
		Method:      http.MethodGet,
		Path:        "/users/{id}/passkeys",
		Summary:     "A user's passkeys",
		Description: "The oldest first. The user, their parents, and the admin see them.",
		Tags:        []string{"Passkeys"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}
	passkeyRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listPasskeysInput) (*collectionOutput[passkeyRow], error) {
		target, err := s.userToActFor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(passkeyRules)
		if err != nil {
			return nil, err
		}
		passkeys, total, next, err := svc.Page(ctx, target.ID, req)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[passkeyRow]{}
		out.Body.Data = make([]passkeyRow, len(passkeys))
		for i, p := range passkeys {
			body := newPasskeyBody(p, caller.User, target)
			version, err := versionOf(body)
			if err != nil {
				return nil, err
			}
			out.Body.Data[i] = passkeyRow{passkeyBody: body, Version: version}
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID: "begin-passkey-registration",
		Method:      http.MethodPost,
		Path:        "/users/{id}/passkeys/registration",
		Summary:     "Start adding a passkey",
		Description: "Only the user. Returns the options for navigator.credentials.create. Finish with the browser's answer within 5 minutes.",
		Tags:        []string{"Passkeys"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *userInput) (*struct{ Body json.RawMessage }, error) {
		target, err := s.passkeyOwner(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		opts, err := svc.BeginRegistration(ctx, target.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body json.RawMessage }{Body: opts}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "finish-passkey-registration",
		Method:        http.MethodPost,
		Path:          "/users/{id}/passkeys",
		Summary:       "Finish adding a passkey",
		Description:   "Only the user. Verifies the browser's answer to the registration options and stores the passkey.",
		Tags:          []string{"Passkeys"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *finishPasskeyInput) (*createdOutput[passkeyBody], error) {
		target, err := s.passkeyOwner(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		caller, _ := CallerFrom(ctx)
		p, err := svc.FinishRegistration(ctx, target.ID, in.Body.Name, in.Body.Credential)
		if err != nil {
			return nil, passkeyProblem(err)
		}
		return createdOf(passkeyPath(target.ID, p.ID), newPasskeyBody(p, caller.User, target))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-passkey",
		Method:      http.MethodPatch,
		Path:        "/users/{id}/passkeys/{passkey_id}",
		Summary:     "Rename a passkey",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. Only the user renames a passkey. " +
			"A field the caller sees but can't change is a 403 `field_not_writable`. `If-Match` is optional.",
		Tags:   []string{"Passkeys"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchPasskeyInput) (*resourceOutput[passkeyBody], error) {
		target, err := s.passkeyOwner(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		passkeyID, ok := parseID(in.PasskeyID)
		if !ok {
			return nil, notFound()
		}
		caller, _ := CallerFrom(ctx)
		visible, err := keysOf(newPasskeyBody(passkey.Passkey{}, caller.User, target))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(passkeyChangeKeys, visible); err != nil {
			return nil, err
		}
		updated, err := svc.Update(ctx, target.ID, passkeyID, passkey.Changes{Name: in.Body.Changes.Name.Optional}, func(current passkey.Passkey) error {
			v, err := versionOf(newPasskeyBody(current, caller.User, target))
			if err != nil {
				return err
			}
			return checkIfMatch(in.IfMatch, v)
		})
		if err != nil {
			return nil, passkeyProblem(err)
		}
		return resourceOf(newPasskeyBody(updated, caller.User, target))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-passkey",
		Method:        http.MethodDelete,
		Path:          "/users/{id}/passkeys/{passkey_id}",
		Summary:       "Delete a passkey",
		Description:   "The user, their parents, and the admin delete a passkey, while the user is active: for a deactivated user it is a 403 `user_deactivated`. The user can then no longer sign in with it. `If-Match` is optional.",
		Tags:          []string{"Passkeys"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deletePasskeyInput) (*struct{}, error) {
		target, err := s.userToWriteFor(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		passkeyID, ok := parseID(in.PasskeyID)
		if !ok {
			return nil, notFound()
		}
		caller, _ := CallerFrom(ctx)
		err = svc.Remove(ctx, target.ID, passkeyID, func(ctx context.Context, q sqlx.QueryerContext, current passkey.Passkey) error {
			if err := stillMayWriteFor(ctx, q, target.ID); err != nil {
				return err
			}
			v, err := versionOf(newPasskeyBody(current, caller.User, target))
			if err != nil {
				return err
			}
			return checkIfMatch(in.IfMatch, v)
		})
		if err != nil {
			return nil, passkeyProblem(err)
		}
		return nil, nil
	})

	acceptMergePatch(api, "/users/{id}/passkeys/{passkey_id}")
}
