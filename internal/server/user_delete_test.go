package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
	"jsclayton.dev/wishbone/internal/userdelete"
)

// These tests cover deleting a user, `wishbone delete-user` (docs/spec.md,
// Deactivation and deletion, Deletion; Admin and setup, CLI). The CLI is a thin
// shell over userdelete.Service, so they run the service on the env's database
// and read the result through the API, as the people who stay would. The clock
// starts on 2026-11-08, and every birthday is on Jan 1.

func (e *env) deleter() *userdelete.Service {
	o := e.srv.opts
	return userdelete.New(e.db, o.Lists, o.Items, o.Gifts, e.clock.now)
}

// deleteUser deletes a user for good and fails the test when it is refused.
func (e *env) deleteUser(id string) userdelete.Result {
	e.t.Helper()
	res, err := e.deleter().Delete(e.t.Context(), mustParse(e.t, id))
	require.NoError(e.t, err, "delete user %s", id)
	return res
}

// rowCount counts the rows of a table that match a condition, whoever sees them.
func (e *env) rowCount(table, where string, args ...any) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM `+table+` WHERE `+where, args...))
	return n
}

func TestDeletingAUserRefusesTheAdminAndAUserWhoIsNotThere(t *testing.T) {
	e := newEnv(t)
	f := e.family()

	_, err := e.deleter().Delete(t.Context(), mustParse(t, f.adminID))
	require.ErrorIs(t, err, userdelete.ErrAdmin)
	_, err = e.deleter().Delete(t.Context(), 999)
	require.ErrorIs(t, err, userdelete.ErrNotFound)

	require.Equal(t, http.StatusOK, e.getUser(f.admin, f.adminID).StatusCode, "the admin is still there")
	require.Equal(t, http.StatusOK, e.getUser(f.admin, f.sueID).StatusCode, "so is everyone else")
}

func TestADeleteThatFailsChangesNothing(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	tools := e.extraList("Dad's tools", f.dadID)
	saw := e.addItem(f.dad, tools, "Saw")
	grill := e.addItem(f.mom, f.house, "Grill")
	sues := e.claim(f.sue, saw.ID, nil)
	dads := e.claim(f.dad, e.addItem(f.sue, e.primaryListFor(f.sue, f.sueID), "Book").ID, nil)
	onHouse := e.claim(f.sue, grill.ID, nil)
	// The last step fails, after the gifts were released and the lists were left.
	_, err := e.db.ExecContext(t.Context(), `CREATE TRIGGER refuse_delete BEFORE DELETE ON users BEGIN SELECT RAISE(ABORT, 'refused'); END`)
	require.NoError(t, err)

	_, err = e.deleter().Delete(t.Context(), mustParse(t, f.dadID))

	require.ErrorContains(t, err, "refused")
	require.Equal(t, http.StatusOK, e.getUser(f.admin, f.dadID).StatusCode)
	require.Equal(t, http.StatusOK, e.getGift(f.dad, dads.ID).StatusCode, "his open gift is not released")
	require.Equal(t, http.StatusOK, e.getGift(f.sue, sues.ID).StatusCode, "nor the gift on his list deleted")
	require.Equal(t, 1, e.listRows(tools))
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(decode[listView](t, e.getList(f.mom, f.house)).Recipients)))
	require.Empty(t, flagCodes(e.gift(f.sue, onHouse.ID)), "no flag raised")
}

func TestDeletingAUserRemovesWhatBelongsToThemAndNothingOfAnyoneElse(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	// Sue has a session, a login link, a passkey, a group, an avatar, a custom
	// occasion, a parent and a child, and an invitation to a list.
	cousins := e.cousins(f)
	e.linkParents(f.admin, f.sueID, f.momID)
	babyID, _ := e.otherUser("Baby")
	e.linkParents(f.admin, babyID, f.sueID, f.dadID)
	avatar := e.uploaded(f.sue)
	require.Equal(t, http.StatusOK, e.setAvatar(f.sue, f.sueID, avatar.ID).StatusCode)
	e.addOccasion(f.sue, "Sue's day", annual(3, 4), f.sueID)
	e.registerPasskey(f.sueID, cookie(f.sue), passkeytest.New(t), "Phone")
	loginLink := e.makeLink(f.admin, f.sueID)
	e.invite(f.mom, f.house, f.sueID)
	// What others have stays.
	hedyOccasions := e.rowCount("occasions", `user_id = ?`, f.hedyID)
	suesList := e.primaryListFor(f.sue, f.sueID)

	res := e.deleteUser(f.sueID)

	require.Equal(t, "Sue", res.User.Name)
	// Sue is gone, with every way in.
	requireCoded(t, e.getUser(f.admin, f.sueID), http.StatusNotFound, "not_found")
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/users/me", header: cookie(f.sue)}).StatusCode, "her session")
	require.Equal(t, http.StatusUnauthorized, e.redeem(loginLink, "", nil).StatusCode, "her login link")
	require.Zero(t, e.rowCount("passkeys", `user_id = ?`, f.sueID))
	// Her group membership, parent links in both directions, and invitation.
	require.NotContains(t, names(decode[userList](t, e.groupUsers(f.admin, cousins, ""))), "Sue")
	require.Equal(t, []string{"Dad"}, summaryNames(decode[userView](t, e.getUser(f.admin, babyID)).Parents), "Baby keeps Dad, and loses Sue")
	require.Empty(t, decode[userView](t, e.getUser(f.admin, f.momID)).Children, "Mom's child is gone")
	require.Empty(t, decode[invitationList](t, e.listInvitations(f.mom, "")).Data)
	// Her primary list, birthday and custom occasions.
	require.Zero(t, e.rowCount("lists", `id = ?`, suesList))
	require.Zero(t, e.rowCount("occasions", `user_id = ?`, f.sueID))
	require.Equal(t, hedyOccasions, e.rowCount("occasions", `user_id = ?`, f.hedyID))
	// Her avatar goes to the sweep.
	require.True(t, e.row(avatar.ID).Detached)
	require.Equal(t, 1, e.sweep())
}

func TestDeletingAUserWhoIsOnlyAParentPrintsTheChildrenLeftWithNoParent(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	bobID, _ := e.otherUser("Bob")
	e.linkParents(f.admin, emmaID, f.dadID)
	e.linkParents(f.admin, bobID, f.dadID, f.momID)

	res := e.deleteUser(f.dadID)

	require.Len(t, res.Orphans, 1, "Bob keeps Mom")
	require.Equal(t, mustParse(t, emmaID), res.Orphans[0].ID)
	require.Equal(t, "Emma", res.Orphans[0].Name)
	require.Equal(t, []string{"Mom"}, summaryNames(decode[userView](t, e.getUser(f.admin, bobID)).Parents))
	require.Empty(t, decode[userView](t, e.getUser(f.admin, emmaID)).Parents, "a child with no parent is a valid state")
}
