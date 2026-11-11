package server

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/user"
)

// InvitationBody is a pending invitation (docs/routes.md, Invitations).
type InvitationBody struct {
	ID        string              `json:"id" example:"12" doc:"An opaque string. Don't parse it."`
	List      ListSummary         `json:"list" doc:"The extra list the user is invited to. A summary doesn't make the list visible."`
	User      UserSummary         `json:"user" doc:"The invited user."`
	InvitedBy OptionalUserSummary `json:"invited_by" doc:"The recipient who invited them, or null once that user is deleted."`
	CreatedAt time.Time           `json:"created_at"`

	// The `can_*` fields are the caller's.
	CanAccept bool `json:"can_accept" doc:"True when the caller is the invited user or their parent, and the invited user is active."`
	CanDelete bool `json:"can_delete" doc:"True when the caller can decline or withdraw: a recipient of the list, or the invited user or their parent while the invited user is active."`
}

// invitationRow is an invitation in the invitations collection: the full
// invitation with its version.
type invitationRow struct {
	InvitationBody
	Version string `json:"version" doc:"The same value as the ETag of GET /invitations/{id} for this caller. Send it in If-Match."`
}

func newInvitationBody(i list.Invitation) InvitationBody {
	b := InvitationBody{
		ID:        formatID(i.ID),
		List:      ListSummary{ID: formatID(i.List.ID), Name: i.List.Name},
		User:      userSummaries([]user.Summary{i.User})[0],
		CreatedAt: i.CreatedAt.UTC().Truncate(time.Second),
		CanAccept: i.CanAccept,
		CanDelete: i.CanDelete,
	}
	if i.InvitedBy != nil {
		by := userSummaries([]user.Summary{*i.InvitedBy})[0]
		b.InvitedBy = OptionalUserSummary{&by}
	}
	return b
}

// RecipientBody is a recipient of a list: the recipient resource of
// docs/routes.md, List routes. Its address is
// `/lists/{id}/recipients/{user_id}`.
type RecipientBody struct {
	User                 UserSummary   `json:"user"`
	CreatedAt            time.Time     `json:"created_at" doc:"When the user became a recipient."`
	SeesListAfterLeaving Private[bool] `json:"sees_list_after_leaving,omitzero" required:"false" doc:"True when the user could still see the list after they leave it: they can see another of its recipients, with the admin's access not counted. Only that user and their parents get it: for anyone else the key is absent. The leave confirmation shows its warning from it."`

	CanDelete bool `json:"can_delete" doc:"True when the list is an extra list with another recipient, and the caller is this user or their parent. Delete the recipient to leave the list."`
}

// recipientRow is a recipient in the recipients collection: the full resource
// with its version.
type recipientRow struct {
	RecipientBody
	Version string `json:"version" doc:"The same value as the ETag of the recipient for this caller. Send it in If-Match."`
}

func newRecipientBody(r list.Recipient) RecipientBody {
	b := RecipientBody{
		User:      userSummaries([]user.Summary{r.User})[0],
		CreatedAt: r.CreatedAt.UTC().Truncate(time.Second),
		CanDelete: r.CanDelete,
	}
	if r.SeesListAfterLeaving != nil {
		b.SeesListAfterLeaving = Shown(r.SeesListAfterLeaving)
	}
	return b
}

type listInvitationsInput struct {
	PageParams
	ListIDs []string `query:"list_id,explode" doc:"Only the invitations to this list. Repeat it for any of several lists."`
	UserIDs []string `query:"user_id,explode" doc:"Only the invitations of this invited user. Repeat it for any of several users."`
}

var invitationRules = collectionRules{Name: "invitations", Sorts: list.InvitationSortKeys, Filters: []string{"list_id", "user_id"}}

type invitationInput struct {
	ID string `path:"id" example:"12" doc:"An invitation's ID."`
}

type deleteInvitationInput struct {
	ID      string `path:"id" example:"12" doc:"An invitation's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the invitation as you loaded it. If it changed since, the answer is 412 resource_changed and nothing is deleted."`
}

