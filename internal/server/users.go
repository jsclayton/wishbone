package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

// UserBody is the full user: what a user's own address returns
// (docs/routes.md, User fields).
//
// `timezone` and `profile_confirmed_at` are for the user, their parents and the
// admin. A caller who sees the user only through a shared group gets a body
// without those two keys (see [Private] and [seesPrivateFields]).
type UserBody struct {
	ID                 string               `json:"id" example:"42" doc:"An opaque string. Don't parse it."`
	Name               string               `json:"name"`
	LastName           *string              `json:"last_name" nullable:"true"`
	Avatar             OptionalImage        `json:"avatar" doc:"The user's avatar, or null when there is none. Set it with avatar_image_id."`
	Birthday           user.PartialDate     `json:"birthday" doc:"Every user has one. The year can be null."`
	GoodToKnow         *string              `json:"good_to_know" nullable:"true" doc:"A note for the people who give to this user. Markdown is not rendered."`
	Timezone           Private[string]      `json:"timezone,omitzero" required:"false" doc:"An IANA zone name such as Europe/Paris, or null when none is set. Only the user, their parents, and the admin see it: for anyone else the key is absent."`
	IsAdmin            bool                 `json:"is_admin" doc:"Read-only. Only the CLI (set-admin) changes it."`
	Parents            []UserSummary        `json:"parents" nullable:"false" doc:"The user's parents, by name. Anyone who sees the user sees them, as summaries, even a parent whom they can't see. Only the admin changes them, with parent_ids."`
	Children           []UserSummary        `json:"children" nullable:"false" doc:"The user's children, by name. Read-only: a child's parent_ids sets it."`
	PrimaryList        Private[ListSummary] `json:"primary_list,omitzero" required:"false" doc:"The user's primary list. Every user has one. A caller who can't see the list gets no key, and the admin's access to every user doesn't count."`
	ProfileConfirmedAt Private[time.Time]   `json:"profile_confirmed_at,omitzero" required:"false" doc:"When the user confirmed their profile, or null. Only the user does, with confirm-profile. Only the user, their parents, and the admin see it: for anyone else the key is absent."`
	DeactivatedAt      Private[time.Time]   `json:"deactivated_at,omitzero" required:"false" doc:"When the admin deactivated the user, or null for an active user. Only the user, their parents, and the admin see a deactivated user, and they see this. Anyone else who sees the user sees an active one, so for them the key is absent."`
	CreatedAt          time.Time            `json:"created_at"`

	// The `can_*` fields are the caller's: one user has different ones for
	// different callers.
	CanEdit           bool `json:"can_edit" doc:"True when the caller can edit this user's profile."`
	CanEditParents    bool `json:"can_edit_parents" doc:"True when the caller can change this user's parents: the admin, for a user who isn't the admin."`
	CanMakeLoginLink  bool `json:"can_make_login_link" doc:"True when the caller can make this user a login link."`
	CanManageSessions bool `json:"can_manage_sessions" doc:"True when the caller can see and revoke this user's sessions."`
	CanConfirmProfile bool `json:"can_confirm_profile" doc:"True when the caller is this user and has not confirmed the profile yet."`
	CanDeactivate     bool `json:"can_deactivate" doc:"True when the caller is the admin and the user is active and isn't the admin."`
	CanReactivate     bool `json:"can_reactivate" doc:"True when the caller is the admin and the user is deactivated."`
	CanAddOccasion    bool `json:"can_add_occasion" doc:"True when the caller is this user or their parent and the user is active. The admin's access doesn't count."`
}

// userRow is a user in the users collection: the full user with its version.
type userRow struct {
	UserBody
	Version string `json:"version" doc:"The same value as the ETag of GET /users/{id} for this caller. Send it in If-Match."`
}

