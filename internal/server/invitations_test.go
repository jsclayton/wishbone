package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover invitations to an extra list (docs/spec.md, Lists,
// Invitations; docs/routes.md, Invitations) and the invitable users filter of
// GET /users.

// invitationView is an invitation as the API shows it.
type invitationView struct {
	ID        string       `json:"id"`
	List      listSummary  `json:"list"`
	User      summaryView  `json:"user"`
	InvitedBy *summaryView `json:"invited_by"`
	CreatedAt string       `json:"created_at"`
	Version   string       `json:"version"`

	CanAccept bool `json:"can_accept"`
	CanDelete bool `json:"can_delete"`
}

type listSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type invitationList struct {
	Data       []invitationView `json:"data"`
	NextCursor *string          `json:"next_cursor"`
	TotalCount int              `json:"total_count"`
}

func (e *env) postInvitation(token, listID string, body any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/lists/" + listID + "/invitations", body: body, header: cookie(token)})
}

// invite invites a user as a recipient of the list, and fails the test when the
// server refuses.
func (e *env) invite(token, listID, userID string) invitationView {
	e.t.Helper()
	res := e.postInvitation(token, listID, map[string]any{"user_id": userID})
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "invite")
	return decode[invitationView](e.t, res)
}

func (e *env) listInvitations(token, query string) *http.Response {
	e.t.Helper()
	path := "/invitations"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) getInvitation(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/invitations/" + id, header: cookie(token)})
}

func (e *env) acceptInvitation(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/invitations/" + id + "/accept", header: cookie(token)})
}

func (e *env) deleteInvitation(token, id string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodDelete, path: "/invitations/" + id, header: h})
}

func invitationUsers(l invitationList) []string {
	out := make([]string, len(l.Data))
	for i, v := range l.Data {
		out[i] = v.User.Name
	}
	return out
}

func TestARecipientInvitesAUserTheySeeAndNothingChangesForThemUntilTheyAccept(t *testing.T) {
	e := newEnv(t)
	f := e.family()

	res := e.postInvitation(f.mom, f.house, map[string]any{"user_id": f.sueID})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	inv := decode[invitationView](t, res)
	require.Equal(t, "/api/v2/invitations/"+inv.ID, res.Header.Get("Location"))
	require.NotEmpty(t, res.Header.Get("ETag"))
	require.Equal(t, listSummary{ID: f.house, Name: "Our house"}, inv.List)
	require.Equal(t, f.sueID, inv.User.ID)
	require.Equal(t, "Sue", inv.User.Name)
	require.Equal(t, f.momID, inv.InvitedBy.ID)
	require.Equal(t, "2026-11-08T12:00:00Z", inv.CreatedAt)
	require.False(t, inv.CanAccept, "the inviter can't accept for Sue")
	require.True(t, inv.CanDelete, "a recipient withdraws it")

	// Sue sees it, and can accept and decline.
	got := decode[invitationView](t, e.getInvitation(f.sue, inv.ID))
	require.Equal(t, inv.ID, got.ID)
	require.True(t, got.CanAccept)
	require.True(t, got.CanDelete)
	// Nothing changes for her: she is not a recipient, and sees the list as a giver.
	house := decode[listView](t, e.getList(f.sue, f.house))
	require.Equal(t, []string{"Mom", "Dad"}, summaryNames(house.Recipients))
	require.False(t, house.CanEdit)
	require.False(t, house.CanInvite)
	require.False(t, house.CanLeave)
	// Dad, a recipient, sees it too, and can withdraw it.
	require.True(t, decode[invitationView](t, e.getInvitation(f.dad, inv.ID)).CanDelete)
}

