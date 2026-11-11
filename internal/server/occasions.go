package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/user"
)

// RuleInput is an occasion's date rule in a request (docs/routes.md,
// Occasion fields). A field that the rule's kind needs is required,
// and a field that the kind doesn't use is ignored.
type RuleInput struct {
	Kind    string `json:"kind" enum:"annual,once,nth_weekday,last_weekday" doc:"annual: a date every year (month, day). once: one date (year, month, day). nth_weekday: the nth weekday of a month, every year (n, weekday, month). last_weekday: the last weekday of a month, every year (weekday, month)."`
	Year    *int   `json:"year,omitempty" required:"false" minimum:"1" maximum:"9999" doc:"once only."`
	Month   int    `json:"month" minimum:"1" maximum:"12" doc:"1 to 12."`
	Day     *int   `json:"day,omitempty" required:"false" minimum:"1" maximum:"31" doc:"annual and once. February 29 of an annual rule falls on February 28 in a year that isn't a leap year."`
	Weekday *int   `json:"weekday,omitempty" required:"false" minimum:"0" maximum:"6" doc:"nth_weekday and last_weekday. 0 is Sunday."`
	N       *int   `json:"n,omitempty" required:"false" minimum:"1" maximum:"4" doc:"nth_weekday only, 1 to 4."`
}

// RuleBody is an occasion's date rule in a response. Every field is always
// there, and the ones that the rule's kind doesn't use are null (docs/spec.md,
// API principles, Values).
type RuleBody struct {
	Kind    string `json:"kind" enum:"annual,once,nth_weekday,last_weekday" doc:"annual: a date every year (month, day). once: one date (year, month, day). nth_weekday: the nth weekday of a month, every year (n, weekday, month). last_weekday: the last weekday of a month, every year (weekday, month)."`
	Year    *int   `json:"year" nullable:"true" doc:"once only. null for the other kinds."`
	Month   int    `json:"month" doc:"1 to 12."`
	Day     *int   `json:"day" nullable:"true" doc:"annual and once. null for the other kinds."`
	Weekday *int   `json:"weekday" nullable:"true" doc:"nth_weekday and last_weekday. 0 is Sunday. null for the other kinds."`
	N       *int   `json:"n" nullable:"true" doc:"nth_weekday only, 1 to 4. null for the other kinds."`
}

var ruleKindNames = map[occasion.Kind]string{
	occasion.KindAnnual:      "annual",
	occasion.KindOnce:        "once",
	occasion.KindNthWeekday:  "nth_weekday",
	occasion.KindLastWeekday: "last_weekday",
}

func newRuleBody(r occasion.Rule) RuleBody {
	b := RuleBody{Kind: ruleKindNames[r.Kind], Month: int(r.Month)}
	weekday := int(r.Weekday)
	switch r.Kind {
	case occasion.KindAnnual:
		b.Day = &r.Day
	case occasion.KindOnce:
		b.Year, b.Day = &r.Year, &r.Day
	case occasion.KindNthWeekday:
		b.Weekday, b.N = &weekday, &r.N
	case occasion.KindLastWeekday:
		b.Weekday = &weekday
	}
	return b
}

// need is a field of a rule that its kind requires.
type need struct {
	name string
	set  bool
}

// rule turns the body into a date rule. A field that the kind needs and the body
// leaves out is a 422. The domain package checks the values (a day that the month
// doesn't have).
func (b RuleInput) rule() (occasion.Rule, error) {
	r := occasion.Rule{Month: time.Month(b.Month)}
	var needs []need
	switch b.Kind {
	case "annual":
		r.Kind, needs = occasion.KindAnnual, []need{{"day", b.Day != nil}}
	case "once":
		r.Kind, needs = occasion.KindOnce, []need{{"year", b.Year != nil}, {"day", b.Day != nil}}
	case "nth_weekday":
		r.Kind, needs = occasion.KindNthWeekday, []need{{"n", b.N != nil}, {"weekday", b.Weekday != nil}}
	case "last_weekday":
		r.Kind, needs = occasion.KindLastWeekday, []need{{"weekday", b.Weekday != nil}}
	}
	for _, n := range needs {
		if !n.set {
			return r, Error(http.StatusUnprocessableEntity, "invalid_input",
				fmt.Sprintf("A %s rule needs %q.", b.Kind, n.name),
				&huma.ErrorDetail{Location: "body.rule." + n.name, Message: "required for this kind of rule"})
		}
	}
	if b.Year != nil {
		r.Year = *b.Year
	}
	if b.Day != nil {
		r.Day = *b.Day
	}
	if b.Weekday != nil {
		r.Weekday = time.Weekday(*b.Weekday)
	}
	if b.N != nil {
		r.N = *b.N
	}
	return r, nil
}

