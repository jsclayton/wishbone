package occasion

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
	"jsclayton.dev/wishbone/internal/user"
)

// MaxNameLength is the longest occasion name, in characters.
const MaxNameLength = 100

// BirthdayName is the name of every birthday occasion.
const BirthdayName = "Birthday"

var (
	// ErrNotFound means no occasion has the ID, or the viewer can't see it. The
	// two look the same.
	ErrNotFound = errors.New("no such occasion")
	// ErrBirthday means nobody edits or deletes a birthday occasion: its date
	// follows the user's birthday, and its name is always "Birthday".
	ErrBirthday = errors.New("a birthday occasion can't be changed or deleted")
	// ErrNotEditable means the viewer sees the occasion but may not change it: a
	// custom occasion is changed by its user and their parent, a shared one by the
	// admin.
	ErrNotEditable = errors.New("you can't change this occasion")
	// ErrNotShared means the occasion is not a shared occasion, so it has no
	// groups.
	ErrNotShared = errors.New("this occasion is not a shared occasion")
	// ErrIsEveryone means a group can't be attached to a shared occasion while
	// it is marked everyone.
	ErrIsEveryone = errors.New("the occasion is marked everyone")
	// ErrAlreadyAttached means the group is already attached to the occasion.
	ErrAlreadyAttached = errors.New("the group is already attached")
	// ErrNotAttached means the group is not attached to the occasion.
	ErrNotAttached = errors.New("the group is not attached")
	// ErrGroupNotFound means the group to attach does not exist.
	ErrGroupNotFound = errors.New("no such group to attach")
	// ErrUserDeactivated means the occasion's user is deactivated, so nobody
	// writes through them.
	ErrUserDeactivated = errors.New("the user of this occasion is deactivated")
	// ErrUserNotFound means the user an occasion is for doesn't exist, or the
	// viewer can't see them.
	ErrUserNotFound = errors.New("no such user to make the occasion for")
	// ErrUserNotAllowed means the viewer sees the user an occasion is for, and is
	// neither that user nor their parent.
	ErrUserNotAllowed = errors.New("you can only add an occasion for yourself or your child")
	// ErrInvalid means the input failed a rule. The message is safe to show.
	ErrInvalid = errors.New("invalid input")
)

type invalidError struct{ msg string }

func (e invalidError) Error() string        { return e.msg }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

// FieldError says which input field failed a rule. It matches [ErrInvalid].
type FieldError struct {
	// Field is the name of the field in the input, such as "name".
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func invalid(field, format string, args ...any) error {
	return &FieldError{Field: field, Err: invalidError{fmt.Sprintf(format, args...)}}
}

// Type says where an occasion comes from (docs/routes.md, Occasion fields).
type Type string

const (
	// TypeBirthday is the birthday of a user. Every user has one.
	TypeBirthday Type = "birthday"
	// TypeShared is an occasion that the admin defines for groups or everyone.
	TypeShared Type = "shared"
	// TypeCustom is an occasion that a user or their parent defines for the user.
	TypeCustom Type = "custom"
)

// Occasion is an occasion as the viewer sees it.
type Occasion struct {
	ID   int64
	Type Type
	Name string
	// Rule is the date rule. A birthday's rule is the user's birthday, every
	// year.
	Rule Rule
	// User is the user the occasion is for, or nil for a shared occasion.
	User *user.Summary
	// NextDate is the first date of the occasion on or after today, or nil when
	// none is left (a one-time date in the past). Today is the date where the
	// user lives, and for a shared occasion, which has no user, where the viewer
	// lives (see [row.occasion]).
	NextDate *Date
	// IsEveryone is true for a shared occasion that applies to every user. It is
	// false for the other kinds.
	IsEveryone bool

	// Manages is true when the viewer is the occasion's user or their parent (see
	// [ManagedBy]). UserActive is false while the user is deactivated. Admin is
	// true when the viewer is the admin.
	Manages    bool
	UserActive bool
	Admin      bool
}

// CanEdit says whether the viewer can change the occasion: a custom occasion,
// for the user or their parent, while the user is active, and a shared occasion,
// for the admin.
func (o Occasion) CanEdit() bool { return o.EditError() == nil }

// CanDelete says whether the viewer can delete the occasion. It is the same
// rule as [Occasion.CanEdit].
func (o Occasion) CanDelete() bool { return o.CanEdit() }

// CanAttachGroup says whether the viewer can attach a group to the occasion: the
// admin, for a shared occasion that is not marked everyone.
func (o Occasion) CanAttachGroup() bool {
	return o.Type == TypeShared && o.Admin && !o.IsEveryone
}

// SortKeys are the sort keys of the occasions collection (docs/routes.md,
// Occasion routes). The first is the default.
var SortKeys = []page.Key{{Name: "next_date"}, {Name: "name"}}

// Filter narrows the occasions collection. A nil field is no filter.
type Filter struct {
	// UserID keeps the occasions that apply to this user: the user's birthday and
	// custom occasions, and the shared occasions that reach them, when the viewer
	// can see the user.
	UserID *int64
	Type   *Type
	// NextDateBefore keeps the occasions that have a next date on or before this
	// date. An occasion with no next date is never kept.
	NextDateBefore *Date
	// ListID keeps the occasions that a giver can pick for a gift on this list
	// ([PickableFor]) and that have a next date.
	ListID *int64
}

// Changes is a merge patch to a custom occasion.
type Changes struct {
	Name patch.Optional[string]
	Rule patch.Optional[Rule]
	// IsEveryone marks a shared occasion everyone, or unmarks it. Marking it
	// detaches its groups.
	IsEveryone patch.Optional[bool]
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", invalid("name", "an occasion needs a name")
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "", invalid("name", "the name must be at most %d characters", MaxNameLength)
	}
	return name, nil
}

