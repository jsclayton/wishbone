package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/user"
)

// ListSummary is a list as another resource holds it (docs/routes.md,
// Summary shapes): a user's `primary_list`. A summary doesn't make the list
// visible.
type ListSummary struct {
	ID   string `json:"id" example:"7" doc:"An opaque string. Don't parse it."`
	Name string `json:"name"`
}

// ListBody is the full list (docs/routes.md, List fields). Its invitations
// and its recipients, as resources, are in invitations.go.
type ListBody struct {
	ID          string        `json:"id" example:"7" doc:"An opaque string. Don't parse it."`
	Name        string        `json:"name" doc:"A primary list's name is its user's name until a user who can edit the list renames it."`
	Description *string       `json:"description" nullable:"true" doc:"Markdown about the list, or null."`
	IsPrimary   bool          `json:"is_primary" doc:"True for the list that every user has from the start, with that user as its only recipient."`
	Recipients  []UserSummary `json:"recipients" nullable:"false" doc:"The users the gifts are for, in the order they became recipients."`
	ItemCount   int           `json:"item_count" doc:"How many items on the list the caller can see."`
	CreatedAt   time.Time     `json:"created_at"`

	// The `can_*` fields are the caller's.
	CanEdit   bool `json:"can_edit" doc:"True when the caller can edit the list: rename it, change its description, and add, change, reorder and remove its items."`
	CanDelete bool `json:"can_delete" doc:"True when the list is an extra list with one recipient and the caller can edit it."`
	CanInvite bool `json:"can_invite" doc:"True when the list is an extra list and the caller is a recipient."`
	CanLeave  bool `json:"can_leave" doc:"True when the list has more than one recipient and the caller is one of them."`
}

// listRow is a list in the lists collection: the full list with its version.
type listRow struct {
	ListBody
	Version string `json:"version" doc:"The same value as the ETag of GET /lists/{id} for this caller. Send it in If-Match."`
}

func newListBody(l list.List, caller user.User) ListBody {
	return ListBody{
		ID:          formatID(l.ID),
		Name:        l.Name,
		Description: l.Description,
		IsPrimary:   l.IsPrimary,
		Recipients:  userSummaries(l.Recipients),
		ItemCount:   l.ItemCount,
		CreatedAt:   l.CreatedAt.UTC().Truncate(time.Second),

		CanEdit:   l.CanEdit,
		CanDelete: l.CanDelete(),
		CanInvite: l.CanInvite(caller),
		CanLeave:  l.CanLeave(caller),
	}
}

func newListRows(lists []list.List, caller user.User) ([]listRow, error) {
	rows := make([]listRow, len(lists))
	for i, l := range lists {
		body := newListBody(l, caller)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = listRow{ListBody: body, Version: v}
	}
	return rows, nil
}

// cannotEdit is the answer to a caller who sees a list and can't edit it: a 403
// user_deactivated when the only thing that stops them is a deactivated child they
// are the parent of, and a 403 forbidden otherwise ([list.NotEditable]).
func (s *Server) cannotEdit(ctx context.Context, listID int64, detail string) error {
	err := s.opts.Lists.NotEditable(ctx, callerUser(ctx), listID)
	if errors.Is(err, list.ErrRecipientDeactivated) {
		return userDeactivated()
	}
	if err != nil && !errors.Is(err, list.ErrNotEditable) {
		return err
	}
	return forbidden(detail)
}

