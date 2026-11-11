package user

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// This file is the one place that says who can see a user (docs/spec.md,
// Visibility, Who can see a user). Everything that depends on it goes through
// [visibleTo]: the users collection and its total_count, a user's own address,
// the users of a group, and the check that an avatar may load (see [CanSee]).
// A rule that hides some users (a deactivated user) changes the condition here
// and nowhere else.
//
// The rule of who can see a group (the admin, and the users in it) is here too,
// as [GroupSeenBy], because both rules read the same set: the groups that
// contain a user ([GroupsContaining]). internal/group uses them both and
// repeats neither.

// sharesAGroup is true for a users row when the viewer and the user share a
// group. A group contains the users directly in it and the users directly in
// any group inside it, to any depth. So the viewer is in their own groups and
// in every group that contains one of those, and they share a group with every
// user in any of those groups or in a group inside one. It takes the viewer's
// ID once.
const sharesAGroup = `users.id IN (
	WITH RECURSIVE
	` + GroupsContaining + `,
	down(id) AS (
		SELECT id FROM up
		UNION
		SELECT gg.child_id FROM group_groups gg JOIN down ON gg.group_id = down.id
	)
	SELECT user_id FROM group_users WHERE group_id IN (SELECT id FROM down)
)`

// GroupsContaining is the definition of a recursive CTE named `up`: the groups
// that contain a user, which are the groups they are directly in and every group
// that holds one of those. `via` is the group the user is directly in that puts
// them in the group `id`. Put it after `WITH RECURSIVE`. It takes the user's ID
// once. It is the one copy of this rule.
const GroupsContaining = `up(id, via) AS (
		SELECT group_id, group_id FROM group_users WHERE user_id = ?
		UNION
		SELECT gg.group_id, up.via FROM group_groups gg JOIN up ON gg.child_id = up.id
	)`

// GroupSeenBy returns a SQL condition, and its arguments, that is true when the
// viewer can see the group whose ID is the SQL expression idExpr (such as
// "g.id"): the admin sees every group, and anyone else sees the groups that
// contain them.
func GroupSeenBy(viewer User, idExpr string) (string, []any) {
	if viewer.IsAdmin {
		return "1 = 1", nil
	}
	return idExpr + " IN (WITH RECURSIVE " + GroupsContaining + " SELECT id FROM up)", []any{viewer.ID}
}

// inGroupTree is true for a users row when the user is in the group with the
// ID, directly or through nesting, and the group is one the viewer can see: the
// admin sees every group, and anyone else sees the groups that contain them. A
// group the viewer can't see has no users here, so the filter doesn't tell the
// viewer that the group exists. The condition is only a filter on users. The
// viewer still sees only the users that [visibleTo] lets them see. It returns
// the condition and its arguments.
func inGroupTree(viewer User, groupID int64) (string, []any) {
	seen, seenArgs := GroupSeenBy(viewer, "g.id")
	// The walk starts at the group only if the viewer can see it, so a group they
	// can't see, or that doesn't exist, has no users.
	return `users.id IN (
		WITH RECURSIVE below(id) AS (
			SELECT g.id FROM groups g WHERE g.id = ? AND ` + seen + `
			UNION
			SELECT gg.child_id FROM group_groups gg JOIN below ON gg.group_id = below.id
		)
		SELECT user_id FROM group_users WHERE group_id IN (SELECT id FROM below)
	)`, append([]any{groupID}, seenArgs...)
}

// visibleTo returns a SQL condition on a row of the users table, true for the
// users whom the viewer can see, and its arguments. The admin sees every user.
// Anyone else sees themselves, the users they share a group with, their
// parents, and their children. Only direct links count: a parent's parent is not
// seen through the parent.
//
// A deactivated user is the exception (docs/spec.md, Deactivation): only the
// admin, their parents, and they themselves see them. So the viewer sees a
// deactivated user only as themselves, or as the user's parent. A shared group
// or a child doesn't show one.
func visibleTo(viewer User) (string, []any) {
	if viewer.IsAdmin {
		return "1 = 1", nil
	}
	return VisibleToWithoutAdminAccess(viewer)
}

// VisibleTo is [visibleTo] for the packages that build their own rule on it: an
// occasion is seen through its user (internal/occasion). It returns a SQL
// condition on a row of the users table, and its arguments.
func VisibleTo(viewer User) (string, []any) { return visibleTo(viewer) }

// VisibleToWithoutAdminAccess is [visibleTo] without the admin's access to every
// user: the admin is seen as anyone else is, through themselves, a shared group,
// and a parent link. Lists follow this rule (docs/spec.md, Visibility, Who can
// see a list): a list is visible through a recipient whom the viewer can see
// this way. It returns a SQL condition on a row of the users table, and its
// arguments.
func VisibleToWithoutAdminAccess(viewer User) (string, []any) {
	return `(users.id = ?
		OR users.id IN (SELECT child_id FROM user_parents WHERE parent_id = ?)
		OR (users.deactivated_at IS NULL AND (` + sharesAGroup + `
			OR users.id IN (SELECT parent_id FROM user_parents WHERE child_id = ?))))`,
		[]any{viewer.ID, viewer.ID, viewer.ID, viewer.ID}
}

// CanSee says whether the viewer can see the user with the given ID. It is false
// for a user who doesn't exist. q is a database or a transaction.
func CanSee(ctx context.Context, q sqlx.QueryerContext, viewer User, id int64) (bool, error) {
	seen, args := visibleTo(viewer)
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM users WHERE users.id = ? AND `+seen+`)`, append([]any{id}, args...)...)
	if err != nil {
		return false, fmt.Errorf("can see user: %w", err)
	}
	return ok, nil
}

// CanSeeWithoutAdminAccess says whether the viewer can see the user with the
// given ID without the admin's access to every user ([VisibleToWithoutAdminAccess]).
// An invitation follows it (docs/spec.md, Lists, Invitations). It is false for
// a user who doesn't exist. q is a database or a transaction.
func CanSeeWithoutAdminAccess(ctx context.Context, q sqlx.QueryerContext, viewer User, id int64) (bool, error) {
	seen, args := VisibleToWithoutAdminAccess(viewer)
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM users WHERE users.id = ? AND `+seen+`)`, append([]any{id}, args...)...)
	if err != nil {
		return false, fmt.Errorf("can see user: %w", err)
	}
	return ok, nil
}

// CanSeeSummary says whether the viewer gets the summary of the user with the
// given ID, which is what an avatar's visibility follows (docs/spec.md,
// Images). A summary reaches the viewers who can see the user ([CanSee]), and the
// viewers who can see a parent or a child of the user, because the user is in
// that user's `parents` or `children`. q is a database or a transaction.
func CanSeeSummary(ctx context.Context, q sqlx.QueryerContext, viewer User, id int64) (bool, error) {
	seen, args := visibleTo(viewer)
	var ok bool
	err := sqlx.GetContext(ctx, q, &ok, `SELECT EXISTS (SELECT 1 FROM users WHERE `+seen+`
		AND (users.id = ? OR users.id IN (
			SELECT parent_id FROM user_parents WHERE child_id = ?
			UNION SELECT child_id FROM user_parents WHERE parent_id = ?)))`,
		append(args, id, id, id)...)
	if err != nil {
		return false, fmt.Errorf("can see summary: %w", err)
	}
	return ok, nil
}