func validRule(r Rule) error {
	if err := r.Validate(); err != nil {
		return invalid("rule", "%s", strings.TrimPrefix(err.Error(), "occasion: "))
	}
	return nil
}

// validate trims and checks the fields that are set.
func (c Changes) validate() (Changes, error) {
	if c.Name.Set {
		if c.Name.Value == nil {
			return c, invalid("name", "an occasion needs a name")
		}
		name, err := validName(*c.Name.Value)
		if err != nil {
			return c, err
		}
		c.Name.Value = &name
	}
	if c.Rule.Set {
		if c.Rule.Value == nil {
			return c, invalid("rule", "an occasion needs a date rule")
		}
		if err := validRule(*c.Rule.Value); err != nil {
			return c, err
		}
	}
	if c.IsEveryone.Set && c.IsEveryone.Value == nil {
		return c, invalid("is_everyone", "is_everyone is true or false")
	}
	return c, nil
}

// row is an occasion as selected, with its user. A shared occasion has no user.
type row struct {
	ID          int64          `db:"id"`
	Kind        string         `db:"kind"`
	Name        string         `db:"name"`
	UserID      sql.NullInt64  `db:"user_id"`
	IsEveryone  bool           `db:"is_everyone"`
	RuleKind    sql.NullString `db:"rule_kind"`
	RuleYear    sql.NullInt64  `db:"rule_year"`
	RuleMonth   sql.NullInt64  `db:"rule_month"`
	RuleDay     sql.NullInt64  `db:"rule_day"`
	RuleWeekday sql.NullInt64  `db:"rule_weekday"`
	RuleNth     sql.NullInt64  `db:"rule_nth"`

	UserName     sql.NullString `db:"user_name"`
	AvatarImage  sql.NullInt64  `db:"avatar_image_id"`
	AvatarWidth  sql.NullInt64  `db:"avatar_width"`
	AvatarHeight sql.NullInt64  `db:"avatar_height"`
	Timezone     sql.NullString `db:"timezone"`
	BirthdayMon  int            `db:"birthday_month"`
	BirthdayDay  int            `db:"birthday_day"`
	UserActive   bool           `db:"user_active"`
	Manages      bool           `db:"manages"`
}

// rule is the row's date rule. A birthday has none stored: it is the user's
// birthday, every year.
func (r row) rule() Rule {
	m := time.Month(r.RuleMonth.Int64)
	switch r.RuleKind.String {
	case "once":
		return Once(int(r.RuleYear.Int64), m, int(r.RuleDay.Int64))
	case "nth_weekday":
		return NthWeekday(int(r.RuleNth.Int64), time.Weekday(r.RuleWeekday.Int64), m)
	case "last_weekday":
		return LastWeekday(time.Weekday(r.RuleWeekday.Int64), m)
	case "annual":
		return Annual(m, int(r.RuleDay.Int64))
	}
	return Annual(time.Month(r.BirthdayMon), r.BirthdayDay)
}

