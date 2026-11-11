package list

import (
	"context"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/user"
)

// This file is the one place that says who can see a list and who can edit one
// (docs/spec.md, Visibility, Who can see a list; Lists, Who edits a list).
// Everything that depends on a rule goes through it: the lists collection and
// its total_count, a list's own address, the items of a list, an item's address,
// the `?can_edit=` filter, and every `can_*` field. A change to a rule
// changes the condition here and nowhere else. [PrimaryListsVisibleTo] and
// [SummaryReaches] read [VisibleTo], so they change with it.

// VisibleTo returns a SQL condition on a row of the `lists` table, true for the
// lists that the viewer can see, and its arguments. A viewer sees a list when
// they are one of its recipients, or can see one of its recipients (ADR 0010).
// The admin's access to every user doesn't count: the admin sees a recipient only
// as any other user does. A parent sees a user, so a parent sees all of their
// child's lists, even while the child is deactivated (a deactivated user is seen
// by their parents, the admin, and themselves).
func VisibleTo(viewer user.User) (string, []any) {
	seen, args := user.VisibleToWithoutAdminAccess(viewer)
	return `lists.id IN (SELECT list_id FROM list_recipients JOIN users ON users.id = list_recipients.user_id WHERE ` + seen + `)`, args
}

// EditableBy returns a SQL condition on a row of the `lists` table, true for the
// lists that the viewer can edit: to add, change, reorder, and remove its items,
// to rename it, and to change its description. The viewer can edit a list when
// they are a recipient or the parent of one (ADR 0006). A list that the viewer
// can edit is one they can see, so a route checks visibility first.
//
// A parent's edit through a deactivated recipient stops (docs/spec.md,
// Deactivation and deletion, Who acts while a user is deactivated): the parent
// branch needs an active recipient ([UserOrTheirActiveParent]). [NotEditable]
// says why a viewer who sees a list can't edit it.
func EditableBy(viewer user.User) (string, []any) {
	who, args := UserOrTheirActiveParent(viewer, "user_id")
	return `lists.id IN (SELECT list_id FROM list_recipients WHERE ` + who + `)`, args
}

// NotEditable returns why the viewer, who sees the list, can't edit it:
// [ErrRecipientDeactivated] when they are the parent of one of its recipients who
// is deactivated, and [ErrNotEditable] otherwise. The caller has found that
// [EditableBy] is false. q is a database or a transaction.
func NotEditable(ctx context.Context, q sqlx.QueryerContext, viewer user.User, listID int64) error {
	var deactivated bool
	err := sqlx.GetContext(ctx, q, &deactivated, `SELECT EXISTS (SELECT 1 FROM list_recipients JOIN users ON users.id = list_recipients.user_id
		WHERE list_recipients.list_id = ? AND users.deactivated_at IS NOT NULL
		AND users.id IN (SELECT child_id FROM user_parents WHERE parent_id = ?))`, listID, viewer.ID)
	if err != nil {
		return fmt.Errorf("find why a list isn't editable: %w", err)
	}
	if deactivated {
		return ErrRecipientDeactivated
	}
	return ErrNotEditable
}

// NotEditable is the package function on the service's database.
func (s *Service) NotEditable(ctx context.Context, viewer user.User, listID int64) error {
	return NotEditable(ctx, s.db, viewer, listID)
}

// UserOrTheirParent returns a SQL condition, true when the column col (a user
// ID) holds the viewer or a child of the viewer, and its arguments. It is the
// role of "the user or their parent" for the reads: an invitation, and who the
// leave warning is for. A deactivated user's parent keeps the reads and loses the
// writes (docs/spec.md, Deactivation and deletion, Who acts while a user is
// deactivated), so a rule that writes through this role uses
// [UserOrTheirActiveParent].
func UserOrTheirParent(viewer user.User, col string) (string, []any) {
	return `(` + col + ` = ? OR ` + col + ` IN (SELECT child_id FROM user_parents WHERE parent_id = ?))`, []any{viewer.ID, viewer.ID}
}

// UserOrTheirActiveParent is [UserOrTheirParent] for a write: the parent's
// power through a child stops while the child is deactivated. Editing a list and
// leaving one use it. The viewer is a signed-in user, so they are active.
func UserOrTheirActiveParent(viewer user.User, col string) (string, []any) {
	return `(` + col + ` = ? OR ` + col + ` IN (SELECT child_id FROM user_parents JOIN users ON users.id = user_parents.child_id
		WHERE user_parents.parent_id = ? AND users.deactivated_at IS NULL))`, []any{viewer.ID, viewer.ID}
}

