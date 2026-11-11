package occasion

import (
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/user"
)

// This file is the one place that says who can see an occasion and who can
// change one (docs/routes.md, Occasion fields). Everything that depends on a
// rule goes through it: the occasions collection and its total_count, an
// occasion's own address, and the `can_*` fields. A change to a rule
// changes the condition here and nowhere else.

// reachCTE is the definition of a recursive CTE named `reach`: the pairs of an
// occasion and a group that it reaches, which are the groups it is attached to
// and every group inside one of those, to any depth. A user directly in a
// reached group is a user that the occasion applies to. Put it after
// `WITH RECURSIVE`. It takes no arguments. It is the one copy of this rule.
const reachCTE = `reach(occasion_id, group_id) AS (
		SELECT occasion_id, group_id FROM occasion_groups
		UNION
		SELECT r.occasion_id, gg.child_id FROM group_groups gg JOIN reach r ON gg.group_id = r.group_id
	)`

// AppliesToUser returns a SQL condition on a row of the `occasions` table, true
// for the shared occasions that apply to the user with the ID, and its
// arguments. A shared occasion applies to every user when it is marked everyone,
// and otherwise to the users in the groups it reaches ([reachCTE]). The user
// must exist.
func AppliesToUser(userID int64) (string, []any) {
	return `(occasions.kind = 'shared' AND EXISTS (SELECT 1 FROM users WHERE users.id = ?)
		AND (occasions.is_everyone = 1 OR occasions.id IN (
			WITH RECURSIVE ` + reachCTE + `
			SELECT r.occasion_id FROM reach r JOIN group_users gu ON gu.group_id = r.group_id
			WHERE gu.user_id = ?)))`, []any{userID, userID}
}

// VisibleTo returns a SQL condition on a row of the `occasions` table, true for
// the occasions that the viewer can see, and its arguments. A birthday or a
// custom occasion is seen by the users who see its user. A birthday follows the
// rule of who can see a user, admin's access included. A custom occasion doesn't
// count the admin's access: the admin sees it only as any other user would,
// through themselves, a shared group, or a parent link (docs/spec.md, The
// admin). A shared occasion is seen by the admin, who manages it, and by every
// user it applies to and every user who sees a user it applies to
// (docs/routes.md, Occasion fields). A viewer sees themselves, so a shared
// occasion marked everyone is seen by everyone.
func VisibleTo(viewer user.User) (string, []any) {
	anyUser, anyArgs := user.VisibleTo(viewer)
	bare, bareArgs := user.VisibleToWithoutAdminAccess(viewer)
	own := `(occasions.kind = 'birthday' AND occasions.user_id IN (SELECT users.id FROM users WHERE ` + anyUser + `))
		OR (occasions.kind = 'custom' AND occasions.user_id IN (SELECT users.id FROM users WHERE ` + bare + `))`
	if viewer.IsAdmin {
		return `(` + own + ` OR occasions.kind = 'shared')`, slices.Concat(anyArgs, bareArgs)
	}
	return `(` + own + `
		OR (occasions.kind = 'shared' AND (occasions.is_everyone = 1 OR occasions.id IN (
			WITH RECURSIVE ` + reachCTE + `
			SELECT r.occasion_id FROM reach r
				JOIN group_users gu ON gu.group_id = r.group_id
				JOIN users ON users.id = gu.user_id
			WHERE ` + anyUser + `))))`, slices.Concat(anyArgs, bareArgs, anyArgs)
}

// appliesToWhere returns a SQL condition on a row of the `users` table, true for
// the users that the occasion applies to, and its arguments: a birthday or a
// custom occasion applies to its user, a shared occasion to every user when it is
// marked everyone, and otherwise to the users in the groups it reaches
// ([reachCTE]). It checks nothing about the viewer. A caller shows first that
// the viewer can see the occasion, and the users collection narrows the result
// to the users the viewer can see.
func appliesToWhere(o Occasion) (string, []any) {
	switch {
	case o.Type != TypeShared:
		return `users.id = ?`, []any{o.User.ID}
	case o.IsEveryone:
		return `1 = 1`, nil
	}
	return `users.id IN (WITH RECURSIVE ` + reachCTE + `
		SELECT gu.user_id FROM reach r JOIN group_users gu ON gu.group_id = r.group_id WHERE r.occasion_id = ?)`, []any{o.ID}
}