// OptionalUser is a user summary, or null when there is none: the type of an
// occasion's `user`, which a shared occasion doesn't have. huma can't mark an
// object nullable on a field, so the type does it (see [OptionalImage]).
type OptionalUser struct{ *UserSummary }

// MarshalJSON writes null for no user.
func (o OptionalUser) MarshalJSON() ([]byte, error) {
	if o.UserSummary == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.UserSummary)
}

// Schema is the schema of a user summary that can also be null.
func (OptionalUser) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[UserSummary](), false, "")
	s.Nullable = true
	return &s
}

// OccasionBody is the full occasion (docs/routes.md, Occasion fields).
type OccasionBody struct {
	ID         string       `json:"id" example:"7" doc:"An opaque string. Don't parse it."`
	Kind       string       `json:"kind" enum:"birthday,shared,custom" doc:"birthday: every user has one, made with the user. custom: defined for one user by that user or their parent. shared: defined by the admin, for groups or everyone."`
	Name       string       `json:"name" doc:"A birthday's name is always Birthday."`
	Rule       RuleBody     `json:"rule" doc:"The date rule. A birthday's rule is the user's birthday, every year."`
	User       OptionalUser `json:"user" doc:"The user the occasion is for. null for a shared occasion."`
	IsEveryone *bool        `json:"is_everyone" nullable:"true" doc:"For a shared occasion: true when it applies to every user, including users created later, and false when it applies to the users in the groups it is attached to. null for the other kinds. Only the admin changes it."`
	NextDate   *string      `json:"next_date" nullable:"true" format:"date" doc:"YYYY-MM-DD: the first date of the occasion that is today or later. For a birthday or a custom occasion it is worked out where the user lives (their timezone, or the server's zone with none). A shared occasion has no user, so it is worked out where the caller lives (the caller's timezone, or the server's zone with none). null when none is left: a one-time date in the past."`

	// The `can_*` fields are the caller's.
	CanEdit        bool `json:"can_edit" doc:"True for a custom occasion when the caller is its user or the user's parent and the user is active, and for a shared occasion when the caller is the admin. Never true for a birthday."`
	CanDelete      bool `json:"can_delete" doc:"The same as can_edit."`
	CanAttachGroup bool `json:"can_attach_group" doc:"True for a shared occasion that is not marked everyone, when the caller is the admin."`
}

// occasionRow is an occasion in the occasions collection: the full occasion with
// its version.
type occasionRow struct {
	OccasionBody
	Version string `json:"version" doc:"The same value as the ETag of GET /occasions/{id} for this caller. Send it in If-Match."`
}

func newOccasionBody(o occasion.Occasion) OccasionBody {
	b := OccasionBody{
		ID:   formatID(o.ID),
		Kind: string(o.Type),
		Name: o.Name,
		Rule: newRuleBody(o.Rule),

		CanEdit:        o.CanEdit(),
		CanDelete:      o.CanDelete(),
		CanAttachGroup: o.CanAttachGroup(),
	}
	if o.User != nil {
		summary := userSummaries([]user.Summary{*o.User})[0]
		b.User = OptionalUser{&summary}
	}
	if o.Type == occasion.TypeShared {
		b.IsEveryone = &o.IsEveryone
	}
	if o.NextDate != nil {
		next := o.NextDate.String()
		b.NextDate = &next
	}
	return b
}

func newOccasionRows(occasions []occasion.Occasion) ([]occasionRow, error) {
	rows := make([]occasionRow, len(occasions))
	for i, o := range occasions {
		body := newOccasionBody(o)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = occasionRow{OccasionBody: body, Version: v}
	}
	return rows, nil
}

