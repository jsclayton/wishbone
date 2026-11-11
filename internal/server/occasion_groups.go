package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/group"
)

// occasionGroupsRules is the collection of the groups attached to a shared
// occasion. It sorts by name only.
var occasionGroupsRules = collectionRules{Name: "occasion-groups", Sorts: group.SortKeys}

type occasionGroupsInput struct {
	ID string `path:"id" example:"7" doc:"A shared occasion's ID."`
	PageParams
}

type attachGroupInput struct {
	ID   string `path:"id" example:"7" doc:"A shared occasion's ID."`
	Body struct {
		GroupID string `json:"group_id" example:"5" doc:"The ID of the group to attach."`
	}
}

type detachGroupInput struct {
	ID      string `path:"id" example:"7" doc:"A shared occasion's ID."`
	GroupID string `path:"group_id" example:"5" doc:"The ID of the group to detach."`
}

// managedSharedOccasion loads the shared occasion at a path for a route that only
// the admin may call. An occasion that the caller can't see, and one that is not
// shared, are a 404, and a caller who sees it and isn't the admin gets a 403.
func (s *Server) managedSharedOccasion(ctx context.Context, rawID string) (id int64, err error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return 0, notFound()
	}
	o, err := s.opts.Occasions.GroupsOf(ctx, caller.User, id)
	if err != nil {
		return 0, occasionProblem(err)
	}
	return o.ID, nil
}

func (s *Server) registerOccasionGroups(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-occasion-groups",
		Method:      http.MethodGet,
		Path:        "/occasions/{id}/groups",
		Summary:     "The groups attached to a shared occasion",
		Description: "Only the admin. The groups that the occasion is attached to directly. An occasion marked everyone has none. An occasion that isn't shared is a 404.",
		Tags:        []string{"Occasions"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}
	occasionGroupsRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *occasionGroupsInput) (*collectionOutput[groupRow], error) {
		caller, _ := CallerFrom(ctx)
		id, err := s.managedSharedOccasion(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		req, err := in.parse(occasionGroupsRules)
		if err != nil {
			return nil, err
		}
		groups, total, next, err := s.opts.Groups.AttachedTo(ctx, caller.User, id, req)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[groupRow]{}
		if out.Body.Data, err = newGroupRows(groups, caller.User); err != nil {
			return nil, err
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "attach-occasion-group",
		Method:        http.MethodPost,
		Path:          "/occasions/{id}/groups",
		Summary:       "Attach a group to a shared occasion",
		Description:   "Only the admin. Returns the group, with the group's own address in `Location`. The occasion then applies to every user in the group, directly or through nesting. A group that is already attached is a 409 `already_attached`, and an occasion marked everyone takes no group: a 409 `occasion_is_everyone`. A group that doesn't exist is a 422 `reference_not_found`.",
		Tags:          []string{"Occasions"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *attachGroupInput) (*createdOutput[GroupBody], error) {
		caller, _ := CallerFrom(ctx)
		id, err := s.managedSharedOccasion(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		groupID, ok := parseID(in.Body.GroupID)
		if !ok {
			return nil, referenceNotFound("body.group_id", "That group doesn't exist, or you can't see it.")
		}
		if err := s.opts.Occasions.AttachGroup(ctx, caller.User, id, groupID); err != nil {
			return nil, occasionProblem(err)
		}
		e, err := s.opts.Groups.Get(ctx, caller.User, groupID)
		if err != nil {
			return nil, groupProblem(err)
		}
		return createdOf("/groups/"+formatID(e.ID), newGroupBody(e, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "detach-occasion-group",
		Method:        http.MethodDelete,
		Path:          "/occasions/{id}/groups/{group_id}",
		Summary:       "Detach a group from a shared occasion",
		Description:   "Only the admin. A group that isn't attached is a 404. The group stays.",
		Tags:          []string{"Occasions"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *detachGroupInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		id, err := s.managedSharedOccasion(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		groupID, ok := parseID(in.GroupID)
		if !ok {
			return nil, notFound()
		}
		if err := s.opts.Occasions.DetachGroup(ctx, caller.User, id, groupID); err != nil {
			return nil, occasionProblem(err)
		}
		return nil, nil
	})
}
