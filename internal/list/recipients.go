package list

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/user"
)

var (
	// ErrNotYours means the viewer sees the list and the recipient, and is
	// neither that recipient nor their parent, so they can't make them leave.
	ErrNotYours = errors.New("only the recipient or their parent can make them leave")
	// ErrLastRecipient means the user is the last recipient of the list. They
	// delete the list instead.
	ErrLastRecipient = errors.New("the last recipient can't leave")
)

// RecipientSortKeys are the sort keys of a list's recipients (docs/routes.md,
// List routes). There is one: when the user became a recipient.
var RecipientSortKeys = []page.Key{{Name: "created_at", Time: true}}

// Recipient is a recipient of a list as the viewer sees them: the recipient
// resource of docs/routes.md, List routes.
type Recipient struct {
	User user.Summary
	// CreatedAt is when the user became a recipient.
	CreatedAt time.Time
	// SeesListAfterLeaving is whether the user could still see the list after
	// they leave it ([SeesListAfterLeaving]). It is nil for a viewer who is
	// neither that user nor their parent: they don't get it.
	SeesListAfterLeaving *bool
	// CanDelete is whether the viewer can make the user leave ([List.CanRemove]).
	CanDelete bool
}

type recipientEntry struct {
	recipientRow
	CreatedAt time.Time `db:"created_at"`
	// Mine is whether the viewer is the user or their parent, which reads the
	// leave warning. Acts is whether they may make the user leave: Mine, and the
	// user is active.
	Mine bool `db:"mine"`
	Acts bool `db:"acts"`
}

// recipientsSQL selects the recipients of one list, as the viewer sees them. The
// first argument of the query is the viewer's side of "the user or their parent",
// so its arguments come first.
func recipientsSQL(viewer user.User) (string, []any) {
	who, args := UserOrTheirParent(viewer, "u.id")
	acts, actsArgs := UserOrTheirActiveParent(viewer, "u.id")
	return `SELECT r.list_id, u.id, u.name, u.avatar_image_id,
		(SELECT width FROM images WHERE images.id = u.avatar_image_id) AS avatar_width,
		(SELECT height FROM images WHERE images.id = u.avatar_image_id) AS avatar_height,
		r.created_at, ` + who + ` AS mine, ` + acts + ` AS acts
		FROM list_recipients r JOIN users u ON u.id = r.user_id`, append(args, actsArgs...)
}

func (e recipientEntry) summary() user.Summary {
	s := user.Summary{ID: e.ID, Name: e.Name}
	if e.AvatarImage.Valid {
		s.Avatar = &image.Summary{ID: e.AvatarImage.Int64, Width: int(e.AvatarWidth.Int64), Height: int(e.AvatarHeight.Int64)}
	}
	return s
}

// recipients builds the recipient resources for entries of the list l. The rule
// of leaving is [List.CanRemove]; a viewer who is the user or their parent also
// gets [SeesListAfterLeaving].
func recipients(ctx context.Context, q sqlx.QueryerContext, l List, entries []recipientEntry) ([]Recipient, error) {
	out := make([]Recipient, len(entries))
	for i, e := range entries {
		r := Recipient{User: e.summary(), CreatedAt: e.CreatedAt, CanDelete: l.CanRemove(e.Acts)}
		if e.Mine {
			sees, err := SeesListAfterLeaving(ctx, q, l.ID, e.ID)
			if err != nil {
				return nil, err
			}
			r.SeesListAfterLeaving = &sees
		}
		out[i] = r
	}
	return out, nil
}