// newUserBody is the full user as the caller sees them. seesList says whether
// the caller sees the user's primary list: [Server.userBody] works it out with
// the rule of internal/list.
func newUserBody(u, caller user.User, seesList bool) UserBody {
	b := UserBody{
		ID:         formatID(u.ID),
		Name:       u.Name,
		LastName:   u.LastName,
		Birthday:   u.Birthday,
		GoodToKnow: u.GoodToKnow,
		IsAdmin:    u.IsAdmin,
		CreatedAt:  u.CreatedAt.UTC().Truncate(time.Second),

		CanEdit:           canEditUser(caller, u),
		CanEditParents:    canEditParents(caller, u),
		CanMakeLoginLink:  canActFor(caller, u),
		CanManageSessions: canActFor(caller, u),
		CanConfirmProfile: caller.ID == u.ID && u.ProfileConfirmedAt == nil,
		CanDeactivate:     caller.IsAdmin && !u.IsAdmin && u.Active(),
		CanReactivate:     caller.IsAdmin && !u.Active(),
		CanAddOccasion:    occasion.CanAddFor(caller, u),
		Parents:           userSummaries(u.Parents),
		Children:          userSummaries(u.Children),
	}
	if seesPrivateFields(caller, u) {
		b.Timezone = Shown(u.Timezone)
		var confirmed *time.Time
		if u.ProfileConfirmedAt != nil {
			t := u.ProfileConfirmedAt.UTC().Truncate(time.Second)
			confirmed = &t
		}
		b.ProfileConfirmedAt = Shown(confirmed)
		var deactivated *time.Time
		if u.DeactivatedAt != nil {
			t := u.DeactivatedAt.UTC().Truncate(time.Second)
			deactivated = &t
		}
		b.DeactivatedAt = Shown(deactivated)
	}
	b.Avatar = optionalImage(u.Avatar)
	if seesList {
		b.PrimaryList = Shown(&ListSummary{ID: formatID(u.PrimaryList.ID), Name: u.PrimaryList.Name})
	}
	return b
}

func newUserRow(u, caller user.User, seesList bool) (userRow, error) {
	body := newUserBody(u, caller, seesList)
	v, err := versionOf(body)
	return userRow{UserBody: body, Version: v}, err
}

// seesPrimaryLists says which of the users' primary lists the caller sees, by
// user ID. A user sees their own without a query. Other callers get the rule of
// [list.VisibleTo], so `primary_list` follows the list's visibility.
func (s *Server) seesPrimaryLists(ctx context.Context, caller user.User, users []user.User) (map[int64]bool, error) {
	seen := make(map[int64]bool, len(users))
	var ask []int64
	for _, u := range users {
		if u.ID == caller.ID {
			seen[u.ID] = true
		} else {
			ask = append(ask, u.ID)
		}
	}
	found, err := s.opts.Lists.PrimaryListsVisibleTo(ctx, caller, ask)
	for id := range found {
		seen[id] = true
	}
	return seen, err
}

// userBody is the full user as the caller sees them.
func (s *Server) userBody(ctx context.Context, u, caller user.User) (UserBody, error) {
	seen, err := s.seesPrimaryLists(ctx, caller, []user.User{u})
	if err != nil {
		return UserBody{}, err
	}
	return newUserBody(u, caller, seen[u.ID]), nil
}

