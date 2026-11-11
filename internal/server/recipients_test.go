package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover a list's recipients and leaving a list (docs/spec.md,
// Lists, Leaving and deleting; docs/routes.md, List routes).

type recipientView struct {
	User                 summaryView `json:"user"`
	CreatedAt            string      `json:"created_at"`
	SeesListAfterLeaving *bool       `json:"sees_list_after_leaving"`
	CanDelete            bool        `json:"can_delete"`
	Version              string      `json:"version"`
}

type recipientList struct {
	Data       []recipientView `json:"data"`
	NextCursor *string         `json:"next_cursor"`
	TotalCount int             `json:"total_count"`
}

func (e *env) listRecipients(token, listID, query string) *http.Response {
	e.t.Helper()
	path := "/lists/" + listID + "/recipients"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) leave(token, listID, userID string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodDelete, path: "/lists/" + listID + "/recipients/" + userID, header: h})
}

// recipientOf is the row of a user in a list's recipients, as the caller sees it.
func (e *env) recipientOf(token, listID, userID string) recipientView {
	e.t.Helper()
	for _, r := range decode[recipientList](e.t, e.listRecipients(token, listID, "")).Data {
		if r.User.ID == userID {
			return r
		}
	}
	e.t.Fatalf("user %s is not a recipient of list %s", userID, listID)
	return recipientView{}
}

func TestARecipientsRowsAreInTheOrderTheyJoinedAndAnyoneWhoSeesTheListReadsThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()

	for name, token := range map[string]string{"a recipient": f.mom, "a giver": f.sue} {
		res := e.listRecipients(token, f.house, "")
		require.Equal(t, http.StatusOK, res.StatusCode, name)
		got := decode[recipientList](t, res)
		require.Equal(t, []string{"Mom", "Dad"}, summaryNames([]summaryView{got.Data[0].User, got.Data[1].User}), name)
		require.Equal(t, 2, got.TotalCount, name)
		require.Nil(t, got.NextCursor, name)
		require.Equal(t, "2026-11-08T12:00:00Z", got.Data[0].CreatedAt, name)
		require.NotEmpty(t, got.Data[0].Version, name)
	}
	require.Equal(t, http.StatusNotFound, e.listRecipients(f.hedy, f.house, "").StatusCode)
	require.Equal(t, http.StatusNotFound, e.listRecipients(f.admin, f.house, "").StatusCode)
	require.Equal(t, http.StatusNotFound, e.listRecipients(f.mom, "9999", "").StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/lists/" + f.house + "/recipients"}).StatusCode)
	require.Equal(t, "unknown_filter", readProblem(t, e.listRecipients(f.mom, f.house, "nope=1")).Code)
	require.Equal(t, "unknown_sort", readProblem(t, e.listRecipients(f.mom, f.house, "sort=name")).Code)
}

func TestRecipientsPageAndSortByWhenTheyJoined(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.clock.advance(1_000_000_000)
	e.acceptInvitation(f.sue, e.invite(f.mom, f.house, f.sueID).ID)

	first := decode[recipientList](t, e.listRecipients(f.mom, f.house, "limit=2"))
	require.Len(t, first.Data, 2)
	require.NotNil(t, first.NextCursor)
	require.Equal(t, 3, first.TotalCount)
	next := decode[recipientList](t, e.listRecipients(f.mom, f.house, "limit=2&cursor="+*first.NextCursor))
	require.Equal(t, "Sue", next.Data[0].User.Name)
	require.Nil(t, next.NextCursor)
	newest := decode[recipientList](t, e.listRecipients(f.mom, f.house, "sort=-created_at"))
	require.Equal(t, "Sue", newest.Data[0].User.Name)
	require.Equal(t, "cursor_mismatch", readProblem(t, e.listRecipients(f.mom, f.house, "sort=-created_at&cursor="+*first.NextCursor)).Code)
}