// location is the zone in which "today" is worked out: a user with no timezone,
// or one the system doesn't know, is in the fallback zone.
func location(zone *string, fallback *time.Location) *time.Location {
	if zone != nil {
		if l, err := time.LoadLocation(*zone); err == nil {
			return l
		}
	}
	return fallback
}

// occasion turns a row into an occasion as the viewer sees it.
//
// "Today" is the date where the occasion's user lives. A shared occasion has no
// user, so it is the date where the viewer lives: the same occasion has one
// next date for a viewer wherever they look at it (the collection, a user's
// occasions, its own address), and nobody's timezone is revealed.
func (r row) occasion(now time.Time, fallback *time.Location, viewer user.User) Occasion {
	o := Occasion{
		ID: r.ID, Type: Type(r.Kind), Name: r.Name, Rule: r.rule(), IsEveryone: r.IsEveryone,
		Manages: r.Manages, UserActive: r.UserActive, Admin: viewer.IsAdmin,
	}
	zone := viewer.Timezone
	if r.UserID.Valid {
		o.User = &user.Summary{ID: r.UserID.Int64, Name: r.UserName.String}
		if r.AvatarImage.Valid {
			o.User.Avatar = &image.Summary{ID: r.AvatarImage.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
		}
		zone = nil
		if r.Timezone.Valid {
			zone = &r.Timezone.String
		}
	}
	if d, ok := o.Rule.Next(DateOf(now, location(zone, fallback))); ok {
		o.NextDate = &d
	}
	return o
}

// selectSQL selects an occasion as the viewer sees it, with its user. Its
// arguments are the viewer's role, and come first. A shared occasion has no user,
// so the user's columns are NULL, or a neutral value for a flag.
func selectSQL(viewer user.User) (string, []any) {
	managed, args := ManagedBy(viewer)
	return `SELECT occasions.id, occasions.kind, occasions.name, occasions.user_id, occasions.is_everyone,
		occasions.rule_kind, occasions.rule_year, occasions.rule_month, occasions.rule_day, occasions.rule_weekday, occasions.rule_nth,
		owner.name AS user_name, owner.avatar_image_id,
		(SELECT width FROM images WHERE images.id = owner.avatar_image_id) AS avatar_width,
		(SELECT height FROM images WHERE images.id = owner.avatar_image_id) AS avatar_height,
		owner.timezone, COALESCE(owner.birthday_month, 0) AS birthday_month, COALESCE(owner.birthday_day, 0) AS birthday_day,
		owner.deactivated_at IS NULL AS user_active,
		COALESCE((` + managed + `), 0) AS manages
		FROM occasions LEFT JOIN users owner ON owner.id = occasions.user_id`, args
}

// Observer is told about a change to an occasion that can make an open gift
// wrong (docs/spec.md, Gifts, Changes while a gift is open). It runs inside the
// transaction of the change, so the change and what it does to the gifts commit
// together, and an error from it undoes the change. package gift implements it.
type Observer interface {
	// Deleting runs before the occasion is deleted, while the gifts for it still
	// point at it.
	Deleting(ctx context.Context, tx *sqlx.Tx, id int64) error
	// Changed runs after the users the occasion applies to, or its date rule,
	// changed. rule is true when the date rule changed.
	Changed(ctx context.Context, tx *sqlx.Tx, id int64, rule bool) error
}

// Service reads and changes occasions.
type Service struct {
	db       *sqlx.DB
	now      func() time.Time
	fallback *time.Location
	observer Observer
}

// Option changes how a [Service] is built.
type Option func(*Service)

// WithFallbackZone sets the zone of a user who has no timezone (and of a caller
// who has none, for a shared occasion). The default is UTC. cmd/wishbone passes
// the server's own zone, so a test sets the zone without touching TZ.
func WithFallbackZone(loc *time.Location) Option {
	return func(s *Service) { s.fallback = loc }
}

// Observe sets the observer of the service. Call it before the service is used.
func (s *Service) Observe(o Observer) { s.observer = o }

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time, opts ...Option) *Service {
	s := &Service{db: db, now: func() time.Time { return now().UTC() }, fallback: time.UTC}
	for _, o := range opts {
		o(s)
	}
	return s
}