// userRows are the users as the caller sees them, for a collection.
func (s *Server) userRows(ctx context.Context, users []user.User, caller user.User) ([]userRow, error) {
	seen, err := s.seesPrimaryLists(ctx, caller, users)
	if err != nil {
		return nil, err
	}
	rows := make([]userRow, len(users))
	for i, u := range users {
		if rows[i], err = newUserRow(u, caller, seen[u.ID]); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// seesPrivateFields says whether the caller sees the fields of a user that are
// only for the user, their parents, and the admin (`timezone`,
// `profile_confirmed_at`, `deactivated_at`). Anyone else who can see the user
// gets a body without them. It is the same group of callers that act for the
// user: the user, their parents, and the admin. A deactivated user's parents
// still see them, so this follows [actsFor] and not [canActFor].
func seesPrivateFields(caller, target user.User) bool {
	return actsFor(caller, target)
}

// canEditUser says whether the caller may edit the user's profile: the user,
// their parents, and the admin, while the user is active.
func canEditUser(caller user.User, target user.User) bool {
	return canActFor(caller, target)
}

// checkEdit says whether the caller may make an edit to the user that sets the
// given keys of the body. The user, their parents, and the admin may edit an
// active user. Of a deactivated user the admin may change only the parents, and
// nobody else may change anything: a 403 user_deactivated. Anyone else is a 403
// forbidden.
func checkEdit(caller, target user.User, keys []string) error {
	switch {
	case !actsFor(caller, target):
		return forbidden("You can't edit this user.")
	case target.Active():
		return nil
	case caller.IsAdmin && onlyParents(keys):
		return nil
	}
	return userDeactivated()
}

// onlyParents says whether the keys of a body are no more than parent_ids.
func onlyParents(keys []string) bool {
	for _, k := range keys {
		if k != "parent_ids" {
			return false
		}
	}
	return true
}

// canEditParents says whether the caller may change the user's parents: only
// the admin, and not for the admin, who can't have parents.
func canEditParents(caller, target user.User) bool {
	return caller.IsAdmin && !target.IsAdmin
}

// UserSummary is a user as another resource holds them (docs/routes.md,
// Summary shapes). A summary doesn't make the user visible.
type UserSummary struct {
	ID     string        `json:"id" example:"42" doc:"An opaque string. Don't parse it."`
	Name   string        `json:"name"`
	Avatar OptionalImage `json:"avatar" doc:"The user's avatar, or null when there is none."`
}

func userSummaries(list []user.Summary) []UserSummary {
	out := make([]UserSummary, len(list))
	for i, s := range list {
		out[i] = UserSummary{ID: formatID(s.ID), Name: s.Name, Avatar: optionalImage(s.Avatar)}
	}
	return out
}

// formatID and parseID are the only places that turn an ID into the string the
// API shows, and back. An ID that isn't a positive number is no resource.
func formatID(id int64) string { return strconv.FormatInt(id, 10) }

func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0 && formatID(id) == s
}

// location is the value of a `Location` header: the address of a resource, which
// includes the API prefix. Every create uses it, as in
// `location("/users/" + formatID(id))`.
func location(path string) string { return apiPrefix + path }

// notFound is the answer for a resource that doesn't exist and for one the
// caller can't see. The two look the same.
func notFound() error {
	return Error(http.StatusNotFound, "not_found", "There is nothing at this address.")
}

// forbidden is the answer for a resource the caller can see but can't act on.
func forbidden(detail string) error {
	return Error(http.StatusForbidden, "forbidden", detail)
}

// userProblem turns an error of the user package into a problem with a code.
func userProblem(err error) error {
	var parentMissing *user.ParentNotFoundError
	switch {
	case errors.As(err, &parentMissing):
		return referenceNotFound(fmt.Sprintf("body.parent_ids.%d", parentMissing.Index), "That user doesn't exist, or you can't see them.")
	case errors.Is(err, user.ErrParentLoop):
		return Error(http.StatusUnprocessableEntity, "parent_loop", err.Error(),
			&huma.ErrorDetail{Location: "body.parent_ids", Message: "would make a loop"})
	case errors.Is(err, user.ErrAdminHasNoParents):
		return Error(http.StatusUnprocessableEntity, "admin_has_no_parents", err.Error(),
			&huma.ErrorDetail{Location: "body.parent_ids", Message: "the admin has no parents"})
	case errors.Is(err, user.ErrNotFound):
		return notFound()
	case errors.Is(err, user.ErrAdminCannotBeDeactivated):
		return Error(http.StatusForbidden, "admin_cannot_be_deactivated", "The admin can't be deactivated. Move the admin role to another user with `wishbone set-admin` first.")
	case errors.Is(err, user.ErrBirthdayRequired):
		return Error(http.StatusUnprocessableEntity, "birthday_required", err.Error(),
			&huma.ErrorDetail{Location: "body.birthday", Message: "can't be null"})
	case errors.Is(err, user.ErrInvalid):
		return invalidInput(err)
	}
	return err
}

type userInput struct {
	ID string `path:"id" example:"42" doc:"A user's ID, or me for the caller."`
}

type listUsersInput struct {
	PageParams
	GroupID     string `query:"group_id" doc:"Only the users who are in this group, directly or through nesting, and whom the caller can see. A group the caller can't see, or that doesn't exist, has no users here: the answer is an empty page, so the filter doesn't show whether a group exists. Empty is no filter. Home's People needs it."`
	InvitableTo string `query:"invitable_to" doc:"A list's ID: only the users that POST /lists/{id}/invitations would accept from the caller. They are users the caller can see, with the admin's access not counted, who are active, aren't recipients of the list, and have no pending invitation to it. It is empty when the caller can't invite to the list: they aren't a recipient, it is a primary list, or they can't see it. Not a field of the user. Empty is no filter."`
	OccasionID  string `query:"occasion_id" doc:"An occasion's ID: only the users that the occasion applies to, and whom the caller can see. A birthday or a custom occasion applies to its user. A shared occasion applies to every user when it is marked everyone, and otherwise to the users in the groups it is attached to, directly or through nesting. An occasion the caller can't see, or that doesn't exist, has no users here: the answer is an empty page, so the filter doesn't show whether it exists. Not a field of the user. Empty is no filter."`
}

// userRules are the sort keys and the filters of GET /users.
var userRules = collectionRules{Name: "users", Sorts: user.SortKeys, Filters: []string{"group_id", "invitable_to", "occasion_id"}}

type createUserInput struct {
	Body struct {
		Name     string           `json:"name" minLength:"1" maxLength:"100" doc:"The first name."`
		LastName *string          `json:"last_name,omitempty" required:"false" nullable:"true" maxLength:"100"`
		Birthday user.PartialDate `json:"birthday" doc:"The month and the day are required. The year is optional."`
		Timezone *string          `json:"timezone,omitempty" required:"false" nullable:"true" doc:"An IANA zone name such as Europe/Paris."`

		ParentIDs []string `json:"parent_ids,omitempty" required:"false" nullable:"false" doc:"The IDs of the new user's parents. A user who doesn't exist is a 422 reference_not_found."`
	}
}

// userChanges are the fields of a user that some caller may edit with PATCH.
// The keys of the other fields in a user's body are refused with 403
// field_not_writable.
type userChanges struct {
	Name       Field[string]           `json:"name,omitempty" minLength:"1" maxLength:"100" doc:"The first name. It can't be null."`
	LastName   Field[string]           `json:"last_name,omitempty" nullable:"true" maxLength:"100" doc:"The last name. null or an empty string clears it."`
	Birthday   Field[user.PartialDate] `json:"birthday,omitempty" nullable:"true" doc:"A birthday can't be cleared: null is a 422 birthday_required."`
	GoodToKnow Field[string]           `json:"good_to_know,omitempty" nullable:"true" maxLength:"2000" doc:"null or an empty string clears it."`
	Timezone   Field[string]           `json:"timezone,omitempty" nullable:"true" doc:"An IANA zone name such as Europe/Paris. null clears it."`

	AvatarImageID Field[string] `json:"avatar_image_id,omitempty" nullable:"true" doc:"The ID of an image to use as the avatar (see POST /images). null removes the avatar. An image that is attached to something else is a 409 image_attached, and one the caller can't see is a 422 reference_not_found."`

	ParentIDs Field[[]string] `json:"parent_ids,omitempty" nullable:"false" doc:"Only the admin sets it; anyone else who sees the user gets a 403 field_not_writable. It replaces the user's parents: an empty list removes them all. A loop or a user as their own parent is a 422 parent_loop, a parent for the admin is a 422 admin_has_no_parents, and a user who doesn't exist is a 422 reference_not_found."`
}

// userChangeKeys are the keys of userChanges that a caller who can edit a user
// may set. The admin may also set the parents, see [writableUserKeys].
var userChangeKeys = []string{"name", "last_name", "birthday", "good_to_know", "timezone", "avatar_image_id"}

// writableUserKeys are the keys of a user that a caller who can edit the user
// may set with PATCH. The admin may send parent_ids for any user. For the admin
// as the user, it is a 422 admin_has_no_parents rather than a 403, because the
// field is theirs to write and the value is what the rules refuse.
func writableUserKeys(caller user.User) []string {
	if caller.IsAdmin {
		return append(append([]string{}, userChangeKeys...), "parent_ids")
	}
	return userChangeKeys
}

// changes turns the body into the domain package's changes. An image ID that
// can't be one names no image, which is the same 422 as an image that doesn't
// exist.
func (c userChanges) changes() (user.Changes, error) {
	parents, err := parentIDs(c.ParentIDs.Set, c.ParentIDs.Value)
	if err != nil {
		return user.Changes{}, err
	}
	avatar := patch.Optional[int64]{Set: c.AvatarImageID.Set}
	if id := c.AvatarImageID.Value; id != nil {
		parsed, ok := parseID(*id)
		if !ok {
			return user.Changes{}, referenceNotFound("body.avatar_image_id", "That image doesn't exist, or you can't see it.")
		}
		avatar.Value = &parsed
	}
	return user.Changes{
		Name:          c.Name.Optional,
		LastName:      c.LastName.Optional,
		Birthday:      c.Birthday.Optional,
		GoodToKnow:    c.GoodToKnow.Optional,
		Timezone:      c.Timezone.Optional,
		AvatarImageID: avatar,
		ParentIDs:     parents,
	}, nil
}

// parentIDs turns the IDs of a body into the domain package's. An ID that can't
// be one names no user, which is the same 422 as a user who doesn't exist.
func parentIDs(set bool, ids *[]string) (patch.Optional[[]int64], error) {
	out := patch.Optional[[]int64]{Set: set}
	if ids == nil {
		return out, nil
	}
	parsed := make([]int64, len(*ids))
	for i, raw := range *ids {
		id, ok := parseID(raw)
		if !ok {
			return out, referenceNotFound(fmt.Sprintf("body.parent_ids.%d", i), "That user doesn't exist, or you can't see them.")
		}
		parsed[i] = id
	}
	out.Value = &parsed
	return out, nil
}

type patchUserInput struct {
	ID      string `path:"id" example:"42" doc:"A user's ID, or me for the caller."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the user as you loaded it. If the user changed since, the answer is 412 resource_changed and nothing changes."`
	Body    MergePatch[userChanges]
}

// visibleUser loads the user at a path, for a caller who must be able to see
// them (see user.CanSee). A user the caller can't see looks the same as one who
// doesn't exist.
func (s *Server) visibleUser(ctx context.Context, rawID string) (user.User, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return user.User{}, notFound()
	}
	u, err := s.opts.Users.GetVisible(ctx, caller.User, id)
	if err != nil {
		return user.User{}, userProblem(err)
	}
	return u, nil
}

