// Package group owns groups: named sets of users and other groups, to any
// depth (docs/spec.md, Visibility, Groups). Only the admin manages them, and
// a group has no owners. The package decides who is in a group and keeps the
// nesting free of loops. Who can see a user through a group is decided in
// internal/user, in one place (see user.CanSee).
//
// A group contains the users directly in it and the users directly in any group
// inside it. A user is in a group when it contains them. A user directly in an
// inner group is in the outer group too, and a user directly in the outer group
// is not in the inner one.
package group

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

	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/user"
)

// MaxNameLength is the longest group name, in characters.
const MaxNameLength = 100

var (
	// ErrNotFound means no group has the ID, or the viewer can't see it. The two
	// look the same.
	ErrNotFound = errors.New("no such group")
	// ErrInvalid means the input failed a rule. The message is safe to show.
	ErrInvalid = errors.New("invalid input")
	// ErrAlreadyIn means the user or the group is already directly in the group.
	ErrAlreadyIn = errors.New("already in the group")
	// ErrLoop means the nesting would make a group contain itself.
	ErrLoop = errors.New("a group can't end up inside itself")
	// ErrNotIn means the user or the group is not directly in the group.
	ErrNotIn = errors.New("not in the group")
	// ErrUserNotFound means a user to add does not exist.
	ErrUserNotFound = errors.New("no such user")
	// ErrUserDeactivated means a user to add to the group, or to take out of it,
	// is deactivated. Deactivation is a pause: it changes no group membership
	// (docs/spec.md, Who acts while a user is deactivated).
	ErrUserDeactivated = errors.New("the user is deactivated")
	// ErrGroupNotFound means a group to nest does not exist.
	ErrGroupNotFound = errors.New("no such group to nest")
)

// FieldError says which input field failed a rule. It matches [ErrInvalid].
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string        { return e.Message }
func (e *FieldError) Is(target error) bool { return target == ErrInvalid }

// Group is a group as the database holds it.
type Group struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// Entry is a group as one viewer sees it: with how they are in it.
type Entry struct {
	Group
	// Through is the group that the viewer is directly in, and that contains
	// this group by nesting. It is nil when the viewer is directly in this
	// group, or is not in it. When several of the viewer's groups put them in
	// this one, it is the first of them by name.
	Through *Group
}

// SortKeys are the sort keys of a collection of groups (docs/routes.md,
// Groups). The first is the default.
var SortKeys = []page.Key{{Name: "name"}}

// cleanName trims a name and checks it.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", &FieldError{Field: "name", Message: "the name is required"}
	case utf8.RuneCountInString(name) > MaxNameLength:
		return "", &FieldError{Field: "name", Message: fmt.Sprintf("the name must be at most %d characters", MaxNameLength)}
	}
	return name, nil
}

// Service reads and changes groups.
type Service struct {
	db       *sqlx.DB
	now      func() time.Time
	observer Observer
}

// Observer is told when a change to the groups can change who sees whom
// (docs/spec.md, Visibility, Groups), so the open gifts of the users affected
// follow the rules of Gifts, Changes while a gift is open. It runs inside the
// transaction of the change, so the change and what it does to the gifts commit
// together, and an error from it undoes the change. package gift implements it.
type Observer interface {
	// VisibilityChanged runs after the change, with the IDs of the users whose
	// visibility to each other may have changed: every user in a group that is, or
	// was, in the groups around the one that changed.
	VisibilityChanged(ctx context.Context, tx *sqlx.Tx, userIDs []int64) error
}

// Observe sets the observer of the service. Call it before the service is used.
func (s *Service) Observe(o Observer) { s.observer = o }

// usersAround returns the IDs of the users in the groups around a group: in the
// group itself or any group inside it, and in every group that contains it, with
// the groups inside those. Two users share a group exactly when some group
// contains both, so a change to the group can change what only these users see
// of each other. q is a database or a transaction.
func usersAround(ctx context.Context, q sqlx.QueryerContext, groupID int64) ([]int64, error) {
	var ids []int64
	err := sqlx.SelectContext(ctx, q, &ids, `WITH RECURSIVE
		up(id) AS (
			SELECT ? UNION SELECT gg.group_id FROM group_groups gg JOIN up ON gg.child_id = up.id
		),
		down(id) AS (
			SELECT id FROM up UNION SELECT gg.child_id FROM group_groups gg JOIN down ON gg.group_id = down.id
		)
		SELECT DISTINCT user_id FROM group_users WHERE group_id IN (SELECT id FROM down)`, groupID)
	if err != nil {
		return nil, fmt.Errorf("find the users around a group: %w", err)
	}
	return ids, nil
}