func TestAnInvitationIsA404ForEveryoneExceptTheInvitedUserTheirParentsAndTheListsRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	graceID, grace := e.otherUser("Grace")
	e.join(f.admin, e.newGroup(f.admin, "Cousins too").ID, f.momID, graceID)
	e.linkParents(f.admin, graceID, f.sueID)
	inv := e.invite(f.mom, f.house, graceID)

	for name, token := range map[string]string{"Grace": grace, "her parent Sue": f.sue, "recipient Dad": f.dad, "recipient Mom": f.mom} {
		require.Equal(t, http.StatusOK, e.getInvitation(token, inv.ID).StatusCode, name)
		require.Equal(t, []string{"Grace"}, invitationUsers(decode[invitationList](t, e.listInvitations(token, ""))), name)
		require.Equal(t, 1, decode[invitationList](t, e.listInvitations(token, "")).TotalCount, name)
	}
	// The admin has no role on it, and neither does a stranger.
	for name, token := range map[string]string{"admin": f.admin, "Hedy": f.hedy} {
		require.Equal(t, http.StatusNotFound, e.getInvitation(token, inv.ID).StatusCode, name)
		require.Empty(t, decode[invitationList](t, e.listInvitations(token, "")).Data, name)
		require.Equal(t, http.StatusNotFound, e.acceptInvitation(token, inv.ID).StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.deleteInvitation(token, inv.ID, nil).StatusCode, name)
	}
	require.Equal(t, http.StatusNotFound, e.getInvitation(f.mom, "9999").StatusCode)
	require.Equal(t, http.StatusNotFound, e.getInvitation(f.mom, "abc").StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/invitations"}).StatusCode)
}

func TestWhoCanInviteWhomAndWhere(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	deactivatedID, _ := e.otherUser("Gone")
	e.linkParents(f.admin, deactivatedID, f.momID) // only a parent still sees a deactivated user
	e.deactivated(f.admin, deactivatedID)
	adminList := decode[listView](t, e.postList(f.admin, map[string]any{"name": "Ada's"})).ID

	type tc struct {
		token, list string
		body        any
		status      int
		code        string
	}
	for name, c := range map[string]tc{
		"a primary list":                          {f.mom, f.momList, map[string]any{"user_id": f.sueID}, http.StatusForbidden, "primary_list"},
		"a giver, who isn't a recipient":          {f.sue, f.house, map[string]any{"user_id": f.momID}, http.StatusForbidden, "forbidden"},
		"a list the caller can't see":             {f.hedy, f.house, map[string]any{"user_id": f.hedyID}, http.StatusNotFound, "not_found"},
		"a list that doesn't exist":               {f.mom, "9999", map[string]any{"user_id": f.sueID}, http.StatusNotFound, "not_found"},
		"a recipient the caller can't see":        {f.mom, f.house, map[string]any{"user_id": f.dadID}, http.StatusUnprocessableEntity, "reference_not_found"},
		"oneself":                                 {f.mom, f.house, map[string]any{"user_id": f.momID}, http.StatusConflict, "already_recipient"},
		"a deactivated user":                      {f.mom, f.house, map[string]any{"user_id": deactivatedID}, http.StatusForbidden, "user_deactivated"},
		"a user the caller can't see":             {f.dad, f.house, map[string]any{"user_id": f.sueID}, http.StatusUnprocessableEntity, "reference_not_found"},
		"a user that doesn't exist":               {f.mom, f.house, map[string]any{"user_id": "9999"}, http.StatusUnprocessableEntity, "reference_not_found"},
		"not an ID":                               {f.mom, f.house, map[string]any{"user_id": "abc"}, http.StatusUnprocessableEntity, "reference_not_found"},
		"a user the admin sees only as the admin": {f.admin, adminList, map[string]any{"user_id": f.hedyID}, http.StatusForbidden, "reference_not_allowed"},
		"no user": {f.mom, f.house, map[string]any{}, http.StatusUnprocessableEntity, "invalid_input"},
	} {
		res := e.postInvitation(c.token, c.list, c.body)
		require.Equal(t, c.status, res.StatusCode, name)
		require.Equal(t, c.code, readProblem(t, res).Code, name)
	}
	require.Empty(t, decode[invitationList](t, e.listInvitations(f.mom, "")).Data, "nothing was made")
	require.Equal(t, http.StatusUnauthorized, e.do(request{method: http.MethodPost, path: "/lists/" + f.house + "/invitations", body: map[string]any{"user_id": f.sueID}}).StatusCode)

	// The admin invites a user whom the admin sees as anyone does, here a user
	// in a shared group.
	e.join(f.admin, e.newGroup(f.admin, "Ada and Sue").ID, f.adminID, f.sueID)
	require.Equal(t, http.StatusCreated, e.postInvitation(f.admin, adminList, map[string]any{"user_id": f.sueID}).StatusCode)
}

func TestAnInvitationIsMadeOnceForAUser(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.invite(f.mom, f.house, f.sueID)

	res := e.postInvitation(f.mom, f.house, map[string]any{"user_id": f.sueID})

	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "already_invited", readProblem(t, res).Code)
	require.Equal(t, 1, decode[invitationList](t, e.listInvitations(f.mom, "")).TotalCount)
}