func (s *Server) registerUsers(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-users",
		Method:      http.MethodGet,
		Path:        "/users",
		Summary:     "The users the caller can see",
		Description: "The caller, the users who share a group with them (directly or through nesting), their parents and children, and for the admin every user. A deactivated user is seen only by the admin, their parents, and themselves.",
		Tags:        []string{"Users"},
		Errors:      []int{http.StatusUnauthorized},
	}
	userRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listUsersInput) (*collectionOutput[userRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(userRules)
		if err != nil {
			return nil, err
		}
		var users []user.User
		var total int
		var next *string
		var filter user.Filter
		if in.GroupID != "" {
			// An ID that can't be a group's names no group, which has no users.
			groupID, _ := parseID(in.GroupID)
			filter.GroupID = &groupID
		}
		if in.InvitableTo != "" {
			// An ID that can't be a list's names no list the caller can invite to.
			listID, _ := parseID(in.InvitableTo)
			if filter.Where, filter.WhereArgs, err = s.opts.Lists.InvitableWhere(ctx, caller.User, listID); err != nil {
				return nil, err
			}
		}
		if in.OccasionID != "" {
			// An ID that can't be an occasion's names no occasion, which applies to nobody.
			occasionID, _ := parseID(in.OccasionID)
			where, args, err := s.opts.Occasions.UsersWhere(ctx, caller.User, occasionID)
			if err != nil {
				return nil, err
			}
			if filter.Where != "" {
				where, args = "("+filter.Where+") AND ("+where+")", append(filter.WhereArgs, args...)
			}
			filter.Where, filter.WhereArgs = where, args
		}
		users, total, next, err = s.opts.Users.Matching(ctx, caller.User, filter, req)
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
		OperationID:   "create-user",
		Method:        http.MethodPost,
		Path:          "/users",
		Summary:       "Create a user",
		Description:   "Only the admin. A user is made with a name and a birthday; the month and day are required and the year is optional. `parent_ids` links the new user's parents.",
		Tags:          []string{"Users"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createUserInput) (*createdOutput[UserBody], error) {
		caller, _ := CallerFrom(ctx)
		if err := requireAdmin(ctx, "creates users"); err != nil {
			return nil, err
		}
		parents, err := parentIDs(true, &in.Body.ParentIDs)
		if err != nil {
			return nil, err
		}
		n := user.NewUser{Name: in.Body.Name, Birthday: in.Body.Birthday}
		if parents.Value != nil {
			n.ParentIDs = *parents.Value
		}
		if in.Body.LastName != nil {
			n.LastName = *in.Body.LastName
		}
		if in.Body.Timezone != nil {
			n.Timezone = *in.Body.Timezone
		}
		u, err := s.opts.Users.Create(ctx, n)
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
		OperationID: "get-user",
		Method:      http.MethodGet,
		Path:        "/users/{id}",
		Summary:     "A user",
		Description: "`me` stands for the caller. A user the caller can't see is a 404: the caller sees themselves, the users who share a group with them, their parents and their children, and for the admin every user. A deactivated user is seen only by the admin, their parents, and themselves. `timezone`, `profile_confirmed_at`, and `deactivated_at` are absent unless the caller is the user, a parent of the user, or the admin.",
		Tags:        []string{"Users"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *userInput) (*resourceOutput[UserBody], error) {
		caller, _ := CallerFrom(ctx)
		u, err := s.visibleUser(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		body, err := s.userBody(ctx, u, caller.User)
		if err != nil {
			return nil, err
		}
		return resourceOf(body)
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-user",
		Method:      http.MethodPatch,
		Path:        "/users/{id}",
		Summary:     "Edit a user's profile",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. " +
			"A field that is present is set, one that is absent doesn't change, and `null` clears the fields that can be cleared. " +
			"A field the caller sees but can't change is a 403 `field_not_writable`. `If-Match` is optional. " +
			"The user, their parents, and the admin edit a user. Only the admin sets `parent_ids`. While a user is deactivated, nobody edits them, and the admin sets only `parent_ids`: any other edit is a 403 `user_deactivated`.",
		Tags:   []string{"Users"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchUserInput) (*resourceOutput[UserBody], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.visibleUser(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !actsFor(caller.User, target) {
			return nil, forbidden("You can't edit this user.")
		}
		targetBody, err := s.userBody(ctx, target, caller.User)
		if err != nil {
			return nil, err
		}
		visible, err := keysOf(targetBody)
		if err != nil {
			return nil, err
		}
		// The parents are written as parent_ids and read as parents.
		visible = append(visible, "parent_ids")
		if err := in.Body.Refuse(writableUserKeys(caller.User), visible); err != nil {
			return nil, err
		}
		if err := checkEdit(caller.User, target, in.Body.Keys); err != nil {
			return nil, err
		}
		changes, err := in.Body.Changes.changes()
		if err != nil {
			return nil, err
		}
		updated, err := s.opts.Users.Update(ctx, target.ID, changes, func(ctx context.Context, q sqlx.ExtContext, current user.User) error {
			// The user may have been unlinked or deactivated since the route loaded
			// them. A write that races that must not succeed.
			if err := checkEdit(caller.User, current, in.Body.Keys); err != nil {
				if !actsFor(caller.User, current) {
					if seen, _ := user.CanSee(ctx, q, caller.User, current.ID); !seen {
						return notFound()
					}
				}
				return err
			}
			// Read inside the write's transaction, as the body will be after it.
			seen, err := list.PrimaryListsVisibleTo(ctx, q, caller.User, []int64{current.ID})
			if err != nil {
				return err
			}
			v, err := versionOf(newUserBody(current, caller.User, seen[current.ID]))
			if err != nil {
				return err
			}
			if err := checkIfMatch(in.IfMatch, v); err != nil {
				return err
			}
			if id := changes.AvatarImageID.Value; id != nil {
				return attachableImage(ctx, q, caller.User, *id, "body.avatar_image_id", func(held image.Attachment) bool {
					return held.Kind == image.KindAvatar && held.ID == current.ID
				})
			}
			return nil
		})
		if err != nil {
			return nil, userProblem(err)
		}
		body, err := s.userBody(ctx, updated, caller.User)
		if err != nil {
			return nil, err
		}
		return resourceOf(body)
	})

	huma.Register(api, huma.Operation{
		OperationID: "confirm-profile",
		Method:      http.MethodPost,
		Path:        "/users/{id}/confirm-profile",
		Summary:     "Confirm the profile",
		Description: "Only the user confirms their own profile; a parent's or the admin's edits don't. Sets `profile_confirmed_at`. A profile that is already confirmed keeps its first time and answers 200.",
		Tags:        []string{"Users"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *userInput) (*resourceOutput[UserBody], error) {
		caller, _ := CallerFrom(ctx)
		target, err := s.visibleUser(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if target.ID != caller.User.ID {
			return nil, forbidden("Only the user confirms their own profile.")
		}
		confirmed, err := s.opts.Users.ConfirmProfile(ctx, target.ID)
		if err != nil {
			return nil, userProblem(err)
		}
		body, err := s.userBody(ctx, confirmed, caller.User)
		if err != nil {
			return nil, err
		}
		return resourceOf(body)
	})

	huma.Register(api, huma.Operation{
		OperationID: "deactivate-user",
		Method:      http.MethodPost,
		Path:        "/users/{id}/deactivate",
		Summary:     "Deactivate a user",
		Description: "Only the admin. A pause: the user's sessions are revoked, their login links and passkeys stop working, and only the admin, their parents, and the user see them. Their open gifts are released: each one is deleted, and its quantity goes back on the item. The gifts they gave stay in Received. For gifts, they are now a recipient that no giver can see, so the open gifts for their occasions move and are flagged, and a gift whose giver sees no other recipient is flagged `no_longer_shared`. Everything else stays, and reactivation brings them back. A user who is already deactivated doesn't change, and the answer is 200. The admin can't be deactivated: 403 `admin_cannot_be_deactivated`.",
		Tags:        []string{"Users"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *userInput) (*resourceOutput[UserBody], error) {
		return s.setActive(ctx, in.ID, false)
	})

	huma.Register(api, huma.Operation{
		OperationID: "reactivate-user",
		Method:      http.MethodPost,
		Path:        "/users/{id}/reactivate",
		Summary:     "Reactivate a user",
		Description: "Only the admin. Brings the user back as they were: login links that haven't expired work again. Their revoked sessions don't come back. A gift that moved or was flagged while they were deactivated stays as it is. An active user doesn't change, and the answer is 200.",
		Tags:        []string{"Users"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *userInput) (*resourceOutput[UserBody], error) {
		return s.setActive(ctx, in.ID, true)
	})

	acceptMergePatch(api, "/users/{id}")
}

// setActive deactivates or reactivates the user at a path, for the admin.
func (s *Server) setActive(ctx context.Context, rawID string, active bool) (*resourceOutput[UserBody], error) {
	caller, _ := CallerFrom(ctx)
	target, err := s.visibleUser(ctx, rawID)
	if err != nil {
		return nil, err
	}
	if err := requireAdmin(ctx, "deactivates and reactivates users"); err != nil {
		return nil, err
	}
	var u user.User
	if active {
		u, err = s.opts.Users.Reactivate(ctx, target.ID)
	} else {
		u, err = s.opts.Users.Deactivate(ctx, target.ID)
	}
	if err != nil {
		return nil, userProblem(err)
	}
	body, err := s.userBody(ctx, u, caller.User)
	if err != nil {
		return nil, err
	}
	return resourceOf(body)
}