// InvitationVisibleTo returns a SQL condition on a row of the `list_invitations`
// table, true for the invitations that the viewer can see, and its arguments. An
// invitation is seen by the invited user, their parents, and the recipients of
// its list (docs/spec.md, Lists, Invitations). It is its own rule: it doesn't
// follow who can see the list or the user, and the admin has no access of its
// own. The invitations collection, an invitation's address, and every `can_*`
// field of an invitation call it. [SummaryReaches] reads it too, for the users in
// an invitation.
func InvitationVisibleTo(viewer user.User) (string, []any) {
	who, args := UserOrTheirParent(viewer, "list_invitations.user_id")
	return `(` + who + ` OR list_invitations.list_id IN (SELECT list_id FROM list_recipients WHERE user_id = ?))`, append(args, viewer.ID)
}

// InvitableUsers returns a SQL condition on a row of the `users` table, true for
// the users whom the viewer could invite to the list, and its arguments: users
// they see, with the admin's access not counted, who are active, aren't
// recipients, and have no pending invitation to it. It checks nothing about the
// viewer's right to invite: [Service.InvitableWhere] does that.
func InvitableUsers(viewer user.User, listID int64) (string, []any) {
	seen, args := user.VisibleToWithoutAdminAccess(viewer)
	return `(users.deactivated_at IS NULL AND ` + seen + `
		AND users.id NOT IN (SELECT user_id FROM list_recipients WHERE list_id = ?)
		AND users.id NOT IN (SELECT user_id FROM list_invitations WHERE list_id = ?))`, append(args, listID, listID)
}

// ItemShownTo returns a SQL condition on a row of the `items` table, true for
// the items that the viewer sees on a list that they can see, and its arguments
// (docs/spec.md, Lists, Screens; Items, Who changes an item). Nobody sees an
// item that was deleted while it had gifts (a soft delete). A recipient sees every
// other item. A giver, who is anyone else who sees the list, doesn't see a fully
// claimed item, unless they have an open gift on it. An unlimited item is never
// fully claimed. The condition says nothing about the list: callers check
// [VisibleTo] too. The items of a list, an item's address, a list's `item_count`,
// the images of an item, and the users that an item's `added_by` shows all call it.
func ItemShownTo(viewer user.User) (string, []any) {
	return `(items.deleted_at IS NULL AND (
		items.list_id IN (SELECT list_id FROM list_recipients WHERE user_id = ?)
		OR items.is_unlimited = 1
		OR items.quantity > COALESCE((SELECT SUM(g.quantity) FROM gifts g WHERE g.item_id = items.id AND g.given_at IS NULL), 0)
		OR EXISTS (SELECT 1 FROM gifts g WHERE g.item_id = items.id AND g.giver_id = ? AND g.given_at IS NULL)))`,
		[]any{viewer.ID, viewer.ID}
}

// GivableRecipients returns a SQL query that selects the IDs of the recipients of
// a list to whom the viewer may give, and its arguments: a recipient whom the
// viewer can see, who is not the viewer, and who is active (for gifts, no giver
// sees a deactivated recipient; docs/spec.md, Gifts, Occasions for a gift).
// listRef is the SQL that names the list's ID, such as "?" or "items.list_id".
// Its argument, if it has one, comes before the returned arguments. A viewer
// can claim on a list when this selects a row and they are not a recipient, and
// the occasions they can pick are those of these recipients.
func GivableRecipients(viewer user.User, listRef string) (string, []any) {
	seen, seenArgs := user.VisibleTo(viewer)
	return `SELECT lr.user_id FROM list_recipients lr JOIN users ON users.id = lr.user_id
		WHERE lr.list_id = ` + listRef + ` AND lr.user_id <> ? AND users.deactivated_at IS NULL AND ` + seen,
		append([]any{viewer.ID}, seenArgs...)
}