func TestAnInvitedUserAcceptsAndBecomesARecipientAndTheAnswerIsTheList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	inv := e.invite(f.mom, f.house, f.sueID)

	res := e.acceptInvitation(f.sue, inv.ID)

	require.Equal(t, http.StatusOK, res.StatusCode)
	l := decode[listView](t, res)
	require.Equal(t, f.house, l.ID)
	require.Equal(t, []string{"Mom", "Dad", "Sue"}, summaryNames(l.Recipients))
	require.True(t, l.CanEdit, "a recipient edits")
	require.True(t, l.CanInvite)
	require.True(t, l.CanLeave)
	require.False(t, l.CanDelete)
	require.NotEmpty(t, res.Header.Get("ETag"))
	// The invitation no longer exists, for anyone.
	require.Equal(t, http.StatusNotFound, e.getInvitation(f.sue, inv.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getInvitation(f.mom, inv.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.acceptInvitation(f.sue, inv.ID).StatusCode)
	require.Contains(t, listNames(decode[listList](t, e.listLists(f.sue, "can_edit=true"))), "Our house")
}

func TestAParentAcceptsAndDeclinesForTheirChild(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.join(f.admin, e.newGroup(f.admin, "Emma's friends").ID, f.momID, emmaID)
	e.linkParents(f.admin, emmaID, f.sueID)
	first := e.invite(f.mom, f.house, emmaID)

	for name, token := range map[string]string{"Emma": emma, "her parent": f.sue} {
		got := decode[invitationView](t, e.getInvitation(token, first.ID))
		require.True(t, got.CanAccept, name)
		require.True(t, got.CanDelete, name)
	}
	accepted := e.acceptInvitation(f.sue, first.ID)
	require.Equal(t, http.StatusOK, accepted.StatusCode)
	require.Equal(t, []string{"Mom", "Dad", "Emma"}, summaryNames(decode[listView](t, accepted).Recipients))
	require.Contains(t, listNames(decode[listList](t, e.listLists(emma, "can_edit=true"))), "Our house")

	// A decline by the parent removes the invitation, and Emma stays out.
	other := e.extraList("Skates", f.momID)
	second := e.invite(f.mom, other, emmaID)
	require.Equal(t, http.StatusNoContent, e.deleteInvitation(f.sue, second.ID, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getInvitation(emma, second.ID).StatusCode)
	require.Equal(t, []string{"Mom"}, summaryNames(decode[listView](t, e.getList(f.mom, other)).Recipients))
}

func TestOnlyTheInvitedUserAndTheirParentAccept(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	inv := e.invite(f.mom, f.house, f.sueID)

	// Mom and Dad see the invitation and can withdraw it, and can't accept.
	for name, token := range map[string]string{"the inviter": f.mom, "another recipient": f.dad} {
		res := e.acceptInvitation(token, inv.ID)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "forbidden", readProblem(t, res).Code, name)
	}
	require.Equal(t, []string{"Mom", "Dad"}, summaryNames(decode[listView](t, e.getList(f.mom, f.house)).Recipients))
}

func TestADeactivatedUsersInvitationStaysPendingAndOnlyARecipientWithdrawsIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.join(f.admin, e.newGroup(f.admin, "Emma's friends").ID, f.momID, emmaID)
	e.linkParents(f.admin, emmaID, f.sueID)
	inv := e.invite(f.mom, f.house, emmaID)
	e.deactivated(f.admin, emmaID)

	// The parent still sees it, and can neither accept nor decline.
	got := decode[invitationView](t, e.getInvitation(f.sue, inv.ID))
	require.False(t, got.CanAccept)
	require.False(t, got.CanDelete)
	for name, res := range map[string]*http.Response{
		"accept":  e.acceptInvitation(f.sue, inv.ID),
		"decline": e.deleteInvitation(f.sue, inv.ID, nil),
	} {
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "user_deactivated", readProblem(t, res).Code, name)
	}
	// The user can't sign in at all.
	require.Equal(t, http.StatusUnauthorized, e.getInvitation(emma, inv.ID).StatusCode)
	// A recipient still withdraws it, and still sees it.
	require.True(t, decode[invitationView](t, e.getInvitation(f.mom, inv.ID)).CanDelete)
	require.Equal(t, http.StatusNoContent, e.deleteInvitation(f.mom, inv.ID, nil).StatusCode)
}