// listProblem turns an error of the list package into a problem with a code.
func listProblem(err error) error {
	var field *list.FieldError
	switch {
	case errors.Is(err, list.ErrNotFound):
		return notFound()
	case errors.Is(err, list.ErrNotEditable):
		return forbidden("You can't edit this list.")
	case errors.Is(err, list.ErrRecipientNotFound):
		return referenceNotFound("body.recipient_id", "That user doesn't exist, or you can't see them.")
	case errors.Is(err, list.ErrRecipientNotAllowed):
		return Error(http.StatusForbidden, "reference_not_allowed", "You can make a list for yourself or for your child.")
	case errors.Is(err, list.ErrRecipientDeactivated):
		return userDeactivated()
	case errors.Is(err, list.ErrPrimaryList):
		return Error(http.StatusForbidden, "primary_list", "A primary list has one recipient, its user. Nobody deletes it, leaves it, or is invited to it.")
	case errors.Is(err, list.ErrLastRecipient):
		return Error(http.StatusForbidden, "last_recipient", "The last recipient of a list can't leave it. Delete the list instead.")
	case errors.Is(err, list.ErrNotYours):
		return forbidden("Only the recipient or their parent can make them leave.")
	case errors.Is(err, list.ErrNotRecipient):
		return forbidden("Only a recipient can invite.")
	case errors.Is(err, list.ErrNotInvitee):
		return forbidden("Only the invited user or their parent can do this.")
	case errors.Is(err, list.ErrInvitationNotFound):
		return notFound()
	case errors.Is(err, list.ErrInviteeNotFound):
		return referenceNotFound("body.user_id", "That user doesn't exist, or you can't see them.")
	case errors.Is(err, list.ErrInviteeNotAllowed):
		return Error(http.StatusForbidden, "reference_not_allowed", "You can only invite a user you see, and you see this one only as the admin.")
	case errors.Is(err, list.ErrInviteeDeactivated):
		return userDeactivated()
	case errors.Is(err, list.ErrAlreadyInvited):
		return Error(http.StatusConflict, "already_invited", "That user already has an invitation to this list.")
	case errors.Is(err, list.ErrAlreadyRecipient):
		return Error(http.StatusConflict, "already_recipient", "That user is already a recipient of this list.")
	case errors.Is(err, list.ErrHasOtherRecipients):
		return Error(http.StatusForbidden, "list_has_other_recipients", "A list with other recipients can't be deleted. Its recipients leave until one is left.")
	case errors.As(err, &field):
		return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(),
			&huma.ErrorDetail{Location: "body." + field.Field, Message: err.Error()})
	}
	return err
}

type listInput struct {
	ID string `path:"id" example:"7" doc:"A list's ID."`
}

type createListInput struct {
	Body struct {
		Name        string  `json:"name" minLength:"1" maxLength:"100" doc:"The list's name."`
		Description *string `json:"description,omitempty" required:"false" nullable:"true" maxLength:"2000" doc:"Markdown about the list. null or a blank string is none."`
		RecipientID *string `json:"recipient_id,omitempty" required:"false" nullable:"true" example:"7" doc:"The user the list is for: the caller (the default) or one of their children."`
	}
}

type deleteListInput struct {
	ID      string `path:"id" example:"7" doc:"A list's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the list as you loaded it. If the list changed since, the answer is 412 resource_changed and nothing is deleted."`
}

type listListsInput struct {
	PageParams
	RecipientIDs []string            `query:"recipient_id,explode" doc:"Only the lists that have this user among their recipients. Repeat it for any of several users. Not a field of the list."`
	CanEdit      OptionalParam[bool] `query:"can_edit" doc:"true keeps the lists the caller can edit, false the ones they can't."`
	IsPrimary    OptionalParam[bool] `query:"is_primary" doc:"true keeps the primary lists, false the extra lists."`
}

var listRules = collectionRules{Name: "lists", Sorts: list.SortKeys, Filters: []string{"recipient_id", "can_edit", "is_primary"}}

// listChanges are the fields of a list that a caller who can edit it may change
// with PATCH. The keys of the other fields in a list's body are refused with 403
// field_not_writable.
type listChanges struct {
	Name        Field[string] `json:"name,omitempty" minLength:"1" maxLength:"100" doc:"The list's name. It can't be null. Renaming a primary list ends its following of the user's name."`
	Description Field[string] `json:"description,omitempty" nullable:"true" maxLength:"2000" doc:"Markdown. null or an empty string clears it."`
}

var listChangeKeys = []string{"name", "description"}

type patchListInput struct {
	ID      string `path:"id" example:"7" doc:"A list's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the list as you loaded it. If the list changed since, the answer is 412 resource_changed and nothing changes."`
	Body    MergePatch[listChanges]
}

func (c listChanges) changes() list.Changes {
	return list.Changes{Name: c.Name.Optional, Description: c.Description.Optional}
}

// listVersionGuard compares the If-Match header with the version of the list
// as the caller sees it, inside the write's transaction.
func listVersionGuard(caller user.User, ifMatch string) list.Guard {
	return func(_ context.Context, _ sqlx.ExtContext, current list.List) error {
		v, err := versionOf(newListBody(current, caller))
		if err != nil {
			return err
		}
		return checkIfMatch(ifMatch, v)
	}
}

// visibleList loads the list at a path, for a caller who must be able to see
// it. A list the caller can't see looks the same as one that doesn't exist.
func (s *Server) visibleList(ctx context.Context, rawID string) (list.List, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return list.List{}, notFound()
	}
	l, err := s.opts.Lists.GetVisible(ctx, caller.User, id)
	if err != nil {
		return list.List{}, listProblem(err)
	}
	return l, nil
}