type createInvitationInput struct {
	ID   string `path:"id" example:"7" doc:"A list's ID."`
	Body struct {
		UserID string `json:"user_id" example:"42" doc:"The user to invite: one the caller can see, with the admin's access not counted."`
	}
}

type listRecipientsInput struct {
	ID string `path:"id" example:"7" doc:"A list's ID."`
	PageParams
}

type leaveListInput struct {
	ID      string `path:"id" example:"7" doc:"A list's ID."`
	UserID  string `path:"user_id" example:"42" doc:"The recipient who leaves."`
	IfMatch string `header:"If-Match" doc:"The version of the recipient as you loaded it (a row's version of GET /lists/{id}/recipients). If it changed since, the answer is 412 resource_changed and nothing changes."`
}

var recipientRules = collectionRules{Name: "recipients", Sorts: list.RecipientSortKeys}

func newInvitationRows(invitations []list.Invitation) ([]invitationRow, error) {
	rows := make([]invitationRow, len(invitations))
	for i, inv := range invitations {
		body := newInvitationBody(inv)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = invitationRow{InvitationBody: body, Version: v}
	}
	return rows, nil
}

func newRecipientRows(recipients []list.Recipient) ([]recipientRow, error) {
	rows := make([]recipientRow, len(recipients))
	for i, r := range recipients {
		body := newRecipientBody(r)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = recipientRow{RecipientBody: body, Version: v}
	}
	return rows, nil
}

// parseIDs reads the IDs of a filter. An ID that can't be a resource's names
// nothing, so it matches nothing: it becomes 0, which no row has.
func parseIDs(raw []string) []int64 {
	out := make([]int64, len(raw))
	for i, r := range raw {
		out[i], _ = parseID(r)
	}
	return out
}

