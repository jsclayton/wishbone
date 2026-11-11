package user

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/image"
)

// This file holds the parent and child links (docs/spec.md, Users and
// parents, Parents and children). The admin is the only one who changes them.
// Who can see whom through a link is a rule of [visibleTo], and what a parent
// may do for a child is a rule of internal/server.

var (
	// ErrParentLoop means a link would make a loop: a user would be their own
	// parent, or a parent of one of their own parents, to any depth.
	ErrParentLoop error = invalidError{"a user can't be their own parent, or a parent of one of their own parents"}
	// ErrAdminHasNoParents means a link would give the admin a parent.
	ErrAdminHasNoParents error = invalidError{"the admin can't have parents"}
	// ErrHasParents means a user who has parents can't become the admin.
	ErrHasParents error = invalidError{"a user who has parents can't be the admin"}
)

// ParentNotFoundError says that the parent at Index of the list of parent IDs
// doesn't exist. It matches [ErrNotFound]. Only the admin links, and the admin
// sees every user, so a user the admin can't see is a user who doesn't exist.
type ParentNotFoundError struct{ Index int }

func (e *ParentNotFoundError) Error() string        { return "there is no such user to link as a parent" }
func (e *ParentNotFoundError) Is(target error) bool { return target == ErrNotFound }

// Summary is a user as another resource holds them: a user's parents and
// children (docs/spec.md, Shapes). A summary doesn't need the viewer to see
// the user.
type Summary struct {
	ID     int64
	Name   string
	Avatar *image.Summary
}

// HasParent says whether the user with the ID is one of this user's parents.
func (u User) HasParent(id int64) bool {
	for _, p := range u.Parents {
		if p.ID == id {
			return true
		}
	}
	return false
}

// linkRow is a user at the other end of a link.
type linkRow struct {
	Of           int64         `db:"of"`
	ID           int64         `db:"id"`
	Name         string        `db:"name"`
	AvatarImage  sql.NullInt64 `db:"avatar_image_id"`
	AvatarWidth  sql.NullInt64 `db:"avatar_width"`
	AvatarHeight sql.NullInt64 `db:"avatar_height"`
}

func (r linkRow) summary() Summary {
	s := Summary{ID: r.ID, Name: r.Name}
	if r.AvatarImage.Valid {
		s.Avatar = &image.Summary{ID: r.AvatarImage.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
	}
	return s
}

// linksSQL selects the users at the other end of the links of some users. %[1]s
// is the column of the user the row is about, and %[2]s the column of the user
// at the other end.
const linksSQL = `SELECT l.%[1]s AS of, o.id, o.name, o.avatar_image_id,
	(SELECT width FROM images WHERE images.id = o.avatar_image_id) AS avatar_width,
	(SELECT height FROM images WHERE images.id = o.avatar_image_id) AS avatar_height
	FROM user_parents l JOIN users o ON o.id = l.%[2]s
	WHERE l.%[1]s IN (?) ORDER BY o.name COLLATE NOCASE, o.id`

// loadLinks fills in the parents and the children of the users, by name. Both
// are empty, not nil, for a user with none.
func loadLinks(ctx context.Context, q sqlx.QueryerContext, users []User) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]int64, len(users))
	at := make(map[int64]int, len(users))
	for i := range users {
		ids[i] = users[i].ID
		at[users[i].ID] = i
		users[i].Parents, users[i].Children = []Summary{}, []Summary{}
	}
	for _, side := range []struct {
		of, other string
		add       func(*User, Summary)
	}{
		{"child_id", "parent_id", func(u *User, s Summary) { u.Parents = append(u.Parents, s) }},
		{"parent_id", "child_id", func(u *User, s Summary) { u.Children = append(u.Children, s) }},
	} {
		query, args, err := sqlx.In(fmt.Sprintf(linksSQL, side.of, side.other), ids)
		if err != nil {
			return fmt.Errorf("load links: %w", err)
		}
		var rows []linkRow
		if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
			return fmt.Errorf("load links: %w", err)
		}
		for _, r := range rows {
			side.add(&users[at[r.Of]], r.summary())
		}
	}
	return nil
}

// setParents replaces the parents of a user with the users in parentIDs. It
// refuses a parent that doesn't exist ([ParentNotFoundError]), a parent for the
// admin ([ErrAdminHasNoParents]), and a loop ([ErrParentLoop]). A user listed
// twice is linked once. q is the transaction of the write.
func setParents(ctx context.Context, q sqlx.ExtContext, child User, parentIDs []int64) error {
	seen := make(map[int64]bool, len(parentIDs))
	unique := make([]int64, 0, len(parentIDs))
	for i, id := range parentIDs {
		var exists bool
		if err := sqlx.GetContext(ctx, q, &exists, `SELECT EXISTS (SELECT 1 FROM users WHERE id = ?)`, id); err != nil {
			return fmt.Errorf("check parent: %w", err)
		}
		if !exists {
			return &ParentNotFoundError{Index: i}
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) > 0 && child.IsAdmin {
		return ErrAdminHasNoParents
	}
	if len(unique) > 0 {
		// The child and everyone below them can't be one of the new parents.
		query, args, err := sqlx.In(`WITH RECURSIVE below(id) AS (
				SELECT ?
				UNION
				SELECT l.child_id FROM user_parents l JOIN below ON l.parent_id = below.id
			) SELECT EXISTS (SELECT 1 FROM below WHERE id IN (?))`, child.ID, unique)
		if err != nil {
			return fmt.Errorf("check loop: %w", err)
		}
		var loop bool
		if err := sqlx.GetContext(ctx, q, &loop, query, args...); err != nil {
			return fmt.Errorf("check loop: %w", err)
		}
		if loop {
			return ErrParentLoop
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM user_parents WHERE child_id = ?`, child.ID); err != nil {
		return fmt.Errorf("unlink parents: %w", err)
	}
	for _, id := range unique {
		if _, err := q.ExecContext(ctx, `INSERT INTO user_parents (child_id, parent_id) VALUES (?, ?)`, child.ID, id); err != nil {
			return fmt.Errorf("link parent: %w", err)
		}
	}
	return nil
}
