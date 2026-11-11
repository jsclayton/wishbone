package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/user"
)

// GroupSummary is a group as another resource contains it
// (docs/routes.md, Summary shapes).
type GroupSummary struct {
	ID   string `json:"id" example:"5" doc:"An opaque string. Don't parse it."`
	Name string `json:"name"`
}

// OptionalGroup is a group summary, or null when there is none: the type of
// `through`. huma can't mark an object nullable on a field, so the type does it.
type OptionalGroup struct{ *GroupSummary }

// MarshalJSON writes null for no group.
func (o OptionalGroup) MarshalJSON() ([]byte, error) {
	if o.GroupSummary == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.GroupSummary)
}

// Schema is the schema of a group summary that can also be null.
func (OptionalGroup) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[GroupSummary](), false, "")
	s.Nullable = true
	return &s
}

// GroupBody is the full group (docs/routes.md, Groups).
type GroupBody struct {
	ID        string        `json:"id" example:"5" doc:"An opaque string. Don't parse it."`
	Name      string        `json:"name"`
	CreatedAt time.Time     `json:"created_at"`
	Through   OptionalGroup `json:"through" doc:"The group the caller is directly in, and that this group contains by nesting; null when the caller is directly in this group or is not in it. When several groups put the caller in this one, the first of them by name."`

	// Only the admin manages groups, and a group has no owners.
	CanEdit     bool `json:"can_edit" doc:"True when the caller can rename the group: the admin."`
	CanDelete   bool `json:"can_delete" doc:"True when the caller can delete the group: the admin."`
	CanAddUser  bool `json:"can_add_user" doc:"True when the caller can add users to the group and remove them: the admin."`
	CanAddGroup bool `json:"can_add_group" doc:"True when the caller can nest groups in the group and take them out: the admin."`
}

// groupRow is a group in a collection: the full group with its version.
type groupRow struct {
	GroupBody
	Version string `json:"version" doc:"The same value as the ETag of GET /groups/{id} for this caller. Send it in If-Match."`
}

func newGroupBody(e group.Entry, caller user.User) GroupBody {
	b := GroupBody{
		ID:          formatID(e.ID),
		Name:        e.Name,
		CreatedAt:   e.CreatedAt.UTC().Truncate(time.Second),
		CanEdit:     caller.IsAdmin,
		CanDelete:   caller.IsAdmin,
		CanAddUser:  caller.IsAdmin,
		CanAddGroup: caller.IsAdmin,
	}
	if e.Through != nil {
		b.Through = OptionalGroup{&GroupSummary{ID: formatID(e.Through.ID), Name: e.Through.Name}}
	}
	return b
}

func newGroupRows(entries []group.Entry, caller user.User) ([]groupRow, error) {
	rows := make([]groupRow, len(entries))
	for i, e := range entries {
		body := newGroupBody(e, caller)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = groupRow{GroupBody: body, Version: v}
	}
	return rows, nil
}

// groupProblem turns an error of the group package into a problem with a code.
func groupProblem(err error) error {
	var field *group.FieldError
	switch {
	case errors.Is(err, group.ErrNotFound):
		return notFound()
	case errors.As(err, &field):
		return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(),
			&huma.ErrorDetail{Location: "body." + field.Field, Message: err.Error()})
	case errors.Is(err, group.ErrAlreadyIn):
		return Error(http.StatusConflict, "already_in_group", "That is already directly in this group.")
	case errors.Is(err, group.ErrLoop):
		return Error(http.StatusUnprocessableEntity, "group_loop", "That would put a group inside itself.",
			&huma.ErrorDetail{Location: "body.group_id", Message: "the group already contains this one"})
	case errors.Is(err, group.ErrNotIn):
		return notFound()
	case errors.Is(err, group.ErrUserDeactivated):
		return userDeactivated()
	case errors.Is(err, group.ErrUserNotFound):
		return referenceNotFound("body.user_id", "That user doesn't exist, or you can't see them.")
	case errors.Is(err, group.ErrGroupNotFound):
		return referenceNotFound("body.group_id", "That group doesn't exist, or you can't see it.")
	}
	return err
}

type groupInput struct {
	ID string `path:"id" example:"5" doc:"A group's ID."`
}

