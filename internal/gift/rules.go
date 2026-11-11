package gift

import (
	"context"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/user"
)

// This file is the one place that says who can see a gift and who can change one
// (docs/spec.md, Gifts, Who sees a gift; ADR 0011). The gifts collection, a
// gift's own address, the image a gift keeps, and every `can_*` field call it.

// VisibleTo returns a SQL condition on a row of the `gifts` table, true for the
// gifts that the viewer can see, and its arguments. Until a gift is given, only
// its giver sees it: it is a 404 for everyone else, whether or not they can see
// the item or the list. Once it is given, its recipients and their parents see it
// too. A parent keeps that read while the child is deactivated.
func VisibleTo(viewer user.User) (string, []any) {
	received, receivedArgs := receives(viewer, `user_parents WHERE parent_id = ?`)
	return `(gifts.giver_id = ? OR ` + received + `)`, append([]any{viewer.ID}, receivedArgs...)
}

// ReceivedBy returns a SQL condition on a row of the `gifts` table, true for the
// given gifts that the viewer receives and acts for: a gift recipient, or a parent
// of one who is active. It is the role that restores a gift and adds it again
// (docs/routes.md, Roles). A parent's powers through a deactivated recipient
// stop (docs/spec.md, Deactivation and deletion, Who acts while a user is
// deactivated), and their reads don't: the role that sees a given gift is
// [VisibleTo], which counts every child. `can_restore` and `can_add_again`, and
// both routes, call this one rule.
func ReceivedBy(viewer user.User) (string, []any) {
	return receives(viewer, `user_parents JOIN users ON users.id = user_parents.child_id
				WHERE user_parents.parent_id = ? AND users.deactivated_at IS NULL`)
}

// receives is the condition of a given gift that the viewer, or a child of the
// viewer from the query children, is among the recipients of. children selects
// child_id for the parents of a viewer and has one placeholder, the viewer.
func receives(viewer user.User, children string) (string, []any) {
	return `(gifts.given_at IS NOT NULL AND EXISTS (
			SELECT 1 FROM gift_recipients WHERE gift_recipients.gift_id = gifts.id AND (gift_recipients.user_id = ?
				OR gift_recipients.user_id IN (SELECT child_id FROM ` + children + `))))`,
		[]any{viewer.ID, viewer.ID}
}

// deactivatedChildReceives says whether the gift is one that a child of the
// viewer receives while the child is deactivated: the viewer sees the gift, and
// the only thing that stops them restoring it or adding it again is that the child
// is deactivated. q is a database or a transaction.
func deactivatedChildReceives(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (bool, error) {
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM gift_recipients JOIN users ON users.id = gift_recipients.user_id
		WHERE gift_recipients.gift_id = ? AND users.deactivated_at IS NOT NULL
		AND users.id IN (SELECT child_id FROM user_parents WHERE parent_id = ?))`, id, viewer.ID)
	if err != nil {
		return false, fmt.Errorf("find whether a deactivated child receives a gift: %w", err)
	}
	return ok, nil
}

// notReceived is the error for a viewer who sees a given gift and can't restore it
// or add it again: [ErrUserDeactivated] when their child who receives it is
// deactivated, and notRestorable otherwise.
func notReceived(ctx context.Context, q sqlx.QueryerContext, viewer user.User, g Gift, notRestorable error) error {
	if g.State == StateGiven {
		deactivated, err := deactivatedChildReceives(ctx, q, viewer, g.ID)
		if err != nil {
			return err
		}
		if deactivated {
			return ErrUserDeactivated
		}
	}
	return notRestorable
}

// CanRestoreOf says which of the gifts the viewer can restore and add again: the
// given gifts that they receive ([ReceivedBy]). It is the one rule of
// `can_restore` and `can_add_again`, and of the right to restore. q is a database
// or a transaction.
func CanRestoreOf(ctx context.Context, q sqlx.QueryerContext, viewer user.User, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	received, args := ReceivedBy(viewer)
	query, inArgs, err := sqlx.In(`SELECT gifts.id FROM gifts WHERE gifts.id IN (?) AND `+received, append([]any{ids}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("find the gifts that can be restored: %w", err)
	}
	var found []int64
	if err := sqlx.SelectContext(ctx, q, &found, query, inArgs...); err != nil {
		return nil, fmt.Errorf("find the gifts that can be restored: %w", err)
	}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// canEdit says whether the viewer can change the gift, and unclaim it: the giver,
// while the gift is open. It is the one rule of `can_edit` and `can_delete`. A
// given gift is the record of what was given, and nobody changes it.
func canEdit(viewer user.User, giverID *int64, given bool) bool {
	return giverID != nil && *giverID == viewer.ID && !given
}

// CanSee says whether the viewer can see the gift with an ID. The image that a
// gift keeps follows it. q is a database or a transaction.
func CanSee(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (bool, error) {
	seen, args := VisibleTo(viewer)
	var ok bool
	if err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM gifts WHERE gifts.id = ? AND `+seen+`)`, slices.Concat([]any{id}, args)...); err != nil {
		return false, fmt.Errorf("find out whether a gift is visible: %w", err)
	}
	return ok, nil
}

// SummaryReaches says whether a given gift that the viewer sees shows them the
// summary of a user, and so the avatar: the gift shows everyone its giver, and it
// shows its recipients to everyone but a giver who couldn't see the list at reveal
// (the rule of Gift.ShowRecipients). The avatar of that user loads for the viewer
// then (docs/spec.md, API principles, Images). q is a database or a
// transaction.
func SummaryReaches(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (bool, error) {
	seen, args := VisibleTo(viewer)
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM gifts WHERE gifts.given_at IS NOT NULL AND `+seen+` AND (gifts.giver_id = ?
			OR ((gifts.giver_id IS NULL OR gifts.giver_id <> ? OR gifts.giver_saw_list = 1)
				AND EXISTS (SELECT 1 FROM gift_recipients WHERE gift_recipients.gift_id = gifts.id AND gift_recipients.user_id = ?))))`,
		slices.Concat(args, []any{id, viewer.ID, id})...)
	if err != nil {
		return false, fmt.Errorf("find out whether a gift holds a summary: %w", err)
	}
	return ok, nil
}

// CanSee says whether the viewer can see the gift with an ID.
func (s *Service) CanSee(ctx context.Context, viewer user.User, id int64) (bool, error) {
	return CanSee(ctx, s.db, viewer, id)
}

// SummaryReaches says whether a given gift that the viewer sees shows them the
// summary of a user.
func (s *Service) SummaryReaches(ctx context.Context, viewer user.User, id int64) (bool, error) {
	return SummaryReaches(ctx, s.db, viewer, id)
}
