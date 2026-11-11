// Package user owns the users table: who a user is, how to load, list, create
// and change one.
//
// This is the layout of a domain package, and the other domain packages follow it:
//
//   - The package holds the types, the rules that don't depend on HTTP, and the
//     SQL. It imports no HTTP package.
//   - A Service holds the database handle and the clock. Its methods are what
//     the HTTP layer calls.
//   - A function that must join another package's transaction is a plain
//     function that takes a [sqlx.ExtContext] (a database or a transaction),
//     such as [Create].
//   - A rule that fails returns a sentinel error ([ErrInvalid], [ErrNotFound]).
//     internal/server maps it to a problem with a code.
//   - A collection takes a [page.Request] and runs a keyset query (see
//     [Service.Visible]).
//   - A change is a struct of [patch.Optional] fields, and [Service.Update]
//     takes it with a guard that runs inside the transaction (see If-Match).
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/session"
)

const (
	// MaxNameLength is the longest first or last name, in characters.
	MaxNameLength = 100
	// MaxGoodToKnowLength is the longest "good to know" note, in characters.
	MaxGoodToKnowLength = 2000
	// MinBirthYear is the earliest year a birthday can have.
	MinBirthYear = 1900
)

var (
	// ErrNotFound means no user has the ID.
	ErrNotFound = errors.New("no such user")
	// ErrAdminCannotBeDeactivated means the user is the admin. To deactivate the
	// admin, the operator first moves the role with `wishbone set-admin`.
	ErrAdminCannotBeDeactivated = errors.New("the admin can't be deactivated")
	// ErrDeactivated means the user is deactivated, so the rule can't apply to
	// them (a deactivated user can't become the admin).
	ErrDeactivated = errors.New("the user is deactivated")
	// ErrInvalid means the input failed a rule. The message is safe to show.
	ErrInvalid = errors.New("invalid input")
	// ErrBirthdayRequired means a change cleared the birthday. Every user has
	// one. It matches [ErrInvalid].
	ErrBirthdayRequired error = invalidError{"every user has a birthday, so it can't be cleared"}
)

// invalidError is a message that matches [ErrInvalid] but reads as the message
// alone, which is safe to show.
type invalidError struct{ msg string }

func (e invalidError) Error() string        { return e.msg }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(format string, args ...any) error {
	return invalidError{fmt.Sprintf(format, args...)}
}

// FieldError says which input field failed a rule. It matches [ErrInvalid].
type FieldError struct {
	// Field is the name of the field in the input, such as "birthday".
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return &FieldError{Field: name, Err: err}
}

// PartialDate is a date that can have no year, such as a birthday. The month
// and the day are always there.
type PartialDate struct {
	Month int  `json:"month" minimum:"1" maximum:"12" example:"11"`
	Day   int  `json:"day" minimum:"1" maximum:"31" example:"5"`
	Year  *int `json:"year" required:"false" nullable:"true" doc:"The year, or null when it is not known."`
}