type listGroupsInput struct {
	PageParams
	IncludesMe bool `query:"includes_me" doc:"true limits the admin's groups to the ones the admin is in, directly or through nesting. false, or leaving it out, gives the default result. For anyone else it changes nothing."`
}

var groupRules = collectionRules{Name: "groups", Sorts: group.SortKeys, Filters: []string{"includes_me"}}

// groupUsersRules and groupGroupsRules are the collections inside a group. They
// sort by name only.
var (
	groupUsersRules  = collectionRules{Name: "group-users", Sorts: group.SortKeys}
	groupGroupsRules = collectionRules{Name: "group-groups", Sorts: group.SortKeys}
)

type createGroupInput struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100" doc:"The group's name."`
	}
}

type groupChanges struct {
	Name Field[string] `json:"name,omitempty" minLength:"1" maxLength:"100" doc:"The group's name. It can't be null."`
}

var groupChangeKeys = []string{"name"}

type patchGroupInput struct {
	ID      string `path:"id" example:"5" doc:"A group's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the group as you loaded it. If the group changed since, the answer is 412 resource_changed and nothing changes."`
	Body    MergePatch[groupChanges]
}

type deleteGroupInput struct {
	ID      string `path:"id" example:"5" doc:"A group's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the group as you loaded it. If the group changed since, the answer is 412 resource_changed and nothing is deleted."`
}

type groupUsersInput struct {
	ID string `path:"id" example:"5" doc:"A group's ID."`
	PageParams
}

type addGroupUserInput struct {
	ID   string `path:"id" example:"5" doc:"A group's ID."`
	Body struct {
		UserID string `json:"user_id" example:"42" doc:"The ID of the user to add."`
	}
}

type removeGroupUserInput struct {
	ID     string `path:"id" example:"5" doc:"A group's ID."`
	UserID string `path:"user_id" example:"42" doc:"The ID of the user to take out."`
}

type groupGroupsInput struct {
	ID string `path:"id" example:"5" doc:"A group's ID."`
	PageParams
}

type addGroupGroupInput struct {
	ID   string `path:"id" example:"5" doc:"The ID of the outer group."`
	Body struct {
		GroupID string `json:"group_id" example:"6" doc:"The ID of the group to put inside it."`
	}
}

type removeGroupGroupInput struct {
	ID      string `path:"id" example:"5" doc:"The ID of the outer group."`
	GroupID string `path:"group_id" example:"6" doc:"The ID of the group to take out."`
}

// visibleGroup loads the group at a path as the caller sees it. A group the
// caller is not in looks the same as one that doesn't exist, unless the caller
// is the admin.
func (s *Server) visibleGroup(ctx context.Context, rawID string) (group.Entry, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return group.Entry{}, notFound()
	}
	e, err := s.opts.Groups.Get(ctx, caller.User, id)
	if err != nil {
		return group.Entry{}, groupProblem(err)
	}
	return e, nil
}

// managedGroup is [Server.visibleGroup] for a route that only the admin may
// call: a caller who sees the group but isn't the admin gets a 403.
func (s *Server) managedGroup(ctx context.Context, rawID, what string) (group.Entry, error) {
	e, err := s.visibleGroup(ctx, rawID)
	if err != nil {
		return group.Entry{}, err
	}
	if err := requireAdmin(ctx, what); err != nil {
		return group.Entry{}, err
	}
	return e, nil
}

func groupVersionGuard(caller user.User, ifMatch string) group.Guard {
	return func(_ context.Context, current group.Entry) error {
		v, err := versionOf(newGroupBody(current, caller))
		if err != nil {
			return err
		}
		return checkIfMatch(ifMatch, v)
	}
}