// changed runs the observer for a change to the groups. before is what
// [usersAround] gave before the change, for the groups that changed. It adds
// the same users after it, and the extra users.
func (s *Service) changed(ctx context.Context, tx *sqlx.Tx, before []int64, groupIDs []int64, extra ...int64) error {
	if s.observer == nil {
		return nil
	}
	ids := append(slices.Clone(before), extra...)
	for _, id := range groupIDs {
		after, err := usersAround(ctx, tx, id)
		if err != nil {
			return err
		}
		ids = append(ids, after...)
	}
	slices.Sort(ids)
	return s.observer.VisibilityChanged(ctx, tx, slices.Compact(ids))
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// row is a group with how the viewer is in it.
type row struct {
	ID          int64          `db:"id"`
	Name        string         `db:"name"`
	CreatedAt   time.Time      `db:"created_at"`
	ThroughID   sql.NullInt64  `db:"through_id"`
	ThroughName sql.NullString `db:"through_name"`
}

func (r row) entry() Entry {
	e := Entry{Group: Group{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt}}
	if r.ThroughID.Valid {
		e.Through = &Group{ID: r.ThroughID.Int64, Name: r.ThroughName.String}
	}
	return e
}

// viewerGroups is a WITH clause. It makes `up` the set of groups that contain
// the viewer, and `via` the group the viewer is directly in that puts them in
// each (see [user.GroupsContaining], which is the one copy of this rule). It
// takes the viewer's ID once.
const viewerGroups = `WITH RECURSIVE ` + user.GroupsContaining

// entrySQL selects groups with how the viewer is in each. It takes the viewer's
// ID twice and is followed by a WHERE clause on g.
const entrySQL = viewerGroups + `
SELECT g.id, g.name, g.created_at,
	(SELECT v.id FROM up JOIN groups v ON v.id = up.via
		WHERE up.id = g.id AND NOT EXISTS (SELECT 1 FROM group_users gu WHERE gu.group_id = g.id AND gu.user_id = ?)
		ORDER BY v.name COLLATE NOCASE, v.id LIMIT 1) AS through_id,
	(SELECT v.name FROM up JOIN groups v ON v.id = up.via
		WHERE up.id = g.id AND NOT EXISTS (SELECT 1 FROM group_users gu WHERE gu.group_id = g.id AND gu.user_id = ?)
		ORDER BY v.name COLLATE NOCASE, v.id LIMIT 1) AS through_name
FROM groups g`

// seenBy returns the condition that a group g is one the viewer can see, and its
// arguments. The rule is [user.GroupSeenBy]: the admin sees every group, and
// anyone else sees the groups that contain them.
func seenBy(viewer user.User) (string, []any) {
	return user.GroupSeenBy(viewer, "g.id")
}

// entryArgs are the arguments of [entrySQL]: the viewer's ID for `up`, and for
// the two subqueries.
func entryArgs(viewer user.User) []any { return []any{viewer.ID, viewer.ID, viewer.ID} }

// Get loads a group as the viewer sees it. It returns [ErrNotFound] when there
// is none and when the viewer can't see it.
func (s *Service) Get(ctx context.Context, viewer user.User, id int64) (Entry, error) {
	return get(ctx, s.db, viewer, id)
}

func get(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (Entry, error) {
	var r row
	seen, seenArgs := seenBy(viewer)
	args := append(append(entryArgs(viewer), id), seenArgs...)
	err := sqlx.GetContext(ctx, q, &r, entrySQL+` WHERE g.id = ? AND `+seen, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get group: %w", err)
	}
	return r.entry(), nil
}

// List returns one page of the groups the viewer can see, and how many they
// can see in all: the groups that contain them, and for the admin every group.
// With includesMe, the admin gets only the groups that contain them, as anyone
// else does.
func (s *Service) List(ctx context.Context, viewer user.User, includesMe bool, req page.Request) (groups []Entry, total int, next *string, err error) {
	where := "1 = 1"
	if !viewer.IsAdmin || includesMe {
		where = "g.id IN (SELECT id FROM up)"
	}
	return s.collect(ctx, viewer, req, where, nil)
}

// Inside returns one page of the groups directly inside a group, as the viewer
// sees them. The group is not checked: the caller must have shown that the
// viewer may see it.
func (s *Service) Inside(ctx context.Context, viewer user.User, id int64, req page.Request) (groups []Entry, total int, next *string, err error) {
	return s.collect(ctx, viewer, req, "g.id IN (SELECT child_id FROM group_groups WHERE group_id = ?)", []any{id})
}

// AttachedTo returns one page of the groups attached to a shared occasion, as
// the viewer sees them. The occasion is not checked: the caller must have shown
// that the viewer may read its groups (only the admin does).
func (s *Service) AttachedTo(ctx context.Context, viewer user.User, occasionID int64, req page.Request) (groups []Entry, total int, next *string, err error) {
	return s.collect(ctx, viewer, req, "g.id IN (SELECT group_id FROM occasion_groups WHERE occasion_id = ?)", []any{occasionID})
}

// collect runs the keyset query of a page of groups. where selects the groups,
// and may use `up` (see [viewerGroups]).
func (s *Service) collect(ctx context.Context, viewer user.User, req page.Request, where string, whereArgs []any) (groups []Entry, total int, next *string, err error) {
	if req.Key != "name" {
		return nil, 0, nil, fmt.Errorf("list groups: no sort key %q", req.Key)
	}
	seen, seenArgs := seenBy(viewer)
	where = "(" + where + ") AND " + seen
	whereArgs = append(append([]any{}, whereArgs...), seenArgs...)
	// The WITH clause comes first, so the viewer's ID is the first argument.
	base := append(entryArgs(viewer), whereArgs...)

	if err := s.db.GetContext(ctx, &total, viewerGroups+` SELECT count(*) FROM groups g WHERE `+where, append([]any{viewer.ID}, whereArgs...)...); err != nil {
		return nil, 0, nil, fmt.Errorf("count groups: %w", err)
	}
	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query := entrySQL + ` WHERE ` + where
	args := base
	if req.After != nil {
		query += ` AND (g.name COLLATE NOCASE ` + cmp + ` ? OR (g.name COLLATE NOCASE = ? AND g.id ` + cmp + ` ?))`
		args = append(args, req.After.Value, req.After.Value, req.After.ID)
	}
	query += ` ORDER BY g.name COLLATE NOCASE ` + dir + `, g.id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []row
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list groups: %w", err)
	}
	all := make([]Entry, len(rows))
	for i, r := range rows {
		all[i] = r.entry()
	}
	groups, next = page.Slice(req, all, func(e Entry) page.Position {
		return page.Position{Value: e.Name, ID: e.ID}
	})
	return groups, total, next, nil
}

// Create makes a group. It returns [ErrInvalid] for a name that fails a rule.
func (s *Service) Create(ctx context.Context, name string) (Entry, error) {
	name, err := cleanName(name)
	if err != nil {
		return Entry{}, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO groups (name, created_at) VALUES (?, ?)`, name, s.now())
	if err != nil {
		return Entry{}, fmt.Errorf("insert group: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Entry{}, err
	}
	var created time.Time
	if err := s.db.GetContext(ctx, &created, `SELECT created_at FROM groups WHERE id = ?`, id); err != nil {
		return Entry{}, fmt.Errorf("get group: %w", err)
	}
	// Nobody is in a new group, so it has no `through`.
	return Entry{Group: Group{ID: id, Name: name, CreatedAt: created}}, nil
}

// Guard runs inside the transaction of a change, before the write, with the
// group as the viewer sees it before. An error from it stops the change.
type Guard func(ctx context.Context, current Entry) error

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

// Rename changes a group's name and returns the group as the viewer sees it
// after. guard, if there is one, runs in the same transaction (an If-Match
// check goes there).
func (s *Service) Rename(ctx context.Context, viewer user.User, id int64, name string, guard Guard) (Entry, error) {
	name, err := cleanName(name)
	if err != nil {
		return Entry{}, err
	}
	var out Entry
	err = s.tx(ctx, func(tx *sqlx.Tx) error {
		current, err := get(ctx, tx, viewer, id)
		if err != nil {
			return err
		}
		if guard != nil {
			if err := guard(ctx, current); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, name, id); err != nil {
			return fmt.Errorf("rename group: %w", err)
		}
		out, err = get(ctx, tx, viewer, id)
		return err
	})
	return out, err
}

// Delete removes a group. Its users stay, and so do the groups inside it: only
// the group, and its places in the nesting, go. guard runs in the same
// transaction.
func (s *Service) Delete(ctx context.Context, viewer user.User, id int64, guard Guard) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		current, err := get(ctx, tx, viewer, id)
		if err != nil {
			return err
		}
		if guard != nil {
			if err := guard(ctx, current); err != nil {
				return err
			}
		}
		before, err := usersAround(ctx, tx, id)
		if err != nil {
			return err
		}
		// The foreign keys cascade to the users and the nesting.
		if _, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete group: %w", err)
		}
		return s.changed(ctx, tx, before, nil)
	})
}