// occasionProblem turns an error of the occasion package into a problem with a
// code.
func occasionProblem(err error) error {
	var field *occasion.FieldError
	switch {
	case errors.Is(err, occasion.ErrNotFound):
		return notFound()
	case errors.Is(err, occasion.ErrBirthday):
		return Error(http.StatusForbidden, "birthday", "A birthday occasion follows the user's birthday. Nobody edits or deletes it.")
	case errors.Is(err, occasion.ErrNotEditable):
		return forbidden("Only the user or their parent can change their occasions, and only the admin changes a shared occasion.")
	case errors.Is(err, occasion.ErrIsEveryone):
		return Error(http.StatusConflict, "occasion_is_everyone", "This occasion applies to everyone, so no group can be attached. Unmark it first.")
	case errors.Is(err, occasion.ErrAlreadyAttached):
		return Error(http.StatusConflict, "already_attached", "That group is already attached to this occasion.")
	case errors.Is(err, occasion.ErrNotAttached):
		return notFound()
	case errors.Is(err, occasion.ErrGroupNotFound):
		return referenceNotFound("body.group_id", "That group doesn't exist, or you can't see it.")
	case errors.Is(err, occasion.ErrUserDeactivated):
		return userDeactivated()
	case errors.Is(err, occasion.ErrUserNotFound):
		return referenceNotFound("body.user_id", "That user doesn't exist, or you can't see them.")
	case errors.Is(err, occasion.ErrUserNotAllowed):
		return Error(http.StatusForbidden, "reference_not_allowed", "You can add an occasion for yourself or for your child.")
	case errors.As(err, &field):
		return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(),
			&huma.ErrorDetail{Location: "body." + field.Field, Message: err.Error()})
	}
	return err
}

// invalidField is a 422 invalid_input that names the field at location.
func invalidField(location, detail string) error {
	return Error(http.StatusUnprocessableEntity, "invalid_input", detail,
		&huma.ErrorDetail{Location: location, Message: detail})
}

// createShared is the create route for a shared occasion: only the admin makes
// one, and it has no user.
func (s *Server) createShared(ctx context.Context, in *createOccasionInput) (*createdOutput[OccasionBody], error) {
	caller, _ := CallerFrom(ctx)
	if err := requireAdmin(ctx, "makes shared occasions"); err != nil {
		return nil, err
	}
	if in.Body.UserID != nil {
		return nil, invalidField("body.user_id", "A shared occasion has no user.")
	}
	rule, err := in.Body.Rule.rule()
	if err != nil {
		return nil, err
	}
	n := occasion.NewShared{Name: in.Body.Name, Rule: rule, IsEveryone: in.Body.IsEveryone != nil && *in.Body.IsEveryone}
	o, err := s.opts.Occasions.CreateShared(ctx, caller.User, n)
	if err != nil {
		return nil, occasionProblem(err)
	}
	return createdOf("/occasions/"+formatID(o.ID), newOccasionBody(o))
}

type occasionInput struct {
	ID string `path:"id" example:"7" doc:"An occasion's ID."`
}

type listOccasionsInput struct {
	PageParams
	UserID         string `query:"user_id" doc:"Only the occasions that apply to this user. Not a field of the occasion. Empty is no filter."`
	Kind           string `query:"kind" enum:"birthday,shared,custom" doc:"Only the occasions of this kind. Empty is no filter."`
	ListID         string `query:"list_id" doc:"Only the occasions that a giver can pick for a gift on this list: the upcoming occasions of the list's recipients, other than the caller, that the caller can see. A list the caller can't see has none. Not a field of the occasion. Empty is no filter."`
	NextDateBefore string `query:"next_date_before" format:"date" doc:"YYYY-MM-DD: only the occasions whose next_date is on or before this date, so a client that wants the next 60 days sends today plus 60 days. An occasion with no next_date never matches. Not a field of the occasion."`
}

var occasionRules = collectionRules{Name: "occasions", Sorts: occasion.SortKeys, Filters: []string{"user_id", "kind", "list_id", "next_date_before"}}

type createOccasionInput struct {
	Body struct {
		Kind       string    `json:"kind" enum:"custom,shared" doc:"custom: for the caller or one of their children. shared: only the admin makes one."`
		Name       string    `json:"name" minLength:"1" maxLength:"100" doc:"The occasion's name."`
		Rule       RuleInput `json:"rule" doc:"The date rule."`
		UserID     *string   `json:"user_id,omitempty" required:"false" nullable:"true" example:"7" doc:"For a custom occasion: the user it is for, the caller (the default) or one of their children. A shared occasion has no user, so it is a 422 there."`
		IsEveryone *bool     `json:"is_everyone,omitempty" required:"false" nullable:"true" doc:"For a shared occasion: true marks it everyone. false, null, or leaving it out attaches it to no groups yet. A 422 for a custom occasion."`
	}
}