// Validate checks that the date is in the calendar. February 29 is valid with
// no year, and with a year only in a leap year. It sets no other limit: a
// birthday's limits are the user's rule (see [ValidateBirthday]).
func (d PartialDate) Validate() error {
	if d.Month < 1 || d.Month > 12 {
		return invalid("the month must be 1 to 12")
	}
	// With no year, check the day in a leap year, so February 29 is valid.
	year := 2000
	if d.Year != nil {
		year = *d.Year
		if year < 1 || year > 9999 {
			return invalid("the year must be 1 to 9999")
		}
	}
	lastDay := time.Date(year, time.Month(d.Month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d.Day < 1 || d.Day > lastDay {
		return invalid("that date is not in the calendar")
	}
	return nil
}

// ValidateBirthday checks a user's birthday: a date in the calendar, with a
// year, if it has one, from [MinBirthYear] to the year of now, and not after
// now.
func ValidateBirthday(d PartialDate, now time.Time) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Year == nil {
		return nil
	}
	if *d.Year < MinBirthYear || *d.Year > now.Year() {
		return invalid("the year must be %d to %d", MinBirthYear, now.Year())
	}
	if time.Date(*d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC).After(now) {
		return invalid("that date is in the future")
	}
	return nil
}

// User is a user as the database holds it.
type User struct {
	ID                 int64
	Name               string
	LastName           *string
	Birthday           PartialDate
	GoodToKnow         *string
	Timezone           *string
	IsAdmin            bool
	ProfileConfirmedAt *time.Time
	// DeactivatedAt is when the admin deactivated the user, or nil for an active
	// user (see [User.Active] and docs/spec.md, Deactivation).
	DeactivatedAt *time.Time
	CreatedAt     time.Time
	// Avatar is the user's avatar, or nil when there is none.
	Avatar *image.Summary
	// PrimaryList is the user's primary list. Every user has one, made with the
	// user (see [Create]). Whether a viewer may see it is the lists' rule
	// (internal/list), not the user's.
	PrimaryList ListSummary
	// Parents and Children are the user's direct links, by name. They are empty,
	// not nil, once the user is loaded. A link to a user the viewer can't see is
	// still here: a summary doesn't need visibility.
	Parents  []Summary
	Children []Summary
}

// ListSummary is a list as the user holds it: the ID and the name
// (docs/routes.md, Summary shapes).
type ListSummary struct {
	ID   int64
	Name string
}

// Active says whether the user is not deactivated. It is the one test of "the
// user is active": the rule of who sees a user ([visibleTo]) and the rule of who
// acts for them (canActFor in internal/server) both follow it.
func (u User) Active() bool { return u.DeactivatedAt == nil }

// NewUser is the input for [Create].
type NewUser struct {
	Name     string
	LastName string // empty for none
	Birthday PartialDate
	Timezone string // empty for none
	IsAdmin  bool
	// ParentIDs are the users to link as the new user's parents. [Service.Create]
	// refuses one that doesn't exist, and a parent for the admin.
	ParentIDs []int64
}

// cleanName trims a name and checks its length. A required name can't be empty.
func cleanName(name, label string, required bool) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case required && name == "":
		return "", invalid("the %s is required", label)
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "", invalid("the %s must be at most %d characters", label, MaxNameLength)
	}
	return name, nil
}

// validTimezone checks that a zone is the name of an IANA zone. "Local" is the
// server's zone, not a name a browser reports.
func validTimezone(zone string) error {
	if zone == "" || zone == "Local" || len(zone) > 64 {
		return invalid("the timezone must be the name of an IANA zone, such as Europe/Paris")
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return invalid("the timezone must be the name of an IANA zone, such as Europe/Paris")
	}
	return nil
}

// Validate trims the names and checks every field. It returns the cleaned input.
func (n NewUser) Validate(now time.Time) (NewUser, error) {
	var err error
	if n.Name, err = cleanName(n.Name, "name", true); err != nil {
		return n, field("name", err)
	}
	if n.LastName, err = cleanName(n.LastName, "last name", false); err != nil {
		return n, field("last_name", err)
	}
	if err := ValidateBirthday(n.Birthday, now); err != nil {
		return n, field("birthday", err)
	}
	if n.Timezone != "" {
		if err := validTimezone(n.Timezone); err != nil {
			return n, field("timezone", err)
		}
	}
	return n, nil
}

// Changes is a merge patch to a user: the fields that the caller may edit. A
// field that is absent doesn't change, and one that is `null` is cleared where
// it can be (see [Changes.Validate]).
type Changes struct {
	Name       patch.Optional[string]
	LastName   patch.Optional[string]
	Birthday   patch.Optional[PartialDate]
	GoodToKnow patch.Optional[string]
	Timezone   patch.Optional[string]
	// AvatarImageID attaches an image as the avatar, or removes the avatar when
	// it is null. [Service.Update] doesn't check that the caller may use the
	// image: its guard does, inside the transaction.
	AvatarImageID patch.Optional[int64]
	// ParentIDs replaces the user's parents. [Service.Update] refuses a loop, a
	// parent that doesn't exist, and a parent for the admin. The caller must
	// have checked that the caller is the admin. null is invalid: an empty list
	// removes every parent.
	ParentIDs patch.Optional[[]int64]
}