func TestSeesListAfterLeavingIsForThatUserAndTheirParentsAndFollowsWhoTheyCanSee(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.sueID)
	e.join(f.admin, e.newGroup(f.admin, "Emma's").ID, emmaID, f.momID)
	e.acceptInvitation(f.sue, e.invite(f.mom, f.house, f.sueID).ID)
	e.acceptInvitation(f.sue, e.invite(f.mom, f.house, emmaID).ID)

	// Mom sees Sue and Emma (a group) so she would still see the list. Dad sees
	// nobody else, so he would not. Sue sees Mom, and Emma sees Mom too.
	for name, c := range map[string]struct {
		token, user string
		want        bool
	}{
		"Mom":  {f.mom, f.momID, true},
		"Dad":  {f.dad, f.dadID, false},
		"Sue":  {f.sue, f.sueID, true},
		"Emma": {f.sue, emmaID, true},
	} {
		got := e.recipientOf(c.token, f.house, c.user)
		require.NotNil(t, got.SeesListAfterLeaving, name)
		require.Equal(t, c.want, *got.SeesListAfterLeaving, name)
	}
	// For everyone else the key is absent, not false: Mom doesn't get Dad's.
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	decodeInto(t, e.listRecipients(f.mom, f.house, ""), &raw)
	for _, row := range raw.Data {
		_, has := row["sees_list_after_leaving"]
		require.Equal(t, row["user"].(map[string]any)["id"] == f.momID, has, "only Mom's own row has it for Mom: %v", row["user"])
	}
}

func TestSeesListAfterLeavingDoesNotCountTheAdminsAccess(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	adminList := decode[listView](t, e.postList(f.admin, map[string]any{"name": "Ada's"})).ID
	e.join(f.admin, e.newGroup(f.admin, "Ada and Sue").ID, f.adminID, f.sueID)
	e.acceptInvitation(f.sue, e.invite(f.admin, adminList, f.sueID).ID)

	// The admin sees every user, and sees Sue through a shared group, too.
	require.True(t, *e.recipientOf(f.admin, adminList, f.adminID).SeesListAfterLeaving)
	// Sue sees the admin through the group.
	require.True(t, *e.recipientOf(f.sue, adminList, f.sueID).SeesListAfterLeaving)
	// Without the group, the admin's access to every user doesn't count.
	solo := decode[listView](t, e.postList(f.admin, map[string]any{"name": "Solo"})).ID
	e.acceptInvitation(f.sue, e.invite(f.admin, solo, f.sueID).ID)
	e.db.MustExecContext(t.Context(), `DELETE FROM group_users WHERE user_id = ?`, f.adminID)
	require.False(t, *e.recipientOf(f.admin, solo, f.adminID).SeesListAfterLeaving)
}

func TestACanDeleteOnARecipientSaysWhoLeavesAndWhoMakesWhoLeave(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.sueID)
	e.join(f.admin, e.newGroup(f.admin, "Emma's").ID, emmaID, f.momID)
	e.acceptInvitation(f.sue, e.invite(f.mom, f.house, emmaID).ID)

	require.True(t, e.recipientOf(f.mom, f.house, f.momID).CanDelete, "Mom leaves")
	require.False(t, e.recipientOf(f.mom, f.house, f.dadID).CanDelete, "she can't make Dad leave")
	require.True(t, e.recipientOf(f.sue, f.house, emmaID).CanDelete, "Emma's parent makes her leave")
	require.False(t, e.recipientOf(f.sue, f.house, f.momID).CanDelete)
	// A primary list, and a list with one recipient, can't be left.
	require.False(t, e.recipientOf(f.mom, f.momList, f.momID).CanDelete)
	solo := e.extraList("Solo", f.momID)
	require.False(t, e.recipientOf(f.mom, solo, f.momID).CanDelete)
	// A parent who is not a recipient gets lists' can_leave false, and the
	// list's can_leave is for a recipient only.
	require.False(t, decode[listView](t, e.getList(f.sue, f.house)).CanLeave)
	require.True(t, decode[listView](t, e.getList(f.mom, f.house)).CanLeave)
	require.False(t, decode[listView](t, e.getList(f.mom, solo)).CanLeave)
}