func (s *Server) registerInvitations(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-invitations",
		Method:      http.MethodGet,
		Path:        "/invitations",
		Summary:     "The invitations the caller can see",
		Description: "The pending invitations to extra lists that the caller can see: the ones to the caller, to their children, and to the lists they are a recipient of. Only pending invitations exist.",
		Tags:        []string{"Lists"},
		Errors:      []int{http.StatusUnauthorized},
	}
	invitationRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listInvitationsInput) (*collectionOutput[invitationRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(invitationRules)
		if err != nil {
			return nil, err
		}
		invitations, total, next, err := s.opts.Lists.Invitations(ctx, caller.User, list.InvitationFilter{ListIDs: parseIDs(in.ListIDs), UserIDs: parseIDs(in.UserIDs)}, req)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[invitationRow]{}
		if out.Body.Data, err = newInvitationRows(invitations); err != nil {
			return nil, err
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "create-invitation",
		Method:        http.MethodPost,
		Path:          "/lists/{id}/invitations",
		Summary:       "Invite a user to an extra list",
		Description:   "A recipient of an extra list invites a user they can see, with the admin's access not counted. The invitation stays pending until the invited user, or their parent, accepts or declines it. A user the caller can't see is a 422 `reference_not_found`, and one they see only as the admin is a 403 `reference_not_allowed`. A primary list is a 403 `primary_list`, a deactivated user is a 403 `user_deactivated`, and a caller who sees the list and is not a recipient is a 403 `forbidden`. A user who already has an invitation is a 409 `already_invited`, and one who is a recipient is a 409 `already_recipient`.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createInvitationInput) (*createdOutput[InvitationBody], error) {
		caller, _ := CallerFrom(ctx)
		listID, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		// An ID that can't be a user's names nobody the caller can see.
		userID, _ := parseID(in.Body.UserID)
		inv, err := s.opts.Lists.Invite(ctx, caller.User, listID, userID)
		if err != nil {
			return nil, listProblem(err)
		}
		return createdOf("/invitations/"+formatID(inv.ID), newInvitationBody(inv))
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-invitation",
		Method:      http.MethodGet,
		Path:        "/invitations/{id}",
		Summary:     "An invitation",
		Description: "Seen by the invited user, their parents, and the recipients of the list. For everyone else it is a 404.",
		Tags:        []string{"Lists"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *invitationInput) (*resourceOutput[InvitationBody], error) {
		inv, err := s.visibleInvitation(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newInvitationBody(inv))
	})

	huma.Register(api, huma.Operation{
		OperationID: "accept-invitation",
		Method:      http.MethodPost,
		Path:        "/invitations/{id}/accept",
		Summary:     "Accept an invitation",
		Description: "The invited user, or their parent, accepts. The user becomes a recipient and the invitation is gone, so the answer is the **list**. A caller who sees the invitation and is not the user or their parent gets a 403 `forbidden`. While the invited user is deactivated, nobody accepts: 403 `user_deactivated`.",
		Tags:        []string{"Lists"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *invitationInput) (*resourceOutput[ListBody], error) {
		caller, _ := CallerFrom(ctx)
		id, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		l, err := s.opts.Lists.Accept(ctx, caller.User, id)
		if err != nil {
			return nil, listProblem(err)
		}
		return resourceOf(newListBody(l, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-invitation",
		Method:        http.MethodDelete,
		Path:          "/invitations/{id}",
		Summary:       "Decline or withdraw an invitation",
		Description:   "The invited user or their parent declines it, and a recipient of the list withdraws it. While the invited user is deactivated, only a recipient can: for anyone else it is a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteInvitationInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		id, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		err := s.opts.Lists.DeleteInvitation(ctx, caller.User, id, func(_ context.Context, _ sqlx.ExtContext, current list.Invitation) error {
			v, err := versionOf(newInvitationBody(current))
			if err != nil {
				return err
			}
			return checkIfMatch(in.IfMatch, v)
		})
		if err != nil {
			return nil, listProblem(err)
		}
		return nil, nil
	})

	recipientsOp := huma.Operation{
		OperationID: "list-recipients",
		Method:      http.MethodGet,
		Path:        "/lists/{id}/recipients",
		Summary:     "The recipients of a list",
		Description: "Anyone who can see the list sees its recipients, as recipient resources. `sees_list_after_leaving` is only for that user and their parents.",
		Tags:        []string{"Lists"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}
	recipientRules.apply(&recipientsOp)
	huma.Register(api, recipientsOp, listing(func(ctx context.Context, in *listRecipientsInput) (*collectionOutput[recipientRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(recipientRules)
		if err != nil {
			return nil, err
		}
		listID, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		recipients, total, next, err := s.opts.Lists.Recipients(ctx, caller.User, listID, req)
		if err != nil {
			return nil, listProblem(err)
		}
		out := &collectionOutput[recipientRow]{}
		if out.Body.Data, err = newRecipientRows(recipients); err != nil {
			return nil, err
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "leave-list",
		Method:        http.MethodDelete,
		Path:          "/lists/{id}/recipients/{user_id}",
		Summary:       "Leave a list",
		Description:   "The recipient leaves an extra list, or their parent makes them leave. The last recipient can't leave: 403 `last_recipient`. A primary list is a 403 `primary_list`. A caller who sees the list and is neither the user nor their parent gets a 403 `forbidden`, and a parent of a deactivated user gets a 403 `user_deactivated`. A user who is not a recipient is a 404. `If-Match` is optional.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *leaveListInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		listID, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		userID, ok := parseID(in.UserID)
		if !ok {
			return nil, notFound()
		}
		err := s.opts.Lists.Leave(ctx, caller.User, listID, userID, func(_ context.Context, _ sqlx.ExtContext, current list.Recipient) error {
			v, err := versionOf(newRecipientBody(current))
			if err != nil {
				return err
			}
			return checkIfMatch(in.IfMatch, v)
		})
		if err != nil {
			return nil, listProblem(err)
		}
		return nil, nil
	})
}

// visibleInvitation loads the invitation at a path, for a caller who must be
// able to see it. An invitation the caller can't see looks the same as one that
// doesn't exist.
func (s *Server) visibleInvitation(ctx context.Context, rawID string) (list.Invitation, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return list.Invitation{}, notFound()
	}
	inv, err := s.opts.Lists.GetInvitation(ctx, caller.User, id)
	if err != nil {
		return list.Invitation{}, listProblem(err)
	}
	return inv, nil
}
