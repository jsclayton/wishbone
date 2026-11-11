package list

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/user"
)

// This file is invitations to an extra list (docs/spec.md, Lists,
// Invitations). Only pending invitations exist: an accept or a decline deletes
// the row. Who sees one is [InvitationVisibleTo] in rules.go.

var (
	// ErrInvitationNotFound means no invitation has the ID, or the viewer can't
	// see it. The two look the same.
	ErrInvitationNotFound = errors.New("no such invitation")
	// ErrNotRecipient means the viewer sees the list and is not one of its
	// recipients, so they can't invite.
	ErrNotRecipient = errors.New("only a recipient can invite")
	// ErrNotInvitee means the viewer sees the invitation and is neither the
	// invited user nor their parent, so they can't accept it.
	ErrNotInvitee = errors.New("only the invited user or their parent can do this")
	// ErrInviteeNotFound means the user to invite doesn't exist, or the viewer
	// can't see them.
	ErrInviteeNotFound = errors.New("no such user to invite")
	// ErrInviteeNotAllowed means the viewer can see the user only through the
	// admin's access to every user.
	ErrInviteeNotAllowed = errors.New("you can only invite a user you see")
	// ErrInviteeDeactivated means the invited user is deactivated, so nobody can
	// invite them, accept for them, or decline for them.
	ErrInviteeDeactivated = errors.New("the invited user is deactivated")
	// ErrAlreadyInvited means the user already has a pending invitation to the
	// list.
	ErrAlreadyInvited = errors.New("already invited")
	// ErrAlreadyRecipient means the user is already a recipient of the list.
	ErrAlreadyRecipient = errors.New("already a recipient")
)

// InvitationSortKeys are the sort keys of the invitations collection
// (docs/routes.md, Invitations). There is one: when it was made.
var InvitationSortKeys = []page.Key{{Name: "created_at", Time: true}}

// Invitation is a pending invitation as the viewer sees it.
type Invitation struct {
	ID int64
	// List is the list the user is invited to. A summary doesn't make the list
	// visible.
	List user.ListSummary
	// User is the invited user.
	User user.Summary
	// InvitedBy is the recipient who invited them, or nil once that user is
	// deleted.
	InvitedBy *user.Summary
	CreatedAt time.Time
	// CanAccept and CanDelete are the viewer's powers ([invitationRow.canAccept]
	// and [invitationRow.canDelete]).
	CanAccept bool
	CanDelete bool
}

// invitationRow is a row of the invitations query, with the facts the viewer's
// powers follow.
type invitationRow struct {
	ID        int64     `db:"id"`
	ListID    int64     `db:"list_id"`
	ListName  string    `db:"list_name"`
	CreatedAt time.Time `db:"created_at"`

	UserID     int64         `db:"user_id"`
	UserName   string        `db:"user_name"`
	UserImage  sql.NullInt64 `db:"user_avatar"`
	UserWidth  sql.NullInt64 `db:"user_avatar_width"`
	UserHeight sql.NullInt64 `db:"user_avatar_height"`

	ByID     sql.NullInt64  `db:"by_id"`
	ByName   sql.NullString `db:"by_name"`
	ByImage  sql.NullInt64  `db:"by_avatar"`
	ByWidth  sql.NullInt64  `db:"by_avatar_width"`
	ByHeight sql.NullInt64  `db:"by_avatar_height"`

	// Active is whether the invited user is not deactivated. Mine is whether the
	// viewer is the invited user or their parent. Recipient is whether the viewer
	// is a recipient of the list.
	Active    bool `db:"user_active"`
	Mine      bool `db:"mine"`
	Recipient bool `db:"is_recipient"`
}

// canAccept: the invited user or their parent accepts, while the invited user is
// active (docs/routes.md, Invitations). It is the one rule of accepting.
func (r invitationRow) canAccept() bool { return r.Mine && r.Active }