func TestARecipientLeavesAListWhileAnotherRecipientRemains(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addItem(f.dad, f.house, "Grill")

	res := e.leave(f.mom, f.house, f.momID, nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	house := decode[listView](t, e.getList(f.dad, f.house))
	require.Equal(t, []string{"Dad"}, summaryNames(house.Recipients))
	require.False(t, house.CanLeave, "the last recipient can't leave")
	require.True(t, house.CanDelete)
	// Mom sees the list only if the rule lets her, and it doesn't: she can't see Dad.
	require.Equal(t, http.StatusNotFound, e.getList(f.mom, f.house).StatusCode)
	require.Equal(t, []string{"Grill"}, e.order(f.dad, f.house))
	require.Equal(t, http.StatusNotFound, e.leave(f.mom, f.house, f.momID, nil).StatusCode)
}

func TestAUserWhoLeavesAndCanStillSeeTheListSeesItAsAGiver(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.acceptInvitation(f.sue, e.invite(f.mom, f.house, f.sueID).ID)
	require.True(t, *e.recipientOf(f.sue, f.house, f.sueID).SeesListAfterLeaving)

	require.Equal(t, http.StatusNoContent, e.leave(f.sue, f.house, f.sueID, nil).StatusCode)

	got := decode[listView](t, e.getList(f.sue, f.house))
	require.Equal(t, []string{"Mom", "Dad"}, summaryNames(got.Recipients))
	require.False(t, got.CanEdit)
	require.False(t, got.CanLeave)
}

func TestTheLastRecipientAndAPrimaryListCannotBeLeft(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	solo := e.extraList("Solo", f.momID)

	last := e.leave(f.mom, solo, f.momID, nil)
	require.Equal(t, http.StatusForbidden, last.StatusCode)
	require.Equal(t, "last_recipient", readProblem(t, last).Code)

	primary := e.leave(f.mom, f.momList, f.momID, nil)
	require.Equal(t, http.StatusForbidden, primary.StatusCode)
	require.Equal(t, "primary_list", readProblem(t, primary).Code)

	require.Equal(t, http.StatusOK, e.getList(f.mom, solo).StatusCode)
	require.Equal(t, http.StatusOK, e.getList(f.mom, f.momList).StatusCode)
}

func TestOnlyTheUserOrTheirParentLeaves(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.acceptInvitation(f.sue, e.invite(f.mom, f.house, f.sueID).ID)

	// Sue sees the list and Mom as a recipient, and can't make Mom leave. Dad
	// can't make Mom leave. Both are 403: the recipient exists and is seen.
	for name, c := range map[string]struct{ token, user string }{
		"another recipient who sees them": {f.sue, f.momID},
		"another recipient":               {f.dad, f.momID},
	} {
		res := e.leave(c.token, f.house, c.user, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "forbidden", readProblem(t, res).Code, name)
	}
	// A list the caller can't see, a user who isn't a recipient, and a bad ID.
	require.Equal(t, http.StatusNotFound, e.leave(f.hedy, f.house, f.momID, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.leave(f.admin, f.house, f.momID, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.leave(f.mom, f.house, f.hedyID, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.leave(f.mom, f.house, "abc", nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.leave(f.mom, "abc", f.momID, nil).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{method: http.MethodDelete, path: "/lists/" + f.house + "/recipients/" + f.momID}).StatusCode)
	require.Equal(t, []string{"Mom", "Dad", "Sue"}, summaryNames(decode[listView](t, e.getList(f.mom, f.house)).Recipients))
}

func TestAParentMakesTheirChildLeave(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.sueID)
	e.join(f.admin, e.newGroup(f.admin, "Emma's").ID, emmaID, f.momID)
	e.acceptInvitation(emma, e.invite(f.mom, f.house, emmaID).ID)

	res := e.leave(f.sue, f.house, emmaID, nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, []string{"Mom", "Dad"}, summaryNames(decode[listView](t, e.getList(f.mom, f.house)).Recipients))
	// Emma still sees the list through Mom, as a giver.
	require.False(t, decode[listView](t, e.getList(emma, f.house)).CanEdit)
}

func TestALeaveHonorsIfMatch(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	row := e.recipientOf(f.mom, f.house, f.momID)

	stale := e.leave(f.mom, f.house, f.momID, http.Header{"If-Match": {`"stale"`}})
	require.Equal(t, http.StatusPreconditionFailed, stale.StatusCode)
	require.Equal(t, []string{"Mom", "Dad"}, summaryNames(decode[listView](t, e.getList(f.mom, f.house)).Recipients))

	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, http.Header{"If-Match": {row.Version}}).StatusCode)
}