// Validate trims and checks the fields that are set, and returns the cleaned
// changes. An empty last name or note is the same as `null`. It returns
// [ErrBirthdayRequired] for a birthday that is cleared, and [ErrInvalid] for the
// rest.
func (c Changes) Validate(now time.Time) (Changes, error) {
	if c.Name.Set {
		if c.Name.Value == nil {
			return c, field("name", invalid("the name is required"))
		}
		name, err := cleanName(*c.Name.Value, "name", true)
		if err != nil {
			return c, field("name", err)
		}
		c.Name.Value = &name
	}
	if c.LastName.Set && c.LastName.Value != nil {
		last, err := cleanName(*c.LastName.Value, "last name", false)
		if err != nil {
			return c, field("last_name", err)
		}
		c.LastName.Value = nilIfEmpty(last)
	}
	if c.Birthday.Set {
		if c.Birthday.Value == nil {
			return c, field("birthday", ErrBirthdayRequired)
		}
		if err := ValidateBirthday(*c.Birthday.Value, now); err != nil {
			return c, field("birthday", err)
		}
	}
	if c.GoodToKnow.Set && c.GoodToKnow.Value != nil {
		note := strings.TrimSpace(*c.GoodToKnow.Value)
		if utf8.RuneCountInString(note) > MaxGoodToKnowLength {
			return c, field("good_to_know", invalid("the note must be at most %d characters", MaxGoodToKnowLength))
		}
		c.GoodToKnow.Value = nilIfEmpty(note)
	}
	if c.Timezone.Set && c.Timezone.Value != nil {
		if err := validTimezone(*c.Timezone.Value); err != nil {
			return c, field("timezone", err)
		}
	}
	if c.ParentIDs.Set && c.ParentIDs.Value == nil {
		return c, field("parent_ids", invalid("the parents can't be null: send an empty list to remove them all"))
	}
	return c, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// row is the shape of a users row.
type row struct {
	ID                 int64          `db:"id"`
	Name               string         `db:"name"`
	LastName           sql.NullString `db:"last_name"`
	BirthdayMonth      int            `db:"birthday_month"`
	BirthdayDay        int            `db:"birthday_day"`
	BirthdayYear       sql.NullInt64  `db:"birthday_year"`
	GoodToKnow         sql.NullString `db:"good_to_know"`
	Timezone           sql.NullString `db:"timezone"`
	IsAdmin            bool           `db:"is_admin"`
	ProfileConfirmedAt sql.NullTime   `db:"profile_confirmed_at"`
	DeactivatedAt      sql.NullTime   `db:"deactivated_at"`
	CreatedAt          time.Time      `db:"created_at"`
	AvatarImageID      sql.NullInt64  `db:"avatar_image_id"`
	AvatarWidth        sql.NullInt64  `db:"avatar_width"`
	AvatarHeight       sql.NullInt64  `db:"avatar_height"`
	PrimaryListID      sql.NullInt64  `db:"primary_list_id"`
	PrimaryListName    sql.NullString `db:"primary_list_name"`
}

const columns = `id, name, last_name, birthday_month, birthday_day, birthday_year, good_to_know, timezone, is_admin, profile_confirmed_at, deactivated_at, created_at,
	avatar_image_id,
	(SELECT width FROM images WHERE images.id = users.avatar_image_id) AS avatar_width,
	(SELECT height FROM images WHERE images.id = users.avatar_image_id) AS avatar_height,
	(SELECT id FROM lists WHERE lists.primary_user_id = users.id) AS primary_list_id,
	(SELECT name FROM lists WHERE lists.primary_user_id = users.id) AS primary_list_name`

func (r row) user() User {
	u := User{
		ID:        r.ID,
		Name:      r.Name,
		Birthday:  PartialDate{Month: r.BirthdayMonth, Day: r.BirthdayDay},
		IsAdmin:   r.IsAdmin,
		CreatedAt: r.CreatedAt,

		PrimaryList: ListSummary{ID: r.PrimaryListID.Int64, Name: r.PrimaryListName.String},
	}
	if r.LastName.Valid {
		u.LastName = &r.LastName.String
	}
	if r.BirthdayYear.Valid {
		y := int(r.BirthdayYear.Int64)
		u.Birthday.Year = &y
	}
	if r.GoodToKnow.Valid {
		u.GoodToKnow = &r.GoodToKnow.String
	}
	if r.Timezone.Valid {
		u.Timezone = &r.Timezone.String
	}
	if r.ProfileConfirmedAt.Valid {
		u.ProfileConfirmedAt = &r.ProfileConfirmedAt.Time
	}
	if r.DeactivatedAt.Valid {
		u.DeactivatedAt = &r.DeactivatedAt.Time
	}
	if r.AvatarImageID.Valid {
		u.Avatar = &image.Summary{ID: r.AvatarImageID.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
	}
	return u
}

// Service reads and changes users.
type Service struct {
	db       *sqlx.DB
	now      func() time.Time
	observer Observer
}

// Observer is told about a change to a user that can make an open gift wrong
// (docs/spec.md, Occasions, Occasions of a user). It runs inside the
// transaction of the change, so the change and what it does to the gifts commit
// together, and an error from it undoes the change. package gift implements it.
type Observer interface {
	// BirthdayChanged runs after the month or the day of a user's birthday
	// changed.
	BirthdayChanged(ctx context.Context, tx *sqlx.Tx, userID int64) error
	// VisibilityChanged runs after a parent link changed, with the IDs of the users
	// whose visibility to each other may have changed: the child and the old and
	// new parents.
	VisibilityChanged(ctx context.Context, tx *sqlx.Tx, userIDs []int64) error
	// Deactivated runs after a user was deactivated, before VisibilityChanged: the
	// user's open gifts are released (docs/spec.md, Deactivation and deletion,
	// Deactivation).
	Deactivated(ctx context.Context, tx *sqlx.Tx, userID int64) error
}

// Observe sets the observer of the service. Call it before the service is used.
func (s *Service) Observe(o Observer) { s.observer = o }

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// Get loads a user. It returns [ErrNotFound] when there is none.
func (s *Service) Get(ctx context.Context, id int64) (User, error) {
	return Get(ctx, s.db, id)
}

// List returns every user, the oldest ID first. It is for the CLI: the API
// lists the users that the caller can see.
func (s *Service) List(ctx context.Context) ([]User, error) {
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, `SELECT `+columns+` FROM users ORDER BY id`); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = r.user()
	}
	if err := loadLinks(ctx, s.db, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SortKeys are the sort keys of the users collection (docs/routes.md, User
// routes). The first is the default.
var SortKeys = []page.Key{{Name: "name"}, {Name: "created_at", Time: true}}

// sortColumn is the SQL of a sort key. A name sorts with no regard to case.
var sortColumn = map[string]string{
	"name":       "name COLLATE NOCASE",
	"created_at": "created_at",
}

// Visible returns one page of the users that the viewer can see, and how many
// users they can see in all. It returns the next cursor, or nil on the last page.
func (s *Service) Visible(ctx context.Context, viewer User, req page.Request) (users []User, total int, next *string, err error) {
	return s.collect(ctx, viewer, req, "1 = 1", nil)
}

// InGroup returns one page of the users directly in a group whom the viewer
// can see, like [Service.Visible]. The group is not checked: the caller must
// have shown that the viewer may see it.
func (s *Service) InGroup(ctx context.Context, viewer User, groupID int64, req page.Request) (users []User, total int, next *string, err error) {
	return s.collect(ctx, viewer, req, "users.id IN (SELECT user_id FROM group_users WHERE group_id = ?)", []any{groupID})
}

// InGroupTree returns one page of the users who are in a group, directly or
// through nesting, and whom the viewer can see, like [Service.Visible]. A group
// that the viewer can't see, or that doesn't exist, has no users here: the
// answer is an empty page, and doesn't show whether the group exists.
func (s *Service) InGroupTree(ctx context.Context, viewer User, groupID int64, req page.Request) (users []User, total int, next *string, err error) {
	where, args := inGroupTree(viewer, groupID)
	return s.collect(ctx, viewer, req, where, args)
}

// Filter narrows the users a viewer sees. Every field that is set must hold: the
// viewer still sees only the users that [visibleTo] lets them see.
type Filter struct {
	// GroupID keeps the users who are in this group, directly or through nesting,
	// as [Service.InGroupTree] does.
	GroupID *int64
	// Where is a SQL condition on a row of the `users` table, with its arguments.
	// Another package builds it (the users a viewer can invite to a list, in
	// internal/list), because the rule isn't about users alone. Empty is no
	// condition.
	Where     string
	WhereArgs []any
}

// Matching returns one page of the users whom the viewer can see and that match
// the filter, like [Service.Visible].
func (s *Service) Matching(ctx context.Context, viewer User, f Filter, req page.Request) (users []User, total int, next *string, err error) {
	where, args := []string{"1 = 1"}, []any{}
	if f.GroupID != nil {
		cond, condArgs := inGroupTree(viewer, *f.GroupID)
		where, args = append(where, cond), append(args, condArgs...)
	}
	if f.Where != "" {
		where, args = append(where, f.Where), append(args, f.WhereArgs...)
	}
	return s.collect(ctx, viewer, req, strings.Join(where, " AND "), args)
}

// collect runs the keyset query of a page of users: the users that match
// where, and that the viewer can see (see [visibleTo]).
func (s *Service) collect(ctx context.Context, viewer User, req page.Request, where string, whereArgs []any) (users []User, total int, next *string, err error) {
	seen, seenArgs := visibleTo(viewer)
	where = "(" + where + ") AND " + seen
	args := append(append([]any{}, whereArgs...), seenArgs...)
	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM users WHERE `+where, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("count users: %w", err)
	}

	col, ok := sortColumn[req.Key]
	if !ok {
		return nil, 0, nil, fmt.Errorf("list users: no sort key %q", req.Key)
	}
	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query := `SELECT ` + columns + ` FROM users WHERE ` + where
	if req.After != nil {
		var after any = req.After.Value
		if req.Key == "created_at" {
			t, err := time.Parse(time.RFC3339Nano, req.After.Value)
			if err != nil {
				return nil, 0, nil, page.ErrBadCursor
			}
			after = t
		}
		query += ` AND (` + col + ` ` + cmp + ` ? OR (` + col + ` = ? AND id ` + cmp + ` ?))`
		args = append(args, after, after, req.After.ID)
	}
	query += ` ORDER BY ` + col + ` ` + dir + `, id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []row
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list users: %w", err)
	}
	all := make([]User, len(rows))
	for i, r := range rows {
		all[i] = r.user()
	}
	if err := loadLinks(ctx, s.db, all); err != nil {
		return nil, 0, nil, err
	}
	users, next = page.Slice(req, all, func(u User) page.Position {
		if req.Key == "created_at" {
			return page.Position{Value: page.FormatTime(u.CreatedAt), ID: u.ID}
		}
		return page.Position{Value: u.Name, ID: u.ID}
	})
	return users, total, next, nil
}

// GetVisible loads a user whom the viewer can see. It returns [ErrNotFound]
// when there is no such user, and when the viewer can't see them: the two look
// the same.
func (s *Service) GetVisible(ctx context.Context, viewer User, id int64) (User, error) {
	seen, args := visibleTo(viewer)
	var r row
	err := s.db.GetContext(ctx, &r, `SELECT `+columns+` FROM users WHERE id = ? AND `+seen, append([]any{id}, args...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return withLinks(ctx, s.db, r.user())
}

// withLinks loads the links of a user.
func withLinks(ctx context.Context, q sqlx.QueryerContext, u User) (User, error) {
	one := []User{u}
	if err := loadLinks(ctx, q, one); err != nil {
		return User{}, err
	}
	return one[0], nil
}

// CanSee says whether the viewer can see the user with the given ID.
func (s *Service) CanSee(ctx context.Context, viewer User, id int64) (bool, error) {
	return CanSee(ctx, s.db, viewer, id)
}

// CanSeeSummary says whether the viewer gets the summary of the user with the
// given ID (see the package function).
func (s *Service) CanSeeSummary(ctx context.Context, viewer User, id int64) (bool, error) {
	return CanSeeSummary(ctx, s.db, viewer, id)
}

// Create validates and inserts a user, and returns them. It returns
// [ErrInvalid] for input that fails a rule.
func (s *Service) Create(ctx context.Context, n NewUser) (User, error) {
	now := s.now()
	n, err := n.Validate(now)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	id, err := Create(ctx, tx, now, n)
	if err != nil {
		return User{}, err
	}
	u, err := Get(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if len(n.ParentIDs) > 0 {
		if err := setParents(ctx, tx, u, n.ParentIDs); err != nil {
			return User{}, err
		}
		if u, err = Get(ctx, tx, id); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// Guard runs inside the transaction of [Service.Update], before the write, with
// the user as it is before. q is the transaction: a guard reads other rows
// through it, never through the database handle, which the transaction holds.
// An error from it stops the update.
type Guard func(ctx context.Context, q sqlx.ExtContext, current User) error

// Update applies a merge patch to a user and returns the user as it is after.
// guard, if there is one, runs inside the same transaction as the write. An
// If-Match check goes there, so no write can slip in between the check and the
// update, and so does the check that the caller may use an image.
//
// A new avatar lets go of the old one: the old image is detached, and the image
// sweep deletes it.
func (s *Service) Update(ctx context.Context, id int64, c Changes, guard Guard) (User, error) {
	c, err := c.Validate(s.now())
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := Get(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return User{}, err
		}
	}
	var sets []string
	var args []any
	assign := func(column string, value any) {
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	if c.Name.Set {
		assign("name", *c.Name.Value)
		// A primary list's name follows the user's, until a rename of the list.
		if _, err := tx.ExecContext(ctx, `UPDATE lists SET name = ? WHERE primary_user_id = ? AND name_follows_user = 1`, *c.Name.Value, id); err != nil {
			return User{}, fmt.Errorf("rename the primary list: %w", err)
		}
	}
	if c.LastName.Set {
		assign("last_name", c.LastName.Value) // a nil pointer is NULL
	}
	if c.Birthday.Set {
		b := *c.Birthday.Value
		assign("birthday_month", b.Month)
		assign("birthday_day", b.Day)
		assign("birthday_year", b.Year)
	}
	if c.GoodToKnow.Set {
		assign("good_to_know", c.GoodToKnow.Value)
	}
	if c.Timezone.Set {
		assign("timezone", c.Timezone.Value)
	}
	if c.AvatarImageID.Set {
		unchanged := current.Avatar != nil && c.AvatarImageID.Value != nil && current.Avatar.ID == *c.AvatarImageID.Value
		if !unchanged {
			assign("avatar_image_id", c.AvatarImageID.Value) // a nil pointer is NULL
			if current.Avatar != nil {
				if err := image.Detach(ctx, tx, s.now(), current.Avatar.ID); err != nil {
					return User{}, err
				}
			}
		}
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := tx.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return User{}, fmt.Errorf("update user: %w", err)
		}
	}
	if c.ParentIDs.Set {
		if err := setParents(ctx, tx, current, *c.ParentIDs.Value); err != nil {
			return User{}, err
		}
		if s.observer != nil {
			ids := []int64{id}
			for _, p := range current.Parents {
				ids = append(ids, p.ID)
			}
			ids = append(ids, *c.ParentIDs.Value...)
			slices.Sort(ids)
			if err := s.observer.VisibilityChanged(ctx, tx, slices.Compact(ids)); err != nil {
				return User{}, err
			}
		}
	}
	if c.Birthday.Set && s.observer != nil {
		b := *c.Birthday.Value
		if b.Month != current.Birthday.Month || b.Day != current.Birthday.Day {
			if err := s.observer.BirthdayChanged(ctx, tx, id); err != nil {
				return User{}, err
			}
		}
	}
	u, err := Get(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// ConfirmProfile sets the time the user confirmed their profile, and returns
// the user. A profile that is already confirmed keeps its first time.
func (s *Service) ConfirmProfile(ctx context.Context, id int64) (User, error) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET profile_confirmed_at = ? WHERE id = ? AND profile_confirmed_at IS NULL`, s.now(), id); err != nil {
		return User{}, fmt.Errorf("confirm profile: %w", err)
	}
	return s.Get(ctx, id)
}

// SetAdmin makes the user the admin, and tells the observer, in the same
// transaction, that the old and the new admin see other users now. The old admin becomes a normal user and
// keeps their sessions. It returns the user, and the old admin, which is nil when
// the user already was the admin. It returns [ErrNotFound] for a user that
// doesn't exist, and [ErrHasParents] for a user who has parents (with the user, so
// the caller can name them), and [ErrDeactivated] for a user who is
// deactivated (also with the user).
func (s *Service) SetAdmin(ctx context.Context, id int64) (target User, previous *User, err error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	target, err = Get(ctx, tx, id)
	if err != nil {
		return User{}, nil, err
	}
	if target.IsAdmin {
		return target, nil, nil
	}
	if len(target.Parents) > 0 {
		return target, nil, ErrHasParents // the user, so the caller can name them
	}
	if !target.Active() {
		return target, nil, ErrDeactivated // the user, so the caller can name them
	}
	var old row
	err = sqlx.GetContext(ctx, tx, &old, `SELECT `+columns+` FROM users WHERE is_admin = 1`)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, nil, fmt.Errorf("find the admin: %w", err)
	}
	// Only one user can be the admin, so the old one goes first.
	if _, err := tx.ExecContext(ctx, `UPDATE users SET is_admin = 0 WHERE is_admin = 1`); err != nil {
		return User{}, nil, fmt.Errorf("clear the admin: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET is_admin = 1 WHERE id = ?`, id); err != nil {
		return User{}, nil, fmt.Errorf("set the admin: %w", err)
	}
	// The admin sees every user, so the role changes which recipients the old and
	// the new admin can give to.
	if s.observer != nil {
		ids := []int64{id}
		if old.ID != 0 {
			ids = append(ids, old.ID)
		}
		if err := s.observer.VisibilityChanged(ctx, tx, ids); err != nil {
			return User{}, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, nil, fmt.Errorf("commit: %w", err)
	}
	target.IsAdmin = true
	if old.ID != 0 {
		u := old.user()
		previous = &u
	}
	return target, previous, nil
}

// Deactivate pauses a user: it sets deactivated_at and revokes every live
// session of theirs, and tells the observer, in one transaction. So the open
// gifts of the user are released, and the open gifts that depend on who sees the
// user are settled, at once (docs/spec.md, Deactivation and deletion). It
// changes nothing for a user who is already deactivated (they keep their time),
// and returns [ErrAdminCannotBeDeactivated] for the admin. It changes no list,
// link, group, or login link of the user, so Reactivate brings them back as they
// were.
func (s *Service) Deactivate(ctx context.Context, id int64) (User, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	u, err := Get(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if u.IsAdmin {
		return User{}, ErrAdminCannotBeDeactivated
	}
	if u.Active() {
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE users SET deactivated_at = ? WHERE id = ?`, now, id); err != nil {
			return User{}, fmt.Errorf("deactivate user: %w", err)
		}
		if err := session.RevokeAll(ctx, tx, now, id); err != nil {
			return User{}, err
		}
		if s.observer != nil {
			if err := s.observer.Deactivated(ctx, tx, id); err != nil {
				return User{}, err
			}
			if err := s.observer.VisibilityChanged(ctx, tx, []int64{id}); err != nil {
				return User{}, err
			}
		}
		if u, err = Get(ctx, tx, id); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// Reactivate brings a deactivated user back. Their revoked sessions stay
// revoked, and their login links that haven't expired work again. It changes
// nothing for an active user. The observer settles the open gifts in the same
// transaction: a giver sees the user again. A gift that moved or was flagged
// while the user was deactivated stays as it is.
func (s *Service) Reactivate(ctx context.Context, id int64) (User, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	u, err := Get(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if !u.Active() {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET deactivated_at = NULL WHERE id = ?`, id); err != nil {
			return User{}, fmt.Errorf("reactivate user: %w", err)
		}
		if s.observer != nil {
			if err := s.observer.VisibilityChanged(ctx, tx, []int64{id}); err != nil {
				return User{}, err
			}
		}
		if u, err = Get(ctx, tx, id); err != nil {
			return User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// Create inserts a user and returns its ID. It checks nothing: call
// [NewUser.Validate] first.
func Create(ctx context.Context, q sqlx.ExtContext, now time.Time, n NewUser) (int64, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO users (name, last_name, birthday_month, birthday_day, birthday_year, timezone, is_admin, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, nilIfEmpty(n.LastName), n.Birthday.Month, n.Birthday.Day, n.Birthday.Year, nilIfEmpty(n.Timezone), n.IsAdmin, now)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	if err := createPrimaryList(ctx, q, now, id, n.Name); err != nil {
		return 0, err
	}
	if err := createBirthdayOccasion(ctx, q, now, id); err != nil {
		return 0, err
	}
	return id, nil
}

// createBirthdayOccasion makes the birthday occasion of a new user (docs/spec.md,
// Occasions, Occasions of a user). It stores no date: the occasion's date follows
// the user's birthday. [Create] is the one place that calls it, so every path that
// makes a user makes the occasion too.
func createBirthdayOccasion(ctx context.Context, q sqlx.ExtContext, now time.Time, userID int64) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO occasions (kind, name, user_id, created_at) VALUES ('birthday', 'Birthday', ?, ?)`, userID, now); err != nil {
		return fmt.Errorf("insert birthday occasion: %w", err)
	}
	return nil
}

// createPrimaryList makes the primary list of a new user (docs/spec.md,
// Lists, Primary list): named after the user, with the user as its only
// recipient. [Create] is the one place that calls it, so every path that makes a
// user (setup, POST /users, a test) makes the list too.
func createPrimaryList(ctx context.Context, q sqlx.ExtContext, now time.Time, userID int64, name string) error {
	res, err := q.ExecContext(ctx,
		`INSERT INTO lists (name, primary_user_id, name_follows_user, created_at) VALUES (?, ?, 1, ?)`, name, userID, now)
	if err != nil {
		return fmt.Errorf("insert primary list: %w", err)
	}
	listID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("insert primary list: %w", err)
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (?, ?, ?)`, listID, userID, now); err != nil {
		return fmt.Errorf("add the recipient of a primary list: %w", err)
	}
	return nil
}

// Count returns how many users there are.
func Count(ctx context.Context, q sqlx.QueryerContext) (int, error) {
	var n int
	if err := sqlx.GetContext(ctx, q, &n, `SELECT count(*) FROM users`); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// Get loads a user, with their parents and children, from a database or a
// transaction.
func Get(ctx context.Context, q sqlx.QueryerContext, id int64) (User, error) {
	var r row
	err := sqlx.GetContext(ctx, q, &r, `SELECT `+columns+` FROM users WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return withLinks(ctx, q, r.user())
}