// occasionChanges are the fields of a custom occasion that its user or their
// parent may change with PATCH. The keys of the other fields in an occasion's
// body are refused with 403 field_not_writable.
type occasionChanges struct {
	Name       Field[string]    `json:"name,omitempty" minLength:"1" maxLength:"100" doc:"The occasion's name. It can't be null."`
	Rule       Field[RuleInput] `json:"rule,omitempty" doc:"The date rule. It replaces the whole rule. It can't be null."`
	IsEveryone Field[bool]      `json:"is_everyone,omitempty" doc:"A shared occasion only, and only the admin. true marks it everyone and detaches its groups. false unmarks it and leaves it attached to no groups. It can't be null."`
}

// occasionChangeKeys are the keys of an occasion that the caller may change: the
// name and the rule of a custom or shared occasion, and for a shared occasion
// whether it is everyone. For a custom occasion `is_everyone` is a key the caller
// sees (it is null) and can't change.
func occasionChangeKeys(o occasion.Occasion) []string {
	if o.Type == occasion.TypeShared {
		return []string{"name", "rule", "is_everyone"}
	}
	return []string{"name", "rule"}
}

type patchOccasionInput struct {
	ID      string `path:"id" example:"7" doc:"An occasion's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the occasion as you loaded it. If the occasion changed since, the answer is 412 resource_changed and nothing changes."`
	Body    MergePatch[occasionChanges]
}

type deleteOccasionInput struct {
	ID      string `path:"id" example:"7" doc:"An occasion's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the occasion as you loaded it. If the occasion changed since, the answer is 412 resource_changed and nothing is deleted."`
}

func (c occasionChanges) changes() (occasion.Changes, error) {
	out := occasion.Changes{Name: c.Name.Optional, IsEveryone: c.IsEveryone.Optional}
	if c.Rule.Set && c.Rule.Value != nil {
		rule, err := c.Rule.Value.rule()
		if err != nil {
			return out, err
		}
		out.Rule.Set, out.Rule.Value = true, &rule
	} else {
		out.Rule.Set = c.Rule.Set // null: the domain package refuses it
	}
	return out, nil
}

// occasionVersionGuard compares the If-Match header with the version of the
// occasion as the caller sees it, inside the write's transaction.
func occasionVersionGuard(ifMatch string) occasion.Guard {
	return func(_ context.Context, _ sqlx.ExtContext, current occasion.Occasion) error {
		v, err := versionOf(newOccasionBody(current))
		if err != nil {
			return err
		}
		return checkIfMatch(ifMatch, v)
	}
}

// visibleOccasion loads the occasion at a path, for a caller who must be able to
// see it. An occasion the caller can't see looks the same as one that doesn't
// exist.
func (s *Server) visibleOccasion(ctx context.Context, rawID string) (occasion.Occasion, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return occasion.Occasion{}, notFound()
	}
	o, err := s.opts.Occasions.GetVisible(ctx, caller.User, id)
	if err != nil {
		return occasion.Occasion{}, occasionProblem(err)
	}
	return o, nil
}