// exists says whether a query that selects one row returns a row.
func exists(ctx context.Context, q sqlx.QueryerContext, query string, args ...any) (bool, error) {
	var ok bool
	if err := sqlx.GetContext(ctx, q, &ok, "SELECT EXISTS ("+query+")", args...); err != nil {
		return false, err
	}
	return ok, nil
}

func groupExists(ctx context.Context, q sqlx.QueryerContext, id int64) error {
	ok, err := exists(ctx, q, `SELECT 1 FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("find group: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// AddUser puts a user directly in a group. It returns [ErrNotFound] for a
// group that doesn't exist, [ErrUserNotFound] for a user who doesn't,
// [ErrUserDeactivated] for a user who is deactivated, and [ErrAlreadyIn] for a
// user who is in the group directly. The admin calls it, and the admin sees every
// user.
func (s *Service) AddUser(ctx context.Context, groupID, userID int64) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		if err := groupExists(ctx, tx, groupID); err != nil {
			return err
		}
		if u, err := user.Get(ctx, tx, userID); errors.Is(err, user.ErrNotFound) {
			return ErrUserNotFound
		} else if err != nil {
			return err
		} else if !u.Active() {
			return ErrUserDeactivated
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO group_users (group_id, user_id) VALUES (?, ?)`, groupID, userID)
		if err != nil {
			return fmt.Errorf("add user to group: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAlreadyIn
		}
		return s.changed(ctx, tx, nil, []int64{groupID}, userID)
	})
}