// Recipients returns one page of the recipients of a list that the viewer can
// see, and how many there are in all. It returns [ErrNotFound] for a list the
// viewer can't see.
func (s *Service) Recipients(ctx context.Context, viewer user.User, listID int64, req page.Request) (rows []Recipient, total int, next *string, err error) {
	l, err := GetVisible(ctx, s.db, viewer, listID)
	if err != nil {
		return nil, 0, nil, err
	}
	total = len(l.Recipients)
	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query, args := recipientsSQL(viewer)
	query += ` WHERE r.list_id = ?`
	args = append(args, listID)
	if req.After != nil {
		t, err := time.Parse(time.RFC3339Nano, req.After.Value)
		if err != nil {
			return nil, 0, nil, page.ErrBadCursor
		}
		query += ` AND (r.created_at ` + cmp + ` ? OR (r.created_at = ? AND u.id ` + cmp + ` ?))`
		args = append(args, t, t, req.After.ID)
	}
	query += ` ORDER BY r.created_at ` + dir + `, u.id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)
	var entries []recipientEntry
	if err := s.db.SelectContext(ctx, &entries, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list recipients: %w", err)
	}
	all, err := recipients(ctx, s.db, l, entries)
	if err != nil {
		return nil, 0, nil, err
	}
	rows, next = page.Slice(req, all, func(r Recipient) page.Position {
		return page.Position{Value: page.FormatTime(r.CreatedAt), ID: r.User.ID}
	})
	return rows, total, next, nil
}

// RecipientGuard runs inside the transaction of [Service.Leave], before the
// write, with the recipient as the viewer sees them. An error from it stops the
// leave: an If-Match check goes there.
type RecipientGuard func(ctx context.Context, q sqlx.ExtContext, current Recipient) error

// Leave takes the user out of the recipients of the list, for the user or their
// parent (docs/spec.md, Lists, Leaving and deleting). It returns [ErrNotFound]
// for a list the viewer can't see and for a user who isn't a recipient,
// [ErrNotYours] for a viewer who is neither the user nor their parent,
// [ErrRecipientDeactivated] for a parent of a deactivated user, [ErrPrimaryList],
// and [ErrLastRecipient]. guard runs inside the same
// transaction as the write.
func (s *Service) Leave(ctx context.Context, viewer user.User, listID, userID int64, guard RecipientGuard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	l, err := GetVisible(ctx, tx, viewer, listID)
	if err != nil {
		return err
	}
	query, args := recipientsSQL(viewer)
	var entry recipientEntry
	err = tx.GetContext(ctx, &entry, query+` WHERE r.list_id = ? AND u.id = ?`, append(args, listID, userID)...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get recipient: %w", err)
	}
	switch {
	case !entry.Mine:
		return ErrNotYours
	case !entry.Acts:
		return ErrRecipientDeactivated
	}
	switch {
	case l.IsPrimary:
		return ErrPrimaryList
	case len(l.Recipients) < 2:
		return ErrLastRecipient
	}
	if guard != nil {
		current, err := recipients(ctx, tx, l, []recipientEntry{entry})
		if err != nil {
			return err
		}
		if err := guard(ctx, tx, current[0]); err != nil {
			return err
		}
	}
	if err := s.RemoveRecipient(ctx, tx, listID, userID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// RemoveRecipient takes the user out of the recipients of the list, in the
// transaction tx, and tells the observer, so the open gifts on the list are
// flagged and settled. It checks nothing: [Service.Leave] has checked the viewer's
// right, and the deletion of a user (internal/userdelete) has its own rule.
func (s *Service) RemoveRecipient(ctx context.Context, tx *sqlx.Tx, listID, userID int64) error {
	before, err := RecipientIDs(ctx, tx, listID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM list_recipients WHERE list_id = ? AND user_id = ?`, listID, userID); err != nil {
		return fmt.Errorf("leave list: %w", err)
	}
	if s.observer != nil {
		return s.observer.RecipientsChanged(ctx, tx, listID, before)
	}
	return nil
}

// RecipientsOf returns the recipients of the lists with the given IDs, by list
// ID, in the order they became recipients. A recipient is a summary: it needs no
// check that the viewer can see the user. q is a database or a transaction.
func RecipientsOf(ctx context.Context, q sqlx.QueryerContext, listIDs []int64) (map[int64][]user.Summary, error) {
	lists := make([]List, len(listIDs))
	for i, id := range listIDs {
		lists[i] = List{ID: id}
	}
	if err := loadRecipients(ctx, q, lists); err != nil {
		return nil, err
	}
	out := make(map[int64][]user.Summary, len(lists))
	for _, l := range lists {
		out[l.ID] = l.Recipients
	}
	return out, nil
}
