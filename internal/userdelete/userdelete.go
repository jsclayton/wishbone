// Package userdelete permanently deletes a user, for `wishbone delete-user`
// (docs/spec.md, Deactivation and deletion, Deletion). It is the one place that
// knows the whole rule, because a delete touches lists, items, gifts, images and
// flags, and those packages can't call each other the way it needs: the user
// package is the lowest, and the list package can't delete the items of a list.
//
// A delete runs in one transaction, so it happens whole or not at all. The order
// of its steps matters; see [Service.Delete].
package userdelete

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/user"
)

var (
	// ErrAdmin means the user is the admin. The operator moves the role with
	// `wishbone set-admin` first, so the instance always has an admin.
	ErrAdmin = errors.New("the admin can't be deleted")
	// ErrNotFound means no user has the ID. It is [user.ErrNotFound].
	ErrNotFound = user.ErrNotFound
)

// Service deletes users. lists, items and gifts must be the services that the
// server uses, with the gift observer registered ([gift.Service.Watch]), so a
// change to a list's recipients flags and settles the open gifts on it.
type Service struct {
	db    *sqlx.DB
	lists *list.Service
	items *item.Service
	gifts *gift.Service
	now   func() time.Time
}

// New returns a Service. now is the clock, so tests control time.
func New(db *sqlx.DB, lists *list.Service, items *item.Service, gifts *gift.Service, now func() time.Time) *Service {
	return &Service{db: db, lists: lists, items: items, gifts: gifts, now: func() time.Time { return now().UTC() }}
}

// Result says what a delete left that the operator may want to fix.
type Result struct {
	// User is the user as they were.
	User user.User
	// Orphans are the children who have no parent after the delete: the deleted
	// user was their only parent. A child with no parent is a valid state, and
	// nobody edits their lists until the admin links a new parent.
	Orphans []user.User
}

// Delete deletes the user for good. It returns [ErrNotFound] for a user that
// doesn't exist and [ErrAdmin] for the admin.
func (s *Service) Delete(ctx context.Context, id int64) (Result, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	u, err := user.Get(ctx, tx, id)
	if err != nil {
		return Result{}, err
	}
	if u.IsAdmin {
		return Result{}, ErrAdmin
	}
	orphans, err := orphansOf(ctx, tx, id)
	if err != nil {
		return Result{}, err
	}
	if err := s.forget(ctx, tx, u); err != nil {
		return Result{}, err
	}
	// Last: the rows that point at the user go with it (sessions, login links,
	// passkeys, parent links, group memberships, birthday and custom occasions,
	// invitations to them) or lose the reference (items.added_by,
	// list_invitations.invited_by, gifts.giver_id, images.uploaded_by).
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return Result{}, fmt.Errorf("delete user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{User: u, Orphans: orphans}, nil
}

// forget does everything that the delete of the users row can't do by itself, in
// the order that keeps the open gifts right:
//
//  1. The gifts they gave keep their name, and their open gifts are released.
//  2. The given gifts that only they received are deleted. The ones they shared
//     with others stay for the others (their row in gift_recipients goes with them).
//  3. For each list they are a recipient of: they leave a list that has another
//     recipient, and the observer flags the open gifts there and settles them. A
//     list where they are the only recipient is deleted, and so are its open
//     gifts. This is before the gifts on a kept list, because a delete of a list
//     keeps the list's shell for the given gifts that stay.
//  4. The lists deleted earlier with their gifts kept forget them as a last
//     recipient, and the gifts there are deleted or settled.
//  5. A gift that is still for one of their occasions is settled, while they are
//     no longer a recipient anywhere, so it moves.
//  6. The flags that name them drop them. This comes after the leaves above, which
//     raise flags that name them as an old recipient.
//  7. Their avatar and the images that only they could see are detached.
func (s *Service) forget(ctx context.Context, tx *sqlx.Tx, u user.User) error {
	if err := s.gifts.KeepGiverName(ctx, tx, u.ID, u.Name); err != nil {
		return err
	}
	if err := s.gifts.ReleaseOf(ctx, tx, u.ID); err != nil {
		return err
	}
	if err := s.gifts.DeleteGivenOnlyTo(ctx, tx, u.ID); err != nil {
		return err
	}
	var listIDs []int64
	if err := tx.SelectContext(ctx, &listIDs, `SELECT list_id FROM list_recipients WHERE user_id = ? ORDER BY list_id`, u.ID); err != nil {
		return fmt.Errorf("find the lists of a user: %w", err)
	}
	for _, listID := range listIDs {
		recipients, err := list.RecipientIDs(ctx, tx, listID)
		if err != nil {
			return err
		}
		if len(recipients) > 1 {
			if err := s.lists.RemoveRecipient(ctx, tx, listID, u.ID); err != nil {
				return err
			}
			continue
		}
		if err := s.gifts.DeleteOpenOn(ctx, tx, listID); err != nil {
			return err
		}
		if err := s.items.RemoveAllOn(ctx, tx, listID); err != nil {
			return err
		}
		if err := s.lists.Remove(ctx, tx, listID); err != nil {
			return err
		}
	}
	// A primary list that is kept for its given gifts is nobody's primary list.
	// Without this, the delete of the user would delete the list with it.
	if _, err := tx.ExecContext(ctx, `UPDATE lists SET primary_user_id = NULL, name_follows_user = 0 WHERE primary_user_id = ?`, u.ID); err != nil {
		return fmt.Errorf("let go of a primary list: %w", err)
	}
	if err := s.gifts.ForgetKeptRecipient(ctx, tx, u.ID); err != nil {
		return err
	}
	if err := s.gifts.SettleOccasionsOf(ctx, tx, u.ID); err != nil {
		return err
	}
	if err := giftflag.ForgetUser(ctx, tx, u.ID); err != nil {
		return err
	}
	now := s.now()
	if u.Avatar != nil {
		if err := image.Detach(ctx, tx, now, u.Avatar.ID); err != nil {
			return err
		}
	}
	return image.DetachUnheldBy(ctx, tx, now, u.ID)
}

// orphansOf returns the children whose only parent is the user, in ID order.
func orphansOf(ctx context.Context, q sqlx.QueryerContext, id int64) ([]user.User, error) {
	var ids []int64
	if err := sqlx.SelectContext(ctx, q, &ids, `SELECT child_id FROM user_parents up WHERE parent_id = ?
		AND NOT EXISTS (SELECT 1 FROM user_parents other WHERE other.child_id = up.child_id AND other.parent_id <> ?)
		ORDER BY child_id`, id, id); err != nil {
		return nil, fmt.Errorf("find the children left with no parent: %w", err)
	}
	out := make([]user.User, 0, len(ids))
	for _, childID := range ids {
		c, err := user.Get(ctx, q, childID)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