// ManagedBy returns a SQL condition on a row of the `occasions` table, true when
// the viewer is the occasion's user or one of the user's parents, and its
// arguments. It is the role only: it says nothing about the kind of occasion or
// whether the user is active (see [Occasion.CanEdit]). The admin has no such
// role, so the admin changes no one's occasions.
func ManagedBy(viewer user.User) (string, []any) {
	return `occasions.user_id IN (SELECT users.id FROM users
		WHERE users.id = ? OR users.id IN (SELECT child_id FROM user_parents WHERE parent_id = ?))`, []any{viewer.ID, viewer.ID}
}

// Manages is [ManagedBy] for a user who is already loaded, with their parents:
// the viewer is the user or one of their parents. User.Parents is filled by
// [user.Service.Get].
func Manages(viewer, owner user.User) bool {
	return viewer.ID == owner.ID || owner.HasParent(viewer.ID)
}

// CanAddFor says whether the viewer can add a custom occasion for the user: the
// user or their parent, while the user is active (docs/routes.md, User
// fields: `can_add_occasion`). A deactivated user's parent keeps the reads and
// loses the writes (docs/spec.md, Deactivation and deletion).
func CanAddFor(viewer, owner user.User) bool {
	return Manages(viewer, owner) && owner.Active()
}

// PickableFor returns a SQL condition on a row of the `occasions` table, true for
// the occasions that apply to a recipient of the list whom the viewer may give
// to, and its arguments (docs/spec.md, Gifts, Occasions for a gift). A
// recipient counts when the viewer can see them, is not the viewer, and is active:
// for gifts, no giver sees a deactivated recipient. A birthday and a custom
// occasion apply to their user, and a shared occasion applies to the users it
// reaches, so a shared occasion that reaches several recipients is one row. The
// list must be one the viewer can see, else nothing matches. The condition checks
// nothing about the occasion's own visibility: the collection and a gift's
// occasion check [VisibleTo] as well, so the admin's access never shows a custom
// occasion through a gift.
func PickableFor(viewer user.User, listID int64) (string, []any) {
	recipients, recipientArgs := list.GivableRecipients(viewer, "?")
	recipientArgs = slices.Concat([]any{listID}, recipientArgs)
	seenList, seenListArgs := list.VisibleTo(viewer)
	applies, appliesArgs := appliesToAny(recipients, recipientArgs)
	return `(? IN (SELECT lists.id FROM lists WHERE ` + seenList + `) AND ` + applies + `)`, slices.Concat([]any{listID}, seenListArgs, appliesArgs)
}

// appliesToAny returns a SQL condition on a row of the `occasions` table, true
// for the occasions that apply to some user selected by the query users (a
// SELECT of user IDs, with its arguments), and the condition's arguments. A
// birthday and a custom occasion apply to their user, and a shared occasion to
// the users it reaches, so one that reaches several of them is one row.
func appliesToAny(users string, usersArgs []any) (string, []any) {
	return `(occasions.user_id IN (` + users + `)
		OR (occasions.kind = 'shared' AND (
			(occasions.is_everyone = 1 AND EXISTS (` + users + `))
			OR occasions.id IN (
				WITH RECURSIVE ` + reachCTE + `
				SELECT r.occasion_id FROM reach r JOIN group_users gu ON gu.group_id = r.group_id
				WHERE gu.user_id IN (` + users + `)))))`, slices.Concat(usersArgs, usersArgs, usersArgs)
}

// AppliesToAnyOf returns a SQL condition on a row of the `occasions` table, true
// for the occasions that apply to one of the users with these IDs, and its
// arguments. It checks nothing about who can see the occasion: the server uses it
// to pick the occasion of a gift whose giver can see none of the recipients.
func AppliesToAnyOf(userIDs []int64) (string, []any) {
	if len(userIDs) == 0 {
		return appliesToAny(`SELECT id FROM users WHERE 1 = 0`, nil)
	}
	in, args, err := sqlx.In(`SELECT id FROM users WHERE id IN (?)`, userIDs)
	if err != nil {
		// sqlx.In fails only for an argument that is not a slice, or an empty one.
		panic(fmt.Sprintf("occasion: applies to any of: %v", err))
	}
	return appliesToAny(in, args)
}