// SeesListAfterLeaving says whether the user could still see the list after they
// leave it: they can see another of its recipients, with the admin's access not
// counted (docs/spec.md, Visibility, Who can see a list). It is the rule of
// [VisibleTo] for the list without that one recipient. q is a database or a
// transaction.
func SeesListAfterLeaving(ctx context.Context, q sqlx.QueryerContext, listID, userID int64) (bool, error) {
	// The rule reads only the viewer's ID.
	seen, args := user.VisibleToWithoutAdminAccess(user.User{ID: userID})
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM list_recipients JOIN users ON users.id = list_recipients.user_id
		WHERE list_recipients.list_id = ? AND list_recipients.user_id <> ? AND `+seen+`)`, append([]any{listID, userID}, args...)...)
	if err != nil {
		return false, fmt.Errorf("sees list after leaving: %w", err)
	}
	return ok, nil
}

// PrimaryListsVisibleTo says which of the users' primary lists the viewer can
// see, by user ID. A user's `primary_list` field follows it. It runs the
// condition of [VisibleTo] itself, so the two can't differ. q is a database or a
// transaction.
func PrimaryListsVisibleTo(ctx context.Context, q sqlx.QueryerContext, viewer user.User, ownerIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool, len(ownerIDs))
	if len(ownerIDs) == 0 {
		return out, nil
	}
	seen, seenArgs := VisibleTo(viewer)
	in, args, err := sqlx.In(`lists.primary_user_id IN (?)`, ownerIDs)
	if err != nil {
		return nil, fmt.Errorf("primary lists visible: %w", err)
	}
	var seenOwners []int64
	if err := sqlx.SelectContext(ctx, q, &seenOwners, `SELECT lists.primary_user_id FROM lists WHERE `+in+` AND `+seen, append(args, seenArgs...)...); err != nil {
		return nil, fmt.Errorf("primary lists visible: %w", err)
	}
	for _, id := range seenOwners {
		out[id] = true
	}
	return out, nil
}

// SummaryReaches says whether the summary of the user reaches the viewer
// through a list: the user is a recipient of a list the viewer can see, or added
// an item on one, or is the invited user or the inviter on an invitation that the
// viewer can see. A summary on a list doesn't make the user visible, but the
// avatar in it loads for everyone who gets the summary (docs/spec.md, Images).
// q is a database or a transaction.
func SummaryReaches(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (bool, error) {
	seen, args := VisibleTo(viewer)
	shown, shownArgs := ItemShownTo(viewer)
	invited, invitedArgs := InvitationVisibleTo(viewer)
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM lists WHERE `+seen+` AND (
			lists.id IN (SELECT list_id FROM list_recipients WHERE user_id = ?)
			OR lists.id IN (SELECT list_id FROM items WHERE added_by = ? AND `+shown+`)))
		OR EXISTS (SELECT 1 FROM list_invitations WHERE `+invited+` AND (list_invitations.user_id = ? OR list_invitations.invited_by = ?))`,
		slices.Concat(args, []any{id, id}, shownArgs, invitedArgs, []any{id, id})...)
	if err != nil {
		return false, fmt.Errorf("summary reaches: %w", err)
	}
	return ok, nil
}

// IsRecipient says whether the viewer is one of the list's recipients.
func (l List) IsRecipient(viewer user.User) bool {
	for _, r := range l.Recipients {
		if r.ID == viewer.ID {
			return true
		}
	}
	return false
}

// CanDelete says whether the viewer may delete the list: an extra list with one
// recipient, and the viewer can edit it (the recipient, or their parent).
func (l List) CanDelete() bool {
	return !l.IsPrimary && len(l.Recipients) == 1 && l.CanEdit
}

// CanInvite says whether the viewer may invite a user to the list: an extra
// list, and the viewer is a recipient.
func (l List) CanInvite(viewer user.User) bool {
	return !l.IsPrimary && l.IsRecipient(viewer)
}

// CanLeave says whether the viewer may leave the list: it has more than one
// recipient, and the viewer is one of them.
func (l List) CanLeave(viewer user.User) bool {
	return l.CanRemove(l.IsRecipient(viewer))
}

// CanRemove says whether a recipient can leave the list, for a viewer who is that
// recipient or their parent (mine): an extra list with another recipient. It is
// the one rule of leaving: `can_leave` on a list and `can_delete` on a recipient
// both call it. mine is false for a parent through a deactivated child (see
// [UserOrTheirActiveParent]).
func (l List) CanRemove(mine bool) bool {
	return mine && !l.IsPrimary && len(l.Recipients) > 1
}