// canDelete: a recipient of the list withdraws, and the invited user or their
// parent declines while the invited user is active. It is the one rule of
// deleting.
func (r invitationRow) canDelete() bool { return r.Recipient || (r.Mine && r.Active) }

func (r invitationRow) invitation() Invitation {
	inv := Invitation{
		ID:        r.ID,
		List:      user.ListSummary{ID: r.ListID, Name: r.ListName},
		User:      summaryOf(r.UserID, r.UserName, r.UserImage, r.UserWidth, r.UserHeight),
		CreatedAt: r.CreatedAt,
		CanAccept: r.canAccept(),
		CanDelete: r.canDelete(),
	}
	if r.ByID.Valid {
		by := summaryOf(r.ByID.Int64, r.ByName.String, r.ByImage, r.ByWidth, r.ByHeight)
		inv.InvitedBy = &by
	}
	return inv
}

func summaryOf(id int64, name string, imageID, width, height sql.NullInt64) user.Summary {
	s := user.Summary{ID: id, Name: name}
	if imageID.Valid {
		s.Avatar = &image.Summary{ID: imageID.Int64, Width: int(width.Int64), Height: int(height.Int64)}
	}
	return s
}

// invitationSQL selects an invitation as the viewer sees it, from the
// `list_invitations` table. The placeholders in the select list come first, so
// its arguments come first.
func invitationSQL(viewer user.User) (string, []any) {
	mine, args := UserOrTheirParent(viewer, "list_invitations.user_id")
	args = append(args, viewer.ID)
	return `SELECT list_invitations.id, list_invitations.list_id, lists.name AS list_name, list_invitations.created_at,
		u.id AS user_id, u.name AS user_name, u.avatar_image_id AS user_avatar,
		(SELECT width FROM images WHERE images.id = u.avatar_image_id) AS user_avatar_width,
		(SELECT height FROM images WHERE images.id = u.avatar_image_id) AS user_avatar_height,
		b.id AS by_id, b.name AS by_name, b.avatar_image_id AS by_avatar,
		(SELECT width FROM images WHERE images.id = b.avatar_image_id) AS by_avatar_width,
		(SELECT height FROM images WHERE images.id = b.avatar_image_id) AS by_avatar_height,
		u.deactivated_at IS NULL AS user_active,
		` + mine + ` AS mine,
		list_invitations.list_id IN (SELECT list_id FROM list_recipients WHERE user_id = ?) AS is_recipient
		FROM list_invitations
		JOIN lists ON lists.id = list_invitations.list_id
		JOIN users u ON u.id = list_invitations.user_id
		LEFT JOIN users b ON b.id = list_invitations.invited_by`, args
}