// FallbackZone is the zone of a user who has no timezone.
func (s *Service) FallbackZone() *time.Location { return s.fallback }

// noDate is the date part of the sort key of an occasion with no next date. It
// sorts after every digit.
const noDate = "~"

// compareSort orders two positions in the collection's sort: the sort key, then
// the ID, both reversed for a descending sort. An occasion with no next date is
// last in both directions of `next_date` (docs/routes.md, Occasion routes).
func compareSort(req page.Request, aKey string, aID int64, bKey string, bID int64) int {
	if req.Key == "next_date" {
		aNone, bNone := strings.HasPrefix(aKey, noDate), strings.HasPrefix(bKey, noDate)
		switch {
		case aNone && !bNone:
			return 1
		case bNone && !aNone:
			return -1
		}
	}
	c := strings.Compare(aKey, bKey)
	if c == 0 {
		switch {
		case aID < bID:
			c = -1
		case aID > bID:
			c = 1
		}
	}
	if req.Desc {
		return -c
	}
	return c
}

// sortKey is the value an occasion sorts by. A next date sorts as text, with no
// date last, then by name: the key is the date, then the name in lower case.
func sortKey(o Occasion, key string) string {
	name := strings.ToLower(o.Name)
	if key == "name" {
		return name
	}
	date := noDate
	if o.NextDate != nil {
		date = o.NextDate.String()
	}
	return date + "\x1f" + name
}

// load returns the occasions that the viewer can see and that match the filter,
// with their next dates, in no order. q is a database or a transaction.
func (s *Service) load(ctx context.Context, q sqlx.QueryerContext, viewer user.User, f Filter) ([]Occasion, error) {
	seen, seenArgs := VisibleTo(viewer)
	where := []string{seen}
	whereArgs := seenArgs
	if f.UserID != nil {
		// The user's own occasions, and the shared occasions that apply to them.
		// The shared ones count only when the viewer sees the user, so the filter
		// doesn't show that a user exists.
		applies, appliesArgs := AppliesToUser(*f.UserID)
		seenUser, seenUserArgs := user.VisibleTo(viewer)
		where = append(where, `(occasions.user_id = ? OR (`+applies+` AND ? IN (SELECT users.id FROM users WHERE `+seenUser+`)))`)
		whereArgs = slices.Concat(whereArgs, []any{*f.UserID}, appliesArgs, []any{*f.UserID}, seenUserArgs)
	}
	if f.Type != nil {
		where, whereArgs = append(where, "occasions.kind = ?"), append(whereArgs, string(*f.Type))
	}
	if f.ListID != nil {
		pickable, pickableArgs := PickableFor(viewer, *f.ListID)
		where, whereArgs = append(where, pickable), slices.Concat(whereArgs, pickableArgs)
	}
	query, args := selectSQL(viewer)
	query += ` WHERE ` + strings.Join(where, " AND ")

	var rows []row
	if err := sqlx.SelectContext(ctx, q, &rows, query, append(args, whereArgs...)...); err != nil {
		return nil, fmt.Errorf("list occasions: %w", err)
	}
	now := s.now()
	all := make([]Occasion, 0, len(rows))
	for _, r := range rows {
		o := r.occasion(now, s.fallback, viewer)
		if f.NextDateBefore != nil && (o.NextDate == nil || f.NextDateBefore.Before(*o.NextDate)) {
			continue
		}
		if f.ListID != nil && o.NextDate == nil {
			continue
		}
		all = append(all, o)
	}
	return all, nil
}

// Pickable returns the occasions that a giver can pick for a gift on the list,
// soonest first, then by name (docs/spec.md, Gifts, Occasions for a gift).
// The first is the default. It is `GET /occasions?list_id=` without the paging.
// q is a database or a transaction.
func (s *Service) Pickable(ctx context.Context, q sqlx.QueryerContext, viewer user.User, listID int64) ([]Occasion, error) {
	all, err := s.load(ctx, q, viewer, Filter{ListID: &listID})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b Occasion) int {
		return compareSort(page.Request{Key: "next_date"}, sortKey(a, "next_date"), a.ID, sortKey(b, "next_date"), b.ID)
	})
	return all, nil
}