func (s *Server) registerLists(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-lists",
		Method:      http.MethodGet,
		Path:        "/lists",
		Summary:     "The lists the caller can see",
		Description: "The lists the caller can see: the ones they are a recipient of, and the ones that have a recipient they can see. The admin's access to every user doesn't reach lists. `can_edit=true` also keeps the lists of the caller's children.",
		Tags:        []string{"Lists"},
		Errors:      []int{http.StatusUnauthorized},
	}
	listRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listListsInput) (*collectionOutput[listRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(listRules)
		if err != nil {
			return nil, err
		}
		filter := list.Filter{CanEdit: in.CanEdit.Pointer(), IsPrimary: in.IsPrimary.Pointer()}
		for _, raw := range in.RecipientIDs {
			// An ID that can't be a user's names nobody, who is no list's recipient.
			id, _ := parseID(raw)
			filter.RecipientIDs = append(filter.RecipientIDs, id)
		}
		lists, total, next, err := s.opts.Lists.Visible(ctx, caller.User, filter, req)
		if err != nil {
			return nil, err
		}
		rows, err := newListRows(lists, caller.User)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[listRow]{}
		out.Body.Data = rows
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID: "get-list",
		Method:      http.MethodGet,
		Path:        "/lists/{id}",
		Summary:     "A list",
		Description: "A list the caller can't see is a 404.",
		Tags:        []string{"Lists"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *listInput) (*resourceOutput[ListBody], error) {
		caller, _ := CallerFrom(ctx)
		l, err := s.visibleList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newListBody(l, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-list",
		Method:      http.MethodPatch,
		Path:        "/lists/{id}",
		Summary:     "Rename a list or change its description",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. " +
			"A user who can edit the list changes it. A field the caller sees but can't change is a 403 `field_not_writable`. A caller who can edit the list only as the parent of a deactivated recipient gets a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:   []string{"Lists"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchListInput) (*resourceOutput[ListBody], error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !current.CanEdit {
			return nil, s.cannotEdit(ctx, current.ID, "You can't edit this list.")
		}
		visible, err := keysOf(newListBody(current, caller.User))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(listChangeKeys, visible); err != nil {
			return nil, err
		}
		updated, err := s.opts.Lists.Update(ctx, caller.User, current.ID, in.Body.Changes.changes(), listVersionGuard(caller.User, in.IfMatch))
		if err != nil {
			return nil, listProblem(err)
		}
		return resourceOf(newListBody(updated, caller.User))
	})

	acceptMergePatch(api, "/lists/{id}")

	huma.Register(api, huma.Operation{
		OperationID:   "create-list",
		Method:        http.MethodPost,
		Path:          "/lists",
		Summary:       "Create an extra list",
		Description:   "Makes an extra list for the caller, or for one of their children. The recipient is the list's first recipient. A parent who makes a list for a child is not a recipient. A `recipient_id` the caller can't see is a 422 `reference_not_found`, one they see and aren't the parent of is a 403 `reference_not_allowed`, and a deactivated child is a 403 `user_deactivated`.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createListInput) (*createdOutput[ListBody], error) {
		caller, _ := CallerFrom(ctx)
		n := list.New{Name: in.Body.Name, Description: in.Body.Description}
		if raw := in.Body.RecipientID; raw != nil {
			// An ID that can't be a user's names nobody the caller can see.
			id, _ := parseID(*raw)
			n.RecipientID = &id
		}
		l, err := s.opts.Lists.Create(ctx, caller.User, n)
		if err != nil {
			return nil, listProblem(err)
		}
		return createdOf("/lists/"+formatID(l.ID), newListBody(l, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-list",
		Method:        http.MethodDelete,
		Path:          "/lists/{id}",
		Summary:       "Delete an extra list and its items",
		Description:   "The recipient of an extra list that has no other recipient deletes it, or their parent does. It deletes the list's items, as a delete by a user who can edit the list does. A primary list is a 403 `primary_list`, and a list with other recipients is a 403 `list_has_other_recipients`. It never refuses because of gifts. A parent of a deactivated recipient gets a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteListInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if err := s.opts.Lists.Delete(ctx, caller.User, current.ID, listVersionGuard(caller.User, in.IfMatch), s.opts.Items.RemoveAllOn); err != nil {
			return nil, listProblem(err)
		}
		return nil, nil
	})
}
