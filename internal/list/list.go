// Package list owns the lists table and a list's recipients: how to load, filter
// and change a list. The items on a list are in internal/item. Who can see and
// who can edit a list is in rules.go, and nowhere else.
//
// See the layout notes in package user.
package list

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

const (
	// MaxNameLength is the longest list name, in characters. It is a user's longest name.
	MaxNameLength = user.MaxNameLength
	// MaxDescriptionLength is the longest description, in characters.
	MaxDescriptionLength = 2000
)

var (
	// ErrNotFound means no list has the ID, or the viewer can't see it. The two
	// look the same.
	ErrNotFound = errors.New("no such list")
	// ErrNotEditable means the viewer sees the list but can't edit it. See
	// [NotEditable] for the parent of a deactivated recipient.
	ErrNotEditable = errors.New("you can't edit this list")
	// ErrInvalid means the input failed a rule. The message is safe to show.
	ErrInvalid = errors.New("invalid input")
	// ErrRecipientNotFound means the user a list is for doesn't exist, or the
	// viewer can't see them.
	ErrRecipientNotFound = errors.New("no such user to make the list for")
	// ErrRecipientNotAllowed means the viewer sees the user a list is for, and is
	// neither that user nor their parent.
	ErrRecipientNotAllowed = errors.New("you can only make a list for yourself or your child")
	// ErrRecipientDeactivated means a parent can't do this through a child who is
	// deactivated: make a list for them, edit one of their lists, or make them
	// leave one.
	ErrRecipientDeactivated = errors.New("the user is deactivated")
	// ErrPrimaryList means nobody deletes a primary list.
	ErrPrimaryList = errors.New("a primary list can't be deleted")
	// ErrHasOtherRecipients means a list with more than one recipient can't be
	// deleted: its recipients leave until one is left.
	ErrHasOtherRecipients = errors.New("the list has other recipients")
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

// List is a list as the viewer sees it.
type List struct {
	ID          int64
	Name        string
	Description *string
	IsPrimary   bool
	// Recipients are in the order they became recipients.
	Recipients []user.Summary
	// ItemCount is the number of items on the list that the viewer can see.
	ItemCount int
	CreatedAt time.Time
	// CanEdit is the viewer's power over the list, from [EditableBy].
	CanEdit bool
}

// Summary is the list as another resource holds it.
func (l List) Summary() user.ListSummary { return user.ListSummary{ID: l.ID, Name: l.Name} }

// SortKeys are the sort keys of the lists collection (docs/routes.md, List
// routes). The first is the default.
var SortKeys = []page.Key{{Name: "name"}, {Name: "created_at", Time: true}}

var sortColumn = map[string]string{
	"name":       "lists.name COLLATE NOCASE",
	"created_at": "lists.created_at",
}

// Filter narrows the lists collection. A nil or empty field is no filter.
type Filter struct {
	// RecipientIDs keeps the lists that have any of these users among their
	// recipients.
	RecipientIDs []int64
	CanEdit      *bool
	IsPrimary    *bool
}

// Changes is a merge patch to a list.
type Changes struct {
	Name        patch.Optional[string]
	Description patch.Optional[string]
}

// validate trims and checks the fields that are set. An empty description is
// the same as `null`.
func (c Changes) validate() (Changes, error) {
	if c.Name.Set {
		if c.Name.Value == nil {
			return c, invalid("name", "a list needs a name")
		}
		name := strings.TrimSpace(*c.Name.Value)
		switch {
		case name == "":
			return c, invalid("name", "a list needs a name")
		case utf8.RuneCountInString(name) > MaxNameLength:
			return c, invalid("name", "the name must be at most %d characters", MaxNameLength)
		}
		c.Name.Value = &name
	}
	if c.Description.Set && c.Description.Value != nil {
		text := strings.TrimSpace(*c.Description.Value)
		switch {
		case utf8.RuneCountInString(text) > MaxDescriptionLength:
			return c, invalid("description", "the description must be at most %d characters", MaxDescriptionLength)
		case text == "":
			c.Description.Value = nil
		default:
			c.Description.Value = &text
		}
	}
	return c, nil
}

type row struct {
	ID          int64          `db:"id"`
	Name        string         `db:"name"`
	Description sql.NullString `db:"description"`
	IsPrimary   bool           `db:"is_primary"`
	CreatedAt   time.Time      `db:"created_at"`
	ItemCount   int            `db:"item_count"`
	CanEdit     bool           `db:"can_edit"`
}

func (r row) list() List {
	l := List{ID: r.ID, Name: r.Name, IsPrimary: r.IsPrimary, CreatedAt: r.CreatedAt, ItemCount: r.ItemCount, CanEdit: r.CanEdit,
		Recipients: []user.Summary{}}
	if r.Description.Valid {
		l.Description = &r.Description.String
	}
	return l
}

// selectSQL selects a list as the viewer sees it, from the `lists` table. The
// first placeholders are the item count's rule and then the viewer's edit rule,
// so their arguments come first.
func selectSQL(viewer user.User) (string, []any) {
	edit, editArgs := EditableBy(viewer)
	shown, shownArgs := ItemShownTo(viewer)
	return `SELECT lists.id, lists.name, lists.description, lists.primary_user_id IS NOT NULL AS is_primary, lists.created_at,
		(SELECT count(*) FROM items WHERE items.list_id = lists.id AND ` + shown + `) AS item_count,
		(` + edit + `) AS can_edit
		FROM lists`, append(shownArgs, editArgs...)
}

// Service reads and changes lists.
type Service struct {
	db       *sqlx.DB
	now      func() time.Time
	observer Observer
}

// Observer is told when the recipients of a list change, so the open gifts on it
// follow the rules of docs/spec.md, Gifts, Who a gift is for. It runs inside
// the transaction of the change, so the change and what it does to the gifts
// commit together, and an error from it undoes the change. package gift
// implements it.
type Observer interface {
	// RecipientsChanged runs after a user joined or left the list. before holds
	// the IDs of the recipients before the change, in the order they joined.
	RecipientsChanged(ctx context.Context, tx *sqlx.Tx, listID int64, before []int64) error
}

// Observe sets the observer of the service. Call it before the service is used.
func (s *Service) Observe(o Observer) { s.observer = o }

// RecipientIDs returns the IDs of the recipients of the list, in the order they
// joined. q is a database or a transaction.
func RecipientIDs(ctx context.Context, q sqlx.QueryerContext, listID int64) ([]int64, error) {
	var ids []int64
	if err := sqlx.SelectContext(ctx, q, &ids, `SELECT user_id FROM list_recipients WHERE list_id = ? ORDER BY created_at, user_id`, listID); err != nil {
		return nil, fmt.Errorf("find the recipients of a list: %w", err)
	}
	return ids, nil
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// Visible returns one page of the lists that the viewer can see and that match
// the filter, and how many there are in all. It returns the next cursor, or nil
// on the last page.
func (s *Service) Visible(ctx context.Context, viewer user.User, f Filter, req page.Request) (lists []List, total int, next *string, err error) {
	seen, seenArgs := VisibleTo(viewer)
	where := []string{seen}
	whereArgs := seenArgs
	if len(f.RecipientIDs) > 0 {
		in, args, err := sqlx.In(`lists.id IN (SELECT list_id FROM list_recipients WHERE user_id IN (?))`, f.RecipientIDs)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("list lists: %w", err)
		}
		where, whereArgs = append(where, in), append(whereArgs, args...)
	}
	if f.CanEdit != nil {
		edit, args := EditableBy(viewer)
		if !*f.CanEdit {
			edit = "NOT (" + edit + ")"
		}
		where, whereArgs = append(where, edit), append(whereArgs, args...)
	}
	if f.IsPrimary != nil {
		cond := "lists.primary_user_id IS NULL"
		if *f.IsPrimary {
			cond = "lists.primary_user_id IS NOT NULL"
		}
		where = append(where, cond)
	}
	cond := strings.Join(where, " AND ")

	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM lists WHERE `+cond, whereArgs...); err != nil {
		return nil, 0, nil, fmt.Errorf("count lists: %w", err)
	}

	col, ok := sortColumn[req.Key]
	if !ok {
		return nil, 0, nil, fmt.Errorf("list lists: no sort key %q", req.Key)
	}
	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query, args := selectSQL(viewer)
	query += ` WHERE ` + cond
	args = append(args, whereArgs...)
	if req.After != nil {
		var after any = req.After.Value
		if req.Key == "created_at" {
			t, err := time.Parse(time.RFC3339Nano, req.After.Value)
			if err != nil {
				return nil, 0, nil, page.ErrBadCursor
			}
			after = t
		}
		query += ` AND (` + col + ` ` + cmp + ` ? OR (` + col + ` = ? AND lists.id ` + cmp + ` ?))`
		args = append(args, after, after, req.After.ID)
	}
	query += ` ORDER BY ` + col + ` ` + dir + `, lists.id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []row
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list lists: %w", err)
	}
	all := make([]List, len(rows))
	for i, r := range rows {
		all[i] = r.list()
	}
	if err := loadRecipients(ctx, s.db, all); err != nil {
		return nil, 0, nil, err
	}
	lists, next = page.Slice(req, all, func(l List) page.Position {
		if req.Key == "created_at" {
			return page.Position{Value: page.FormatTime(l.CreatedAt), ID: l.ID}
		}
		return page.Position{Value: l.Name, ID: l.ID}
	})
	return lists, total, next, nil
}