func TestARecipientWithdrawsAndTheInvitedUserDeclinesWithoutAConfirmation(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	withdrawn := e.invite(f.mom, f.house, f.sueID)
	declined := e.invite(f.mom, e.extraList("Skates", f.momID), f.sueID)

	require.Equal(t, http.StatusNoContent, e.deleteInvitation(f.dad, withdrawn.ID, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteInvitation(f.sue, declined.ID, nil).StatusCode)

	require.Empty(t, decode[invitationList](t, e.listInvitations(f.mom, "")).Data)
	require.Equal(t, http.StatusNotFound, e.deleteInvitation(f.dad, withdrawn.ID, nil).StatusCode, "a second delete finds nothing")
	// A user who declined can be invited again.
	require.Equal(t, http.StatusCreated, e.postInvitation(f.mom, f.house, map[string]any{"user_id": f.sueID}).StatusCode)
}

func TestAnInvitationDeleteHonorsIfMatch(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	res := e.postInvitation(f.mom, f.house, map[string]any{"user_id": f.sueID})
	inv := decode[invitationView](t, res)

	stale := e.deleteInvitation(f.mom, inv.ID, http.Header{"If-Match": {`"stale"`}})
	require.Equal(t, http.StatusPreconditionFailed, stale.StatusCode)
	require.Equal(t, http.StatusOK, e.getInvitation(f.mom, inv.ID).StatusCode)

	require.Equal(t, http.StatusNoContent, e.deleteInvitation(f.mom, inv.ID, http.Header{"If-Match": {res.Header.Get("ETag")}}).StatusCode)
}

func TestInvitationsFilterBySortAndPage(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	other := e.extraList("Skates", f.momID)
	e.clock.advance(1_000_000_000)
	toSue := e.invite(f.mom, f.house, f.sueID)
	e.clock.advance(1_000_000_000)
	graceID, _ := e.otherUser("Grace")
	e.join(f.admin, e.newGroup(f.admin, "Grace's").ID, graceID, f.momID)
	toGrace := e.invite(f.mom, f.house, graceID)
	e.clock.advance(1_000_000_000)
	skates := e.invite(f.mom, other, f.sueID)

	all := decode[invitationList](t, e.listInvitations(f.mom, ""))
	require.Len(t, all.Data, 3)
	require.Equal(t, []string{toSue.ID, toGrace.ID, skates.ID}, []string{all.Data[0].ID, all.Data[1].ID, all.Data[2].ID}, "oldest first")
	require.Equal(t, []string{"Sue", "Grace"}, invitationUsers(decode[invitationList](t, e.listInvitations(f.mom, "list_id="+f.house))))
	require.Equal(t, []string{"Sue", "Sue"}, invitationUsers(decode[invitationList](t, e.listInvitations(f.mom, "user_id="+f.sueID))))
	require.Equal(t, []string{"Sue"}, invitationUsers(decode[invitationList](t, e.listInvitations(f.mom, "user_id="+f.sueID+"&list_id="+other))))
	require.Equal(t, 3, decode[invitationList](t, e.listInvitations(f.mom, "list_id="+f.house+"&list_id="+other)).TotalCount, "a repeated filter is any of")
	require.Equal(t, 3, decode[invitationList](t, e.listInvitations(f.mom, "user_id="+f.sueID+"&user_id="+graceID)).TotalCount)
	newest := decode[invitationList](t, e.listInvitations(f.mom, "sort=-created_at"))
	require.Equal(t, []string{skates.ID, toGrace.ID, toSue.ID}, []string{newest.Data[0].ID, newest.Data[1].ID, newest.Data[2].ID})

	first := decode[invitationList](t, e.listInvitations(f.mom, "limit=2"))
	require.Len(t, first.Data, 2)
	require.NotNil(t, first.NextCursor)
	require.Equal(t, 3, first.TotalCount)
	next := decode[invitationList](t, e.listInvitations(f.mom, "limit=2&cursor="+*first.NextCursor))
	require.Equal(t, []string{skates.ID}, []string{next.Data[0].ID})
	require.Nil(t, next.NextCursor)

	// A filter by a list or a user the caller can't see finds nothing.
	require.Empty(t, decode[invitationList](t, e.listInvitations(f.hedy, "list_id="+f.house)).Data)
	for name, query := range map[string]string{"unknown filter": "nope=1", "unknown sort": "sort=name"} {
		res := e.listInvitations(f.mom, query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
	}
	require.Equal(t, "unknown_filter", readProblem(t, e.listInvitations(f.mom, "nope=1")).Code)
	require.Equal(t, "unknown_sort", readProblem(t, e.listInvitations(f.mom, "sort=name")).Code)
	require.Equal(t, "cursor_mismatch", readProblem(t, e.listInvitations(f.mom, "sort=-created_at&cursor="+*first.NextCursor)).Code)
}

func TestTheInvitedUsersAvatarLoadsForTheListsRecipientsWhoCantSeeThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	img := e.uploaded(f.sue)
	require.Equal(t, http.StatusOK, e.setAvatar(f.sue, f.sueID, img.ID).StatusCode)
	// Dad can't see Sue, and Sue sees the house through Mom, so her avatar already
	// loads for Sue. It doesn't load for Dad until the invitation reaches him.
	require.Equal(t, http.StatusNotFound, e.getUser(f.dad, f.sueID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.dad, img.ID).StatusCode)

	e.invite(f.mom, f.house, f.sueID)

	require.Equal(t, http.StatusOK, e.getImage(f.dad, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.hedy, img.ID).StatusCode)
	got := decode[invitationList](t, e.listInvitations(f.dad, ""))
	require.NotNil(t, got.Data[0].User.Avatar)
}

func TestInvitableUsersAreTheOnesTheCallerCanInviteToTheList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	goneID, _ := e.otherUser("Gone")
	pendingID, _ := e.otherUser("Pending")
	e.join(f.admin, e.newGroup(f.admin, "Friends").ID, f.momID, pendingID)
	e.linkParents(f.admin, goneID, f.momID) // only a parent still sees a deactivated user
	e.deactivated(f.admin, goneID)
	e.invite(f.mom, f.house, pendingID)

	// Mom sees Sue, Gone (deactivated), and Pending. Dad and Mom are recipients,
	// Pending has an invitation, and Gone is deactivated.
	require.Equal(t, []string{"Sue"}, names(decode[userList](t, e.listUsers(f.mom, "invitable_to="+f.house))))
	require.Equal(t, 1, decode[userList](t, e.listUsers(f.mom, "invitable_to="+f.house)).TotalCount)
	// It is empty when the caller can't invite to the list.
	for name, c := range map[string]struct{ token, list string }{
		"a primary list":        {f.mom, f.momList},
		"a giver":               {f.sue, f.house},
		"a list they can't see": {f.hedy, f.house},
		"a list that isn't":     {f.mom, "9999"},
		"not an ID":             {f.mom, "abc"},
	} {
		got := decode[userList](t, e.listUsers(c.token, "invitable_to="+c.list))
		require.Empty(t, got.Data, name)
		require.Zero(t, got.TotalCount, name)
	}
	// A declined invitation makes the user invitable again.
	pending := decode[invitationList](t, e.listInvitations(f.mom, "user_id="+pendingID)).Data[0]
	e.deleteInvitation(f.mom, pending.ID, nil)
	require.Equal(t, []string{"Pending", "Sue"}, names(decode[userList](t, e.listUsers(f.mom, "invitable_to="+f.house))))
	// It combines with the other filters and with sorting and paging.
	require.Equal(t, []string{"Sue"}, names(decode[userList](t, e.listUsers(f.mom, "invitable_to="+f.house+"&limit=1&sort=-name"))))
}

func TestTheAdminsInvitableUsersLeaveOutWhoOnlyTheAdminSees(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	adminList := decode[listView](t, e.postList(f.admin, map[string]any{"name": "Ada's"})).ID
	e.join(f.admin, e.newGroup(f.admin, "Ada and Sue").ID, f.adminID, f.sueID)

	got := names(decode[userList](t, e.listUsers(f.admin, "invitable_to="+adminList)))

	require.Equal(t, []string{"Sue"}, got, "the admin's GET /users has every user, and the filter keeps the ones POST would take")
	require.Greater(t, len(decode[userList](t, e.listUsers(f.admin, "")).Data), 1)
}

func TestInvitableToCombinesWithTheGroupFilter(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	graceID, _ := e.otherUser("Grace")
	group := e.newGroup(f.admin, "Grace's")
	e.join(f.admin, group.ID, graceID, f.momID)

	both := names(decode[userList](t, e.listUsers(f.mom, "invitable_to="+f.house+"&group_id="+group.ID)))

	require.Equal(t, []string{"Grace"}, both)
}