// UpcomingFor returns the occasions that apply to the users with these IDs and
// have a next date, soonest first, then by name, with the next dates as the viewer
// sees them. It checks nothing about what the viewer can see: it picks the
// occasion of a gift whose giver can see none of the recipients. q is a database
// or a transaction.
func (s *Service) UpcomingFor(ctx context.Context, q sqlx.QueryerContext, viewer user.User, userIDs []int64) ([]Occasion, error) {
	applies, appliesArgs := AppliesToAnyOf(userIDs)
	query, args := selectSQL(viewer)
	var rows []row
	if err := sqlx.SelectContext(ctx, q, &rows, query+` WHERE `+applies, append(args, appliesArgs...)...); err != nil {
		return nil, fmt.Errorf("upcoming occasions: %w", err)
	}
	now := s.now()
	var all []Occasion
	for _, r := range rows {
		if o := r.occasion(now, s.fallback, viewer); o.NextDate != nil {
			all = append(all, o)
		}
	}
	slices.SortFunc(all, func(a, b Occasion) int {
		return compareSort(page.Request{Key: "next_date"}, sortKey(a, "next_date"), a.ID, sortKey(b, "next_date"), b.ID)
	})
	return all, nil
}

// PickableIDs returns the IDs of the occasions that apply to a recipient of the
// list whom the viewer may give to ([PickableFor]) and that the viewer can see,
// whether or not they have a next date. A gift shows its occasion to its giver
// only while the occasion is one of these. q is a database or a transaction.
func PickableIDs(ctx context.Context, q sqlx.QueryerContext, viewer user.User, listID int64) (map[int64]bool, error) {
	seen, seenArgs := VisibleTo(viewer)
	pickable, pickableArgs := PickableFor(viewer, listID)
	var ids []int64
	if err := sqlx.SelectContext(ctx, q, &ids, `SELECT occasions.id FROM occasions WHERE `+seen+` AND `+pickable, slices.Concat(seenArgs, pickableArgs)...); err != nil {
		return nil, fmt.Errorf("pickable occasions: %w", err)
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// Visible returns one page of the occasions that the viewer can see and that
// match the filter, and how many there are in all. It returns the next cursor,
// or nil on the last page. A next date follows the clock and the zone of each
// occasion's user, so it is not a column: the collection is sorted here.
func (s *Service) Visible(ctx context.Context, viewer user.User, f Filter, req page.Request) (occasions []Occasion, total int, next *string, err error) {
	all, err := s.load(ctx, s.db, viewer, f)
	if err != nil {
		return nil, 0, nil, err
	}
	total = len(all)

	slices.SortFunc(all, func(a, b Occasion) int {
		return compareSort(req, sortKey(a, req.Key), a.ID, sortKey(b, req.Key), b.ID)
	})
	if req.After != nil {
		after := req.After
		start := slices.IndexFunc(all, func(o Occasion) bool {
			return compareSort(req, sortKey(o, req.Key), o.ID, after.Value, after.ID) > 0
		})
		if start < 0 {
			start = len(all)
		}
		all = all[start:]
	}
	all = all[:min(len(all), req.Limit+1)]
	occasions, next = page.Slice(req, all, func(o Occasion) page.Position {
		return page.Position{Value: sortKey(o, req.Key), ID: o.ID}
	})
	return occasions, total, next, nil
}

// getVisible loads an occasion that the viewer can see. q is a database or a
// transaction.
func (s *Service) getVisible(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (Occasion, error) {
	seen, seenArgs := VisibleTo(viewer)
	query, args := selectSQL(viewer)
	var r row
	err := sqlx.GetContext(ctx, q, &r, query+` WHERE occasions.id = ? AND `+seen, append(append(args, id), seenArgs...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Occasion{}, ErrNotFound
	}
	if err != nil {
		return Occasion{}, fmt.Errorf("get occasion: %w", err)
	}
	return r.occasion(s.now(), s.fallback, viewer), nil
}

// GetVisible loads an occasion that the viewer can see. It returns
// [ErrNotFound] when there is no such occasion, and when the viewer can't see
// it: the two look the same.
func (s *Service) GetVisible(ctx context.Context, viewer user.User, id int64) (Occasion, error) {
	return s.getVisible(ctx, s.db, viewer, id)
}

// ruleColumns are a rule's stored values: kind, year, month, day, weekday, nth.
// Columns a kind doesn't use are NULL.
func ruleColumns(r Rule) (kind string, year, month, day, weekday, nth any) {
	month = int(r.Month)
	switch r.Kind {
	case KindAnnual:
		kind, day = "annual", r.Day
	case KindOnce:
		kind, year, day = "once", r.Year, r.Day
	case KindNthWeekday:
		kind, weekday, nth = "nth_weekday", int(r.Weekday), r.N
	case KindLastWeekday:
		kind, weekday = "last_weekday", int(r.Weekday)
	}
	return
}

// New is the input for [Service.Create].
type New struct {
	Name string
	Rule Rule
	// UserID is the user the occasion is for: the viewer (the default when nil)
	// or one of their children.
	UserID *int64
}

// Create makes a custom occasion for the viewer or for one of their children
// (docs/spec.md, Occasions, Occasions of a user). It returns the occasion as
// the viewer sees it. It returns [ErrInvalid] for a bad name or rule,
// [ErrUserNotFound] when the viewer can't see the user, [ErrUserNotAllowed] when
// they see the user and are neither that user nor a parent, and
// [ErrUserDeactivated] for a deactivated user.
func (s *Service) Create(ctx context.Context, viewer user.User, n New) (Occasion, error) {
	name, err := validName(n.Name)
	if err != nil {
		return Occasion{}, err
	}
	if err := validRule(n.Rule); err != nil {
		return Occasion{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Occasion{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id := viewer.ID
	if n.UserID != nil {
		id = *n.UserID
	}
	if id != viewer.ID {
		seen, err := user.CanSee(ctx, tx, viewer, id)
		if err != nil {
			return Occasion{}, err
		}
		if !seen {
			return Occasion{}, ErrUserNotFound
		}
	}
	// The user is loaded inside the transaction, with their parents, so a link or
	// a deactivation that commits first is seen here.
	owner, err := user.Get(ctx, tx, id)
	if errors.Is(err, user.ErrNotFound) {
		return Occasion{}, ErrUserNotFound
	}
	if err != nil {
		return Occasion{}, err
	}
	switch {
	case !Manages(viewer, owner):
		return Occasion{}, ErrUserNotAllowed
	case !owner.Active():
		return Occasion{}, ErrUserDeactivated
	}
	kind, year, month, day, weekday, nth := ruleColumns(n.Rule)
	res, err := tx.ExecContext(ctx, `INSERT INTO occasions
		(kind, name, user_id, rule_kind, rule_year, rule_month, rule_day, rule_weekday, rule_nth, created_at)
		VALUES ('custom', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, id, kind, year, month, day, weekday, nth, s.now())
	if err != nil {
		return Occasion{}, fmt.Errorf("insert occasion: %w", err)
	}
	oid, err := res.LastInsertId()
	if err != nil {
		return Occasion{}, fmt.Errorf("insert occasion: %w", err)
	}
	created, err := s.getVisible(ctx, tx, viewer, oid)
	if err != nil {
		return Occasion{}, err
	}
	if err := tx.Commit(); err != nil {
		return Occasion{}, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

// Guard runs inside the transaction of [Service.Update] and [Service.Delete],
// before the write, with the occasion as the viewer sees it. q is the
// transaction. An error from it stops the write.
type Guard func(ctx context.Context, q sqlx.ExtContext, current Occasion) error

// EditError says why the viewer can't change the occasion, or nil when they can
// ([Occasion.CanEdit]): [ErrBirthday] for a birthday, [ErrNotEditable] when the
// viewer is not the admin (a shared occasion) or neither the user nor their
// parent (a custom one), and [ErrUserDeactivated]. A route asks it before it
// reads the body, and the writes ask it again inside their transaction.
func (o Occasion) EditError() error {
	switch {
	case o.Type == TypeBirthday:
		return ErrBirthday
	case o.Type == TypeShared && !o.Admin:
		return ErrNotEditable
	case o.Type == TypeShared:
		return nil
	case !o.Manages:
		return ErrNotEditable
	case !o.UserActive:
		return ErrUserDeactivated
	}
	return nil
}

// editable loads an occasion that the viewer must be able to change, and
// returns the error that says why they can't.
func (s *Service) editable(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (Occasion, error) {
	current, err := s.getVisible(ctx, q, viewer, id)
	if err != nil {
		return Occasion{}, err
	}
	return current, current.EditError()
}

// Update applies a merge patch to an occasion that the viewer can change (a
// custom occasion, or a shared one for the admin), and returns the occasion as it is after. It returns [ErrNotFound] for an
// occasion the viewer can't see, [ErrBirthday], [ErrNotEditable] for one they see
// and can't change, [ErrUserDeactivated], and [ErrInvalid]. guard runs inside
// the same transaction as the write: an If-Match check goes there.
func (s *Service) Update(ctx context.Context, viewer user.User, id int64, c Changes, guard Guard) (Occasion, error) {
	c, err := c.validate()
	if err != nil {
		return Occasion{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Occasion{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.editable(ctx, tx, viewer, id)
	if err != nil {
		return Occasion{}, err
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return Occasion{}, err
		}
	}
	if c.Name.Set {
		if _, err := tx.ExecContext(ctx, `UPDATE occasions SET name = ? WHERE id = ?`, *c.Name.Value, id); err != nil {
			return Occasion{}, fmt.Errorf("update occasion: %w", err)
		}
	}
	if c.Rule.Set {
		kind, year, month, day, weekday, nth := ruleColumns(*c.Rule.Value)
		if _, err := tx.ExecContext(ctx, `UPDATE occasions SET rule_kind = ?, rule_year = ?, rule_month = ?, rule_day = ?,
			rule_weekday = ?, rule_nth = ? WHERE id = ?`, kind, year, month, day, weekday, nth, id); err != nil {
			return Occasion{}, fmt.Errorf("update occasion: %w", err)
		}
	}
	if c.IsEveryone.Set {
		if current.Type != TypeShared {
			return Occasion{}, invalid("is_everyone", "only a shared occasion can be marked everyone")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE occasions SET is_everyone = ? WHERE id = ?`, *c.IsEveryone.Value, id); err != nil {
			return Occasion{}, fmt.Errorf("update occasion: %w", err)
		}
		// Marking it everyone detaches its groups. Unmarking it leaves it with
		// none, because there are none to keep.
		if *c.IsEveryone.Value {
			if _, err := tx.ExecContext(ctx, `DELETE FROM occasion_groups WHERE occasion_id = ?`, id); err != nil {
				return Occasion{}, fmt.Errorf("detach groups: %w", err)
			}
		}
	}
	// An edit of the date rule moves the gifts for the occasion to its new date.
	// Unmarking a shared occasion everyone leaves it reaching nobody.
	if s.observer != nil && (c.Rule.Set || (c.IsEveryone.Set && !*c.IsEveryone.Value && current.IsEveryone)) {
		if err := s.observer.Changed(ctx, tx, id, c.Rule.Set); err != nil {
			return Occasion{}, err
		}
	}
	updated, err := s.getVisible(ctx, tx, viewer, id)
	if err != nil {
		return Occasion{}, err
	}
	if err := tx.Commit(); err != nil {
		return Occasion{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

// sharedForAdmin loads a shared occasion inside a transaction, for a write by
// the admin. It returns [ErrNotFound] for an occasion that doesn't exist and for
// one that isn't shared, and [ErrNotEditable] for a viewer who isn't the admin.
func (s *Service) sharedForAdmin(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (Occasion, error) {
	o, err := s.getVisible(ctx, q, viewer, id)
	if err != nil {
		return Occasion{}, err
	}
	if o.Type != TypeShared {
		return Occasion{}, ErrNotFound
	}
	if !o.Admin {
		return Occasion{}, ErrNotEditable
	}
	return o, nil
}

// AttachGroup attaches a group to a shared occasion. Only the admin does. It
// returns [ErrNotFound] for an occasion that is not a shared occasion the viewer
// can see, [ErrNotEditable] for a viewer who isn't the admin, [ErrIsEveryone]
// while the occasion is marked everyone, [ErrGroupNotFound] for a group that
// doesn't exist, and [ErrAlreadyAttached].
func (s *Service) AttachGroup(ctx context.Context, viewer user.User, id, groupID int64) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		o, err := s.sharedForAdmin(ctx, tx, viewer, id)
		if err != nil {
			return err
		}
		if o.IsEveryone {
			return ErrIsEveryone
		}
		var ok bool
		if err := tx.GetContext(ctx, &ok, `SELECT EXISTS (SELECT 1 FROM groups WHERE id = ?)`, groupID); err != nil {
			return fmt.Errorf("find group: %w", err)
		}
		if !ok {
			return ErrGroupNotFound
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO occasion_groups (occasion_id, group_id) VALUES (?, ?)`, id, groupID)
		if err != nil {
			return fmt.Errorf("attach group: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAlreadyAttached
		}
		return nil
	})
}

// DetachGroup takes a group off a shared occasion. Only the admin does. It
// returns [ErrNotFound] for an occasion that is not a shared occasion the viewer
// can see, [ErrNotEditable] for a viewer who isn't the admin, and
// [ErrNotAttached] for a group that is not attached.
func (s *Service) DetachGroup(ctx context.Context, viewer user.User, id, groupID int64) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		if _, err := s.sharedForAdmin(ctx, tx, viewer, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM occasion_groups WHERE occasion_id = ? AND group_id = ?`, id, groupID)
		if err != nil {
			return fmt.Errorf("detach group: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotAttached
		}
		if s.observer != nil {
			return s.observer.Changed(ctx, tx, id, false)
		}
		return nil
	})
}

// GroupsOf loads a shared occasion for the admin to read its groups. It returns
// the errors of [Service.AttachGroup] that don't need a group.
func (s *Service) GroupsOf(ctx context.Context, viewer user.User, id int64) (Occasion, error) {
	return s.sharedForAdmin(ctx, s.db, viewer, id)
}

func (s *Service) tx(ctx context.Context, fn func(tx *sqlx.Tx) error) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// UsersWhere returns a SQL condition on a row of the `users` table, true for the
// users that the occasion with the ID applies to, and its arguments: a birthday
// or a custom occasion applies to its user, a shared occasion to every user when
// it is marked everyone, and otherwise to the users in the groups it is attached
// to, with nesting. For an occasion the viewer can't see, or that doesn't exist,
// it is never true, so the filter doesn't show whether the occasion exists. The
// users collection still shows only the users the viewer can see.
func (s *Service) UsersWhere(ctx context.Context, viewer user.User, id int64) (string, []any, error) {
	o, err := s.GetVisible(ctx, viewer, id)
	if errors.Is(err, ErrNotFound) {
		return "1 = 0", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	where, args := appliesToWhere(o)
	return where, args, nil
}

// NewShared is the input for [Service.CreateShared].
type NewShared struct {
	Name       string
	Rule       Rule
	IsEveryone bool
}

// CreateShared makes a shared occasion, attached to no groups, and returns it as
// the admin sees it. Only the admin makes one: any other viewer gets
// [ErrNotEditable]. It returns [ErrInvalid] for a bad name or rule.
func (s *Service) CreateShared(ctx context.Context, viewer user.User, n NewShared) (Occasion, error) {
	if !viewer.IsAdmin {
		return Occasion{}, ErrNotEditable
	}
	name, err := validName(n.Name)
	if err != nil {
		return Occasion{}, err
	}
	if err := validRule(n.Rule); err != nil {
		return Occasion{}, err
	}
	kind, year, month, day, weekday, nth := ruleColumns(n.Rule)
	res, err := s.db.ExecContext(ctx, `INSERT INTO occasions
		(kind, name, user_id, is_everyone, rule_kind, rule_year, rule_month, rule_day, rule_weekday, rule_nth, created_at)
		VALUES ('shared', ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, n.IsEveryone, kind, year, month, day, weekday, nth, s.now())
	if err != nil {
		return Occasion{}, fmt.Errorf("insert shared occasion: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Occasion{}, fmt.Errorf("insert shared occasion: %w", err)
	}
	return s.GetVisible(ctx, viewer, id)
}

// Delete deletes an occasion that the viewer can change: a custom occasion, or
// any shared one for the admin. Its attached groups go with it. It returns the
// errors of [Service.Update] but [ErrInvalid].
func (s *Service) Delete(ctx context.Context, viewer user.User, id int64, guard Guard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.editable(ctx, tx, viewer, id)
	if err != nil {
		return err
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return err
		}
	}
	// The gifts for the occasion move to another before it goes. What is left
	// pointing at it is a given gift, which keeps the name it was given with.
	if s.observer != nil {
		if err := s.observer.Deleting(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM occasions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete occasion: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