// GetVisible loads a list that the viewer can see. It returns [ErrNotFound]
// when there is no such list, and when the viewer can't see it. q is a database
// or a transaction.
func GetVisible(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (List, error) {
	seen, seenArgs := VisibleTo(viewer)
	query, args := selectSQL(viewer)
	var r row
	err := sqlx.GetContext(ctx, q, &r, query+` WHERE lists.id = ? AND `+seen, append(append(args, id), seenArgs...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return List{}, ErrNotFound
	}
	if err != nil {
		return List{}, fmt.Errorf("get list: %w", err)
	}
	one := []List{r.list()}
	if err := loadRecipients(ctx, q, one); err != nil {
		return List{}, err
	}
	return one[0], nil
}

// GetVisible loads a list that the viewer can see (see the package function).
func (s *Service) GetVisible(ctx context.Context, viewer user.User, id int64) (List, error) {
	return GetVisible(ctx, s.db, viewer, id)
}

type recipientRow struct {
	ListID       int64         `db:"list_id"`
	ID           int64         `db:"id"`
	Name         string        `db:"name"`
	AvatarImage  sql.NullInt64 `db:"avatar_image_id"`
	AvatarWidth  sql.NullInt64 `db:"avatar_width"`
	AvatarHeight sql.NullInt64 `db:"avatar_height"`
}

// loadRecipients fills in the recipients of the lists, in the order they became
// recipients. A recipient is a summary: it doesn't need the viewer to see them.
func loadRecipients(ctx context.Context, q sqlx.QueryerContext, lists []List) error {
	if len(lists) == 0 {
		return nil
	}
	ids := make([]int64, len(lists))
	at := make(map[int64]int, len(lists))
	for i := range lists {
		ids[i] = lists[i].ID
		at[lists[i].ID] = i
	}
	query, args, err := sqlx.In(`SELECT r.list_id, u.id, u.name, u.avatar_image_id,
		(SELECT width FROM images WHERE images.id = u.avatar_image_id) AS avatar_width,
		(SELECT height FROM images WHERE images.id = u.avatar_image_id) AS avatar_height
		FROM list_recipients r JOIN users u ON u.id = r.user_id
		WHERE r.list_id IN (?) ORDER BY r.created_at, u.id`, ids)
	if err != nil {
		return fmt.Errorf("load recipients: %w", err)
	}
	var rows []recipientRow
	if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
		return fmt.Errorf("load recipients: %w", err)
	}
	for _, r := range rows {
		s := user.Summary{ID: r.ID, Name: r.Name}
		if r.AvatarImage.Valid {
			s.Avatar = &image.Summary{ID: r.AvatarImage.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
		}
		l := &lists[at[r.ListID]]
		l.Recipients = append(l.Recipients, s)
	}
	return nil
}

// Guard runs inside the transaction of [Service.Update], before the write, with
// the list as the viewer sees it. q is the transaction. An error from it stops
// the update.
type Guard func(ctx context.Context, q sqlx.ExtContext, current List) error

// Update applies a merge patch to a list that the viewer can edit, and returns
// the list as it is after. It returns [ErrNotFound] for a list the viewer can't
// see, [ErrNotEditable] for one they see and can't edit, and [ErrInvalid]. guard
// runs inside the same transaction as the write: an If-Match check goes there.
//
// A name that differs from the current one ends the primary list's following of
// its user's name. Saving the name it already has doesn't.
func (s *Service) Update(ctx context.Context, viewer user.User, id int64, c Changes, guard Guard) (List, error) {
	c, err := c.validate()
	if err != nil {
		return List{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return List{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := GetVisible(ctx, tx, viewer, id)
	if err != nil {
		return List{}, err
	}
	if !current.CanEdit {
		return List{}, NotEditable(ctx, tx, viewer, id)
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return List{}, err
		}
	}
	var sets []string
	var args []any
	if c.Name.Set && *c.Name.Value != current.Name {
		sets = append(sets, "name = ?", "name_follows_user = 0")
		args = append(args, *c.Name.Value)
	}
	if c.Description.Set {
		sets = append(sets, "description = ?")
		args = append(args, c.Description.Value) // a nil pointer is NULL
	}
	if len(sets) > 0 {
		args = append(args, id)
		if _, err := tx.ExecContext(ctx, `UPDATE lists SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return List{}, fmt.Errorf("update list: %w", err)
		}
	}
	updated, err := GetVisible(ctx, tx, viewer, id)
	if err != nil {
		return List{}, err
	}
	if err := tx.Commit(); err != nil {
		return List{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

// SummaryReaches says whether the summary of the user reaches the viewer through
// a list (see the package function).
func (s *Service) SummaryReaches(ctx context.Context, viewer user.User, id int64) (bool, error) {
	return SummaryReaches(ctx, s.db, viewer, id)
}

// PrimaryListsVisibleTo says which of the users' primary lists the viewer can
// see (see the package function).
func (s *Service) PrimaryListsVisibleTo(ctx context.Context, viewer user.User, ownerIDs []int64) (map[int64]bool, error) {
	return PrimaryListsVisibleTo(ctx, s.db, viewer, ownerIDs)
}

// New is the input for [Service.Create].
type New struct {
	Name string
	// Description is markdown, or nil (or blank) for none.
	Description *string
	// RecipientID is the user the list is for: the viewer, or one of their
	// children. Nil is the viewer.
	RecipientID *int64
}

// Create makes an extra list for the viewer or for one of their children, who
// becomes its first recipient (docs/spec.md, Lists, Extra lists). A parent
// who makes a list for a child is not a recipient. It returns the list as the
// viewer sees it. It returns [ErrInvalid] for a bad name or description,
// [ErrRecipientNotFound] when the viewer can't see the recipient,
// [ErrRecipientNotAllowed] when they see the user and are neither that user nor a
// parent, and [ErrRecipientDeactivated] for a deactivated child.
func (s *Service) Create(ctx context.Context, viewer user.User, n New) (List, error) {
	c, err := Changes{
		Name:        patch.Optional[string]{Set: true, Value: &n.Name},
		Description: patch.Optional[string]{Set: n.Description != nil, Value: n.Description},
	}.validate()
	if err != nil {
		return List{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return List{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	recipient := viewer
	if n.RecipientID != nil && *n.RecipientID != viewer.ID {
		seen, err := user.CanSee(ctx, tx, viewer, *n.RecipientID)
		if err != nil {
			return List{}, err
		}
		if !seen {
			return List{}, ErrRecipientNotFound
		}
		if recipient, err = user.Get(ctx, tx, *n.RecipientID); err != nil {
			return List{}, err
		}
		if !recipient.HasParent(viewer.ID) {
			return List{}, ErrRecipientNotAllowed
		}
		if !recipient.Active() {
			return List{}, ErrRecipientDeactivated
		}
	}
	now := s.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO lists (name, description, created_at) VALUES (?, ?, ?)`, *c.Name.Value, c.Description.Value, now)
	if err != nil {
		return List{}, fmt.Errorf("insert list: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return List{}, fmt.Errorf("insert list: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (?, ?, ?)`, id, recipient.ID, now); err != nil {
		return List{}, fmt.Errorf("add the first recipient: %w", err)
	}
	created, err := GetVisible(ctx, tx, viewer, id)
	if err != nil {
		return List{}, err
	}
	if err := tx.Commit(); err != nil {
		return List{}, fmt.Errorf("commit: %w", err)
	}
	return created, nil
}

// ItemRemover deletes every item on a list, inside the transaction of
// [Service.Delete]. It is [item.Service.RemoveAllOn]: this package can't import
// internal/item, which imports this one.
type ItemRemover func(ctx context.Context, tx *sqlx.Tx, listID int64) error

// keepForGifts turns a list that is being deleted into the shell that its kept
// gifts need (docs/spec.md, Lists, Leaving and deleting): the row stays and is
// marked deleted, its recipients move to kept_list_recipients, and its pending
// invitations go, and so does its description, which no gift needs. With no recipient and no invitation, no rule that reads
// list_recipients or list_invitations shows it, and nothing else sees it.
func keepForGifts(ctx context.Context, tx *sqlx.Tx, id int64, now time.Time) error {
	for _, q := range []string{
		`INSERT INTO kept_list_recipients (list_id, user_id) SELECT list_id, user_id FROM list_recipients WHERE list_id = ?`,
		`DELETE FROM list_recipients WHERE list_id = ?`,
		`DELETE FROM list_invitations WHERE list_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("keep a deleted list for its gifts: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE lists SET deleted_at = ?, description = NULL WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("keep a deleted list for its gifts: %w", err)
	}
	return nil
}

// PurgeIfUnused deletes a list that was deleted while its items had gifts, once
// no item is left on it: the last gift went, so nothing needs the shell. It does
// nothing for a list that is live or still has items. The package of items calls
// it in the transaction that purges an item.
func PurgeIfUnused(ctx context.Context, tx *sqlx.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM lists WHERE id = ? AND deleted_at IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM items WHERE list_id = lists.id)`, id); err != nil {
		return fmt.Errorf("delete a kept list: %w", err)
	}
	return nil
}

// Delete deletes an extra list that has one recipient, and its items, for the
// recipient or their parent (docs/spec.md, Lists, Leaving and deleting). It
// returns [ErrNotFound] for a list the viewer can't see, [ErrNotEditable] for
// one they see and can't edit, [ErrPrimaryList], and [ErrHasOtherRecipients].
// It never refuses because of the list's gifts. guard runs inside the same
// transaction as the write: an If-Match check goes there.
func (s *Service) Delete(ctx context.Context, viewer user.User, id int64, guard Guard, removeItems ItemRemover) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := GetVisible(ctx, tx, viewer, id)
	if err != nil {
		return err
	}
	if !current.CanEdit {
		return NotEditable(ctx, tx, viewer, id)
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return err
		}
	}
	switch {
	case current.IsPrimary:
		return ErrPrimaryList
	case len(current.Recipients) != 1:
		return ErrHasOtherRecipients
	}
	if err := removeItems(ctx, tx, id); err != nil {
		return err
	}
	if err := s.Remove(ctx, tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Remove deletes a list whose items are already removed, in the transaction tx:
// the row goes, unless an item is left (it has gifts), and then the list is kept
// for them. It checks nothing and removes no item.
func (s *Service) Remove(ctx context.Context, tx *sqlx.Tx, id int64) error {
	var kept bool
	if err := tx.GetContext(ctx, &kept, `SELECT EXISTS (SELECT 1 FROM items WHERE list_id = ?)`, id); err != nil {
		return fmt.Errorf("find the items a list keeps: %w", err)
	}
	if kept {
		// An item with gifts stays, deleted softly, and its gifts are for the list's
		// last recipients at reveal. The list keeps its row for them, with no
		// recipients, so no rule shows it again.
		return keepForGifts(ctx, tx, id, s.now())
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lists WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete list: %w", err)
	}
	return nil
}