// getInvitation loads an invitation that the viewer can see. It returns
// [ErrInvitationNotFound] when there is none, and when the viewer can't see it.
func getInvitation(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (invitationRow, error) {
	seen, seenArgs := InvitationVisibleTo(viewer)
	query, args := invitationSQL(viewer)
	var r invitationRow
	err := sqlx.GetContext(ctx, q, &r, query+` WHERE list_invitations.id = ? AND `+seen, append(append(args, id), seenArgs...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return invitationRow{}, ErrInvitationNotFound
	}
	if err != nil {
		return invitationRow{}, fmt.Errorf("get invitation: %w", err)
	}
	return r, nil
}

// InvitationFilter narrows the invitations collection. An empty field is no
// filter, and a field with several IDs keeps the invitations that match any.
type InvitationFilter struct {
	ListIDs []int64
	UserIDs []int64
}

// Invitations returns one page of the invitations that the viewer can see and
// that match the filter, and how many there are in all.
func (s *Service) Invitations(ctx context.Context, viewer user.User, f InvitationFilter, req page.Request) (invitations []Invitation, total int, next *string, err error) {
	seen, seenArgs := InvitationVisibleTo(viewer)
	where := []string{seen}
	whereArgs := seenArgs
	for _, in := range []struct {
		ids []int64
		col string
	}{{f.ListIDs, "list_invitations.list_id"}, {f.UserIDs, "list_invitations.user_id"}} {
		if len(in.ids) == 0 {
			continue
		}
		cond, args, err := sqlx.In(in.col+` IN (?)`, in.ids)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("list invitations: %w", err)
		}
		where, whereArgs = append(where, cond), append(whereArgs, args...)
	}
	cond := strings.Join(where, " AND ")
	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM list_invitations WHERE `+cond, whereArgs...); err != nil {
		return nil, 0, nil, fmt.Errorf("count invitations: %w", err)
	}

	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query, args := invitationSQL(viewer)
	query += ` WHERE ` + cond
	args = append(args, whereArgs...)
	if req.After != nil {
		t, err := time.Parse(time.RFC3339Nano, req.After.Value)
		if err != nil {
			return nil, 0, nil, page.ErrBadCursor
		}
		query += ` AND (list_invitations.created_at ` + cmp + ` ? OR (list_invitations.created_at = ? AND list_invitations.id ` + cmp + ` ?))`
		args = append(args, t, t, req.After.ID)
	}
	query += ` ORDER BY list_invitations.created_at ` + dir + `, list_invitations.id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []invitationRow
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list invitations: %w", err)
	}
	all := make([]Invitation, len(rows))
	for i, r := range rows {
		all[i] = r.invitation()
	}
	invitations, next = page.Slice(req, all, func(i Invitation) page.Position {
		return page.Position{Value: page.FormatTime(i.CreatedAt), ID: i.ID}
	})
	return invitations, total, next, nil
}

// GetInvitation loads an invitation that the viewer can see. It returns
// [ErrInvitationNotFound] when there is none, and when the viewer can't see it.
func (s *Service) GetInvitation(ctx context.Context, viewer user.User, id int64) (Invitation, error) {
	r, err := getInvitation(ctx, s.db, viewer, id)
	if err != nil {
		return Invitation{}, err
	}
	return r.invitation(), nil
}