// RemoveUser takes a user out of a group. It returns [ErrNotFound] for a group
// that doesn't exist, [ErrUserDeactivated] for a user who is deactivated, and
// [ErrNotIn] for a user who is not directly in it.
func (s *Service) RemoveUser(ctx context.Context, groupID, userID int64) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		if err := groupExists(ctx, tx, groupID); err != nil {
			return err
		}
		if u, err := user.Get(ctx, tx, userID); err == nil && !u.Active() {
			return ErrUserDeactivated
		} else if err != nil && !errors.Is(err, user.ErrNotFound) {
			return err
		}
		before, err := usersAround(ctx, tx, groupID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM group_users WHERE group_id = ? AND user_id = ?`, groupID, userID)
		if err != nil {
			return fmt.Errorf("remove user from group: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotIn
		}
		return s.changed(ctx, tx, before, []int64{groupID}, userID)
	})
}

// AddGroup puts a group directly inside another. It returns [ErrNotFound] for
// an outer group that doesn't exist, [ErrGroupNotFound] for an inner one that
// doesn't, [ErrAlreadyIn] for a group that is inside it directly, and
// [ErrLoop] when the outer group is the inner group or is inside it.
func (s *Service) AddGroup(ctx context.Context, outerID, innerID int64) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		if err := groupExists(ctx, tx, outerID); err != nil {
			return err
		}
		if err := groupExists(ctx, tx, innerID); errors.Is(err, ErrNotFound) {
			return ErrGroupNotFound
		} else if err != nil {
			return err
		}
		inside, err := exists(ctx, tx, `SELECT 1 FROM group_groups WHERE group_id = ? AND child_id = ?`, outerID, innerID)
		if err != nil {
			return fmt.Errorf("find nesting: %w", err)
		}
		if inside {
			return ErrAlreadyIn
		}
		// A loop forms when the outer group is the inner group, or is already
		// somewhere inside it.
		loop, err := exists(ctx, tx, `
			WITH RECURSIVE below(id) AS (
				SELECT ?
				UNION
				SELECT gg.child_id FROM group_groups gg JOIN below ON gg.group_id = below.id
			)
			SELECT 1 FROM below WHERE id = ?`, innerID, outerID)
		if err != nil {
			return fmt.Errorf("find loop: %w", err)
		}
		if loop {
			return ErrLoop
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_groups (group_id, child_id) VALUES (?, ?)`, outerID, innerID); err != nil {
			return fmt.Errorf("nest group: %w", err)
		}
		return s.changed(ctx, tx, nil, []int64{outerID, innerID})
	})
}

// RemoveGroup takes a group out of another. It returns [ErrNotFound] for an
// outer group that doesn't exist and [ErrNotIn] for an inner group that is not
// directly inside it.
func (s *Service) RemoveGroup(ctx context.Context, outerID, innerID int64) error {
	return s.tx(ctx, func(tx *sqlx.Tx) error {
		if err := groupExists(ctx, tx, outerID); err != nil {
			return err
		}
		before, err := usersAround(ctx, tx, innerID)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM group_groups WHERE group_id = ? AND child_id = ?`, outerID, innerID)
		if err != nil {
			return fmt.Errorf("remove group from group: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotIn
		}
		return s.changed(ctx, tx, before, []int64{outerID, innerID})
	})
}