func (s *Server) registerGroups(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-groups",
		Method:      http.MethodGet,
		Path:        "/groups",
		Summary:     "The groups the caller is in",
		Description: "The groups that contain the caller, directly or through nesting. The admin gets every group.",
		Tags:        []string{"Groups"},
		Errors:      []int{http.StatusUnauthorized},
	}
	groupRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listGroupsInput) (*collectionOutput[groupRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(groupRules)
		if err != nil {
			return nil, err
		}
		groups, total, next, err := s.opts.Groups.List(ctx, caller.User, in.IncludesMe, req)
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
		OperationID:   "create-group",
		Method:        http.MethodPost,
		Path:          "/groups",
		Summary:       "Create a group",
		Description:   "Only the admin.",
		Tags:          []string{"Groups"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createGroupInput) (*createdOutput[GroupBody], error) {
		caller, _ := CallerFrom(ctx)
		if err := requireAdmin(ctx, "creates groups"); err != nil {
			return nil, err
		}
		e, err := s.opts.Groups.Create(ctx, in.Body.Name)
		if err != nil {
			return nil, groupProblem(err)
		}
		return createdOf("/groups/"+formatID(e.ID), newGroupBody(e, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-group",
		Method:      http.MethodGet,
		Path:        "/groups/{id}",
		Summary:     "A group",
		Description: "For a user in the group, directly or through nesting, and the admin. Any other caller gets a 404.",
		Tags:        []string{"Groups"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *groupInput) (*resourceOutput[GroupBody], error) {
		caller, _ := CallerFrom(ctx)
		e, err := s.visibleGroup(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newGroupBody(e, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-group",
		Method:      http.MethodPatch,
		Path:        "/groups/{id}",
		Summary:     "Rename a group",
		Description: "Only the admin. JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. " +
			"A field the caller sees but can't change is a 403 `field_not_writable`. `If-Match` is optional.",
		Tags:   []string{"Groups"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchGroupInput) (*resourceOutput[GroupBody], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.managedGroup(ctx, in.ID, "renames groups")
		if err != nil {
			return nil, err
		}
		visible, err := keysOf(newGroupBody(target, caller.User))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(groupChangeKeys, visible); err != nil {
			return nil, err
		}
		name := in.Body.Changes.Name
		if name.Set && name.Value == nil {
			return nil, groupProblem(&group.FieldError{Field: "name", Message: "the name is required"})
		}
		updated := target
		if name.Set {
			updated, err = s.opts.Groups.Rename(ctx, caller.User, target.ID, *name.Value, groupVersionGuard(caller.User, in.IfMatch))
		} else {
			// Nothing to change; the version still has to fit.
			err = groupVersionGuard(caller.User, in.IfMatch)(ctx, target)
		}
		if err != nil {
			return nil, groupProblem(err)
		}
		return resourceOf(newGroupBody(updated, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-group",
		Method:        http.MethodDelete,
		Path:          "/groups/{id}",
		Summary:       "Delete a group",
		Description:   "Only the admin. The group and its places in the nesting go. The users in it, and the groups inside it, stay. `If-Match` is optional.",
		Tags:          []string{"Groups"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteGroupInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.managedGroup(ctx, in.ID, "deletes groups")
		if err != nil {
			return nil, err
		}
		if err := s.opts.Groups.Delete(ctx, caller.User, target.ID, groupVersionGuard(caller.User, in.IfMatch)); err != nil {
			return nil, groupProblem(err)
		}
		return nil, nil
	})

	usersOp := huma.Operation{
		OperationID: "list-group-users",
		Method:      http.MethodGet,
		Path:        "/groups/{id}/users",
		Summary:     "The users directly in a group",
		Description: "For a user in the group, directly or through nesting, and the admin. Full users: the caller shares the group with each of them.",
		Tags:        []string{"Groups"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}
	groupUsersRules.apply(&usersOp)
	huma.Register(api, usersOp, listing(func(ctx context.Context, in *groupUsersInput) (*collectionOutput[userRow], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.visibleGroup(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		req, err := in.parse(groupUsersRules)
		if err != nil {
			return nil, err
		}
		users, total, next, err := s.opts.Users.InGroup(ctx, caller.User, target.ID, req)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[userRow]{}
		if out.Body.Data, err = s.userRows(ctx, users, caller.User); err != nil {
			return nil, err
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "add-group-user",
		Method:        http.MethodPost,
		Path:          "/groups/{id}/users",
		Summary:       "Add a user to a group",
		Description:   "Only the admin. Returns the user, with the user's own address in `Location`. A user who is already directly in the group is a 409 `already_in_group`. A deactivated user is a 403 `user_deactivated`.",
		Tags:          []string{"Groups"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *addGroupUserInput) (*createdOutput[UserBody], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.managedGroup(ctx, in.ID, "adds users to groups")
		if err != nil {
			return nil, err
		}
		userID, ok := parseID(in.Body.UserID)
		if !ok {
			return nil, referenceNotFound("body.user_id", "That user doesn't exist, or you can't see them.")
		}
		if err := s.opts.Groups.AddUser(ctx, target.ID, userID); err != nil {
			return nil, groupProblem(err)
		}
		u, err := s.opts.Users.GetVisible(ctx, caller.User, userID)
		if err != nil {
			return nil, userProblem(err)
		}
		body, err := s.userBody(ctx, u, caller.User)
		if err != nil {
			return nil, err
		}
		return createdOf("/users/"+formatID(u.ID), body)
	})

	huma.Register(api, huma.Operation{
		OperationID:   "remove-group-user",
		Method:        http.MethodDelete,
		Path:          "/groups/{id}/users/{user_id}",
		Summary:       "Take a user out of a group",
		Description:   "Only the admin. A user who is not directly in the group is a 404. A deactivated user is a 403 `user_deactivated`: deactivation changes no group membership.",
		Tags:          []string{"Groups"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *removeGroupUserInput) (*struct{}, error) {
		target, err := s.managedGroup(ctx, in.ID, "takes users out of groups")
		if err != nil {
			return nil, err
		}
		userID, ok := parseID(in.UserID)
		if !ok {
			return nil, notFound()
		}
		if err := s.opts.Groups.RemoveUser(ctx, target.ID, userID); err != nil {
			return nil, groupProblem(err)
		}
		return nil, nil
	})

	groupsOp := huma.Operation{
		OperationID: "list-group-groups",
		Method:      http.MethodGet,
		Path:        "/groups/{id}/groups",
		Summary:     "The groups directly inside a group",
		Description: "Only the admin: a user in the outer group is not in the groups inside it.",
		Tags:        []string{"Groups"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}
	groupGroupsRules.apply(&groupsOp)
	huma.Register(api, groupsOp, listing(func(ctx context.Context, in *groupGroupsInput) (*collectionOutput[groupRow], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.managedGroup(ctx, in.ID, "lists the groups inside a group")
		if err != nil {
			return nil, err
		}
		req, err := in.parse(groupGroupsRules)
		if err != nil {
			return nil, err
		}
		groups, total, next, err := s.opts.Groups.Inside(ctx, caller.User, target.ID, req)
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
		OperationID:   "add-group-group",
		Method:        http.MethodPost,
		Path:          "/groups/{id}/groups",
		Summary:       "Put a group inside a group",
		Description:   "Only the admin. Returns the inner group, with its own address in `Location`. A group that is already directly inside is a 409 `already_in_group`, and a change that would put a group inside itself is a 422 `group_loop`.",
		Tags:          []string{"Groups"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *addGroupGroupInput) (*createdOutput[GroupBody], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.managedGroup(ctx, in.ID, "puts groups inside groups")
		if err != nil {
			return nil, err
		}
		innerID, ok := parseID(in.Body.GroupID)
		if !ok {
			return nil, referenceNotFound("body.group_id", "That group doesn't exist, or you can't see it.")
		}
		if err := s.opts.Groups.AddGroup(ctx, target.ID, innerID); err != nil {
			return nil, groupProblem(err)
		}
		inner, err := s.opts.Groups.Get(ctx, caller.User, innerID)
		if err != nil {
			return nil, groupProblem(err)
		}
		return createdOf("/groups/"+formatID(inner.ID), newGroupBody(inner, caller.User))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "remove-group-group",
		Method:        http.MethodDelete,
		Path:          "/groups/{id}/groups/{group_id}",
		Summary:       "Take a group out of a group",
		Description:   "Only the admin. A group that is not directly inside is a 404.",
		Tags:          []string{"Groups"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *removeGroupGroupInput) (*struct{}, error) {
		target, err := s.managedGroup(ctx, in.ID, "takes groups out of groups")
		if err != nil {
			return nil, err
		}
		innerID, ok := parseID(in.GroupID)
		if !ok {
			return nil, notFound()
		}
		if err := s.opts.Groups.RemoveGroup(ctx, target.ID, innerID); err != nil {
			return nil, groupProblem(err)
		}
		return nil, nil
	})

	acceptMergePatch(api, "/groups/{id}")
}