// InvitableWhere returns the SQL condition ([InvitableUsers]) that keeps the
// users whom the viewer can invite to the list, and its arguments, for the users
// collection. It is a condition that nothing satisfies when the viewer can't
// invite to the list: they can't see it, they aren't a recipient, or it is a
// primary list.
func (s *Service) InvitableWhere(ctx context.Context, viewer user.User, listID int64) (string, []any, error) {
	l, err := GetVisible(ctx, s.db, viewer, listID)
	if errors.Is(err, ErrNotFound) || (err == nil && !l.CanInvite(viewer)) {
		return "0 = 1", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	cond, args := InvitableUsers(viewer, listID)
	return cond, args, nil
}

// Invite invites a user to an extra list, for a recipient of the list. It
// returns [ErrNotFound] for a list the viewer can't see, [ErrNotRecipient],
// [ErrPrimaryList], [ErrInviteeNotFound] for a user the viewer can't see,
// [ErrInviteeNotAllowed] for a user they see only through the admin's access,
// [ErrAlreadyRecipient], [ErrAlreadyInvited], and [ErrInviteeDeactivated].
func (s *Service) Invite(ctx context.Context, viewer user.User, listID, userID int64) (Invitation, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Invitation{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	l, err := GetVisible(ctx, tx, viewer, listID)
	if err != nil {
		return Invitation{}, err
	}
	switch {
	case !l.IsRecipient(viewer):
		return Invitation{}, ErrNotRecipient
	case l.IsPrimary:
		return Invitation{}, ErrPrimaryList
	}
	// The reference rules of docs/routes.md: a user the caller can't see is
	// not found, and one they see only as the admin is not allowed.
	if seen, err := user.CanSee(ctx, tx, viewer, userID); err != nil {
		return Invitation{}, err
	} else if !seen {
		return Invitation{}, ErrInviteeNotFound
	}
	if seen, err := user.CanSeeWithoutAdminAccess(ctx, tx, viewer, userID); err != nil {
		return Invitation{}, err
	} else if !seen {
		return Invitation{}, ErrInviteeNotAllowed
	}
	for _, r := range l.Recipients {
		if r.ID == userID {
			return Invitation{}, ErrAlreadyRecipient
		}
	}
	var invited bool
	if err := tx.GetContext(ctx, &invited, `SELECT EXISTS (SELECT 1 FROM list_invitations WHERE list_id = ? AND user_id = ?)`, listID, userID); err != nil {
		return Invitation{}, fmt.Errorf("find invitation: %w", err)
	}
	if invited {
		return Invitation{}, ErrAlreadyInvited
	}
	invitee, err := user.Get(ctx, tx, userID)
	if err != nil {
		return Invitation{}, err
	}
	if !invitee.Active() {
		return Invitation{}, ErrInviteeDeactivated
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO list_invitations (list_id, user_id, invited_by, created_at) VALUES (?, ?, ?, ?)`,
		listID, userID, viewer.ID, s.now())
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		// The checks above run in this transaction, so this is only a second
		// invitation that got through them: the answer is the same as for the first.
		return Invitation{}, ErrAlreadyInvited
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("insert invitation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Invitation{}, fmt.Errorf("insert invitation: %w", err)
	}
	created, err := getInvitation(ctx, tx, viewer, id)
	if err != nil {
		return Invitation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Invitation{}, fmt.Errorf("commit: %w", err)
	}
	return created.invitation(), nil
}

// Accept makes the invited user a recipient of the list, for the invited user or
// their parent, and deletes the invitation. It returns the list as the viewer
// sees it after. It returns [ErrInvitationNotFound], [ErrNotInvitee] for a
// viewer who sees the invitation and isn't the user or their parent, and
// [ErrInviteeDeactivated].
func (s *Service) Accept(ctx context.Context, viewer user.User, id int64) (List, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return List{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inv, err := getInvitation(ctx, tx, viewer, id)
	if err != nil {
		return List{}, err
	}
	switch {
	case !inv.Mine:
		return List{}, ErrNotInvitee
	case !inv.canAccept():
		return List{}, ErrInviteeDeactivated
	}
	before, err := RecipientIDs(ctx, tx, inv.ListID)
	if err != nil {
		return List{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO list_recipients (list_id, user_id, created_at) VALUES (?, ?, ?)`, inv.ListID, inv.UserID, s.now())
	if err != nil {
		return List{}, fmt.Errorf("add recipient: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 && s.observer != nil {
		if err := s.observer.RecipientsChanged(ctx, tx, inv.ListID, before); err != nil {
			return List{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM list_invitations WHERE id = ?`, id); err != nil {
		return List{}, fmt.Errorf("delete invitation: %w", err)
	}
	l, err := GetVisible(ctx, tx, viewer, inv.ListID)
	if err != nil {
		return List{}, err
	}
	if err := tx.Commit(); err != nil {
		return List{}, fmt.Errorf("commit: %w", err)
	}
	return l, nil
}

// InvitationGuard runs inside the transaction of [Service.DeleteInvitation],
// before the write, with the invitation as the viewer sees it. An error from it
// stops the delete: an If-Match check goes there.
type InvitationGuard func(ctx context.Context, q sqlx.ExtContext, current Invitation) error

// DeleteInvitation declines or withdraws an invitation. A recipient of the list
// withdraws it, and the invited user or their parent declines it while the user
// is active. It returns [ErrInvitationNotFound] and [ErrInviteeDeactivated] for
// a parent of a deactivated user who isn't a recipient.
func (s *Service) DeleteInvitation(ctx context.Context, viewer user.User, id int64, guard InvitationGuard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	inv, err := getInvitation(ctx, tx, viewer, id)
	if err != nil {
		return err
	}
	if !inv.canDelete() {
		return ErrInviteeDeactivated
	}
	if guard != nil {
		if err := guard(ctx, tx, inv.invitation()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM list_invitations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