func (s *Server) registerOccasions(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-occasions",
		Method:      http.MethodGet,
		Path:        "/occasions",
		Summary:     "The occasions the caller can see",
		Description: "The birthday and custom occasions of the users the caller can see, and the shared occasions that apply to them or to a user they see. The admin's access to every user shows every birthday, and no custom occasion, and the admin sees every shared occasion. With `user_id`, a user's own occasions and the shared occasions that apply to them, when the caller sees the user. Sorted by `next_date` (soonest first), then by name. An occasion with no `next_date` is last, in both directions of the sort.",
		Tags:        []string{"Occasions"},
		Errors:      []int{http.StatusUnauthorized},
	}
	occasionRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listOccasionsInput) (*collectionOutput[occasionRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(occasionRules)
		if err != nil {
			return nil, err
		}
		var filter occasion.Filter
		if in.UserID != "" {
			// An ID that can't be a user's names nobody, whose occasions are none.
			id, _ := parseID(in.UserID)
			filter.UserID = &id
		}
		if in.ListID != "" {
			id, _ := parseID(in.ListID)
			filter.ListID = &id
		}
		if in.Kind != "" {
			kind := occasion.Type(in.Kind)
			filter.Type = &kind
		}
		if in.NextDateBefore != "" {
			d, err := occasion.ParseDate(in.NextDateBefore)
			if err != nil {
				return nil, Error(http.StatusUnprocessableEntity, "invalid_input", "next_date_before is a date written YYYY-MM-DD.",
					&huma.ErrorDetail{Location: "query.next_date_before", Message: "not a date", Value: in.NextDateBefore})
			}
			filter.NextDateBefore = &d
		}
		occasions, total, next, err := s.opts.Occasions.Visible(ctx, caller.User, filter, req)
		if err != nil {
			return nil, err
		}
		rows, err := newOccasionRows(occasions)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[occasionRow]{}
		out.Body.Data = rows
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID: "get-occasion",
		Method:      http.MethodGet,
		Path:        "/occasions/{id}",
		Summary:     "An occasion",
		Description: "An occasion the caller can't see is a 404. A shared occasion is seen by the admin, by every user it applies to, and by every user who sees a user it applies to.",
		Tags:        []string{"Occasions"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *occasionInput) (*resourceOutput[OccasionBody], error) {
		o, err := s.visibleOccasion(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newOccasionBody(o))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-occasion",
		Method:        http.MethodPost,
		Path:          "/occasions",
		Summary:       "Add a custom or shared occasion",
		Description:   "Makes a custom occasion for the caller, or for one of their children, or, for the admin, a shared occasion (`kind: shared`, with `is_everyone` to mark it everyone; any other caller gets a 403). A `user_id` the caller can't see is a 422 `reference_not_found`, one they see and aren't the parent of is a 403 `reference_not_allowed`, and a deactivated child is a 403 `user_deactivated`. The admin has no access to other users' custom occasions: for a user who isn't their child it is a 403 `reference_not_allowed` like anyone else.",
		Tags:          []string{"Occasions"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createOccasionInput) (*createdOutput[OccasionBody], error) {
		caller, _ := CallerFrom(ctx)
		if in.Body.Kind == "shared" {
			return s.createShared(ctx, in)
		}
		if in.Body.IsEveryone != nil {
			return nil, invalidField("body.is_everyone", "Only a shared occasion is marked everyone.")
		}
		rule, err := in.Body.Rule.rule()
		if err != nil {
			return nil, err
		}
		n := occasion.New{Name: in.Body.Name, Rule: rule}
		if raw := in.Body.UserID; raw != nil {
			// An ID that can't be a user's names nobody the caller can see.
			id, _ := parseID(*raw)
			n.UserID = &id
		}
		o, err := s.opts.Occasions.Create(ctx, caller.User, n)
		if err != nil {
			return nil, occasionProblem(err)
		}
		return createdOf("/occasions/"+formatID(o.ID), newOccasionBody(o))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-occasion",
		Method:      http.MethodPatch,
		Path:        "/occasions/{id}",
		Summary:     "Change an occasion's name or date rule, or mark a shared occasion everyone",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. " +
			"The occasion's user or their parent changes a custom occasion, while the user is active, and the admin changes a shared one, including `is_everyone`: marking it everyone detaches its groups, and unmarking it leaves it attached to none. A birthday is a 403 `birthday`: it follows the user's birthday. " +
			"A field the caller sees but can't change is a 403 `field_not_writable`. `If-Match` is optional.",
		Tags:   []string{"Occasions"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchOccasionInput) (*resourceOutput[OccasionBody], error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleOccasion(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if err := current.EditError(); err != nil {
			return nil, occasionProblem(err)
		}
		visible, err := keysOf(newOccasionBody(current))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(occasionChangeKeys(current), visible); err != nil {
			return nil, err
		}
		changes, err := in.Body.Changes.changes()
		if err != nil {
			return nil, err
		}
		updated, err := s.opts.Occasions.Update(ctx, caller.User, current.ID, changes, occasionVersionGuard(in.IfMatch))
		if err != nil {
			return nil, occasionProblem(err)
		}
		return resourceOf(newOccasionBody(updated))
	})

	acceptMergePatch(api, "/occasions/{id}")

	huma.Register(api, huma.Operation{
		OperationID:   "delete-occasion",
		Method:        http.MethodDelete,
		Path:          "/occasions/{id}",
		Summary:       "Delete a custom or shared occasion",
		Description:   "The occasion's user or their parent deletes a custom occasion, while the user is active, and the admin deletes a shared one with its attachments. A birthday is a 403 `birthday`: it goes only with its user. `If-Match` is optional.",
		Tags:          []string{"Occasions"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteOccasionInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleOccasion(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if err := current.EditError(); err != nil {
			return nil, occasionProblem(err)
		}
		if err := s.opts.Occasions.Delete(ctx, caller.User, current.ID, occasionVersionGuard(in.IfMatch)); err != nil {
			return nil, occasionProblem(err)
		}
		return nil, nil
	})
}
