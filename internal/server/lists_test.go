package server

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/user"
)

// listView is a full list as the API shows it, in the fields these tests read.
type listView struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description *string       `json:"description"`
	IsPrimary   bool          `json:"is_primary"`
	Recipients  []summaryView `json:"recipients"`
	ItemCount   int           `json:"item_count"`
	CreatedAt   string        `json:"created_at"`
	Version     string        `json:"version"`

	CanEdit   bool `json:"can_edit"`
	CanDelete bool `json:"can_delete"`
	CanInvite bool `json:"can_invite"`
	CanLeave  bool `json:"can_leave"`
}

type listList struct {
	Data       []listView `json:"data"`
	NextCursor *string    `json:"next_cursor"`
	TotalCount int        `json:"total_count"`
}

func (e *env) listLists(token, query string) *http.Response {
	e.t.Helper()
	path := "/lists"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) getList(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/lists/" + id, header: cookie(token)})
}

func (e *env) patchList(token, id string, body any, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodPatch, path: "/lists/" + id, body: body, header: h})
}

// primaryListOf is the primary list of the user whose session is token, as that
// user's own GET /lists shows it.
func (e *env) primaryListOf(token string) listView {
	e.t.Helper()
	list := decode[listList](e.t, e.listLists(token, "is_primary=true"))
	require.Len(e.t, list.Data, 1, "a user has exactly one primary list")
	return list.Data[0]
}

// signInAs gives the admin's new user a session through a login link.
func (e *env) signInAs(admin, userID string) string {
	e.t.Helper()
	res := e.redeem(e.makeLink(admin, userID), "", nil)
	require.Equal(e.t, http.StatusOK, res.StatusCode)
	c := sessionCookie(res)
	require.NotNil(e.t, c)
	return c.Value
}

// extraList adds an extra list straight to the database, with the given users
// as its recipients, and returns its ID. There is no route that makes one yet.
func (e *env) extraList(name string, recipientIDs ...string) string {
	e.t.Helper()
	res, err := e.db.ExecContext(e.t.Context(), `INSERT INTO lists (name, created_at) VALUES (?, ?)`, name, e.clock.now())
	require.NoError(e.t, err)
	id, err := res.LastInsertId()
	require.NoError(e.t, err)
	for _, r := range recipientIDs {
		uid, err := strconv.ParseInt(r, 10, 64)
		require.NoError(e.t, err)
		_, err = e.db.ExecContext(e.t.Context(), `INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (?, ?, ?)`, id, uid, e.clock.now())
		require.NoError(e.t, err)
	}
	return strconv.FormatInt(id, 10)
}

func listNames(l listList) []string {
	out := make([]string, len(l.Data))
	for i, v := range l.Data {
		out[i] = v.Name
	}
	return out
}

func TestSetupMakesTheAdminsPrimaryListAndTheUserSeesItOnTheirProfile(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()

	list := decode[listList](t, e.listLists(admin, ""))

	require.Equal(t, 1, list.TotalCount)
	require.Len(t, list.Data, 1)
	l := list.Data[0]
	require.Equal(t, "Ada", l.Name, "the list is named after its user")
	require.True(t, l.IsPrimary)
	require.Nil(t, l.Description)
	require.Equal(t, 0, l.ItemCount)
	require.Len(t, l.Recipients, 1)
	require.Equal(t, summaryView{ID: adminID, Name: "Ada"}, l.Recipients[0])
	require.True(t, l.CanEdit)
	require.False(t, l.CanDelete, "nobody deletes a primary list")
	require.False(t, l.CanInvite, "nobody invites to a primary list")
	require.False(t, l.CanLeave)

	me := decode[map[string]any](t, e.do(request{path: "/users/me", header: cookie(admin)}))
	require.Equal(t, map[string]any{"id": l.ID, "name": "Ada"}, me["primary_list"])
}

func TestAUserTheAdminCreatesGetsAPrimaryListToo(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	made := decode[userView](t, e.createUser(admin, map[string]any{"name": "Grace", "birthday": birthday(1, 1, nil)}))
	grace := e.signInAs(admin, made.ID)

	l := e.primaryListOf(grace)

	require.Equal(t, "Grace", l.Name)
	require.Equal(t, []summaryView{{ID: made.ID, Name: "Grace"}}, l.Recipients)
}

func TestTheAdminDoesNotSeeAnotherUsersPrimaryListOrItsAddressOnTheUser(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	graceList := e.primaryListOf(grace)

	require.Equal(t, http.StatusNotFound, e.getList(admin, graceList.ID).StatusCode, "the admin's access to every user doesn't reach lists")
	require.Equal(t, []string{"Ada"}, listNames(decode[listList](t, e.listLists(admin, ""))))
	body := decode[map[string]any](t, e.getUser(admin, graceID))
	require.NotContains(t, body, "primary_list", "the key is absent for a caller who can't see the list")
}

func TestAListThatDoesNotExistOrIsHiddenIsTheSame404(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	_, grace := e.otherUser("Grace")
	graceList := e.primaryListOf(grace)

	for name, id := range map[string]string{"hidden": graceList.ID, "missing": "9999", "not an id": "abc", "zero": "0"} {
		res := e.getList(admin, id)
		require.Equal(t, http.StatusNotFound, res.StatusCode, name)
		require.Equal(t, "not_found", readProblem(t, res).Code, name)
	}
}

func TestAListNeedsASession(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/lists"}).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/lists/1"}).StatusCode)
}

func TestAPrimaryListsNameFollowsItsUsersNameUntilARecipientRenamesTheList(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	list := e.primaryListOf(grace)

	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"name": "Gracie"}, nil).StatusCode)
	require.Equal(t, "Gracie", decode[listView](t, e.getList(grace, list.ID)).Name, "the list follows the rename")
	me := decode[map[string]any](t, e.do(request{path: "/users/me", header: cookie(grace)}))
	require.Equal(t, "Gracie", me["primary_list"].(map[string]any)["name"])

	// Editing the description, or saving the name it already has, is no rename.
	require.Equal(t, http.StatusOK, e.patchList(grace, list.ID, map[string]any{"name": "Gracie", "description": "Hi"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.patchUser(admin, graceID, map[string]any{"name": "Grace"}, nil).StatusCode)
	require.Equal(t, "Grace", decode[listView](t, e.getList(grace, list.ID)).Name, "still follows")

	// A rename of the list ends the following.
	renamed := e.patchList(grace, list.ID, map[string]any{"name": "Grace's wishes"}, nil)
	require.Equal(t, http.StatusOK, renamed.StatusCode)
	require.Equal(t, "Grace's wishes", decode[listView](t, renamed).Name)
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"name": "Grace H"}, nil).StatusCode)
	require.Equal(t, "Grace's wishes", decode[listView](t, e.getList(grace, list.ID)).Name, "the list keeps its own name")
}

func TestARecipientEditsTheNameAndTheDescriptionOfTheirList(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	list := e.primaryListOf(grace)

	res := e.patchList(grace, list.ID, map[string]any{"name": "  Wishes  ", "description": "We **love** books."}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, res.Header.Get("ETag"), e.getList(grace, list.ID).Header.Get("ETag"))
	got := decode[listView](t, res)
	require.Equal(t, "Wishes", got.Name, "the name is trimmed")
	require.Equal(t, "We **love** books.", *got.Description)

	cleared := decode[listView](t, e.patchList(grace, list.ID, map[string]any{"description": nil}, nil))
	require.Nil(t, cleared.Description)
	require.Equal(t, "Wishes", cleared.Name, "an absent field doesn't change")
	empty := decode[listView](t, e.patchList(grace, list.ID, map[string]any{"description": "   "}, nil))
	require.Nil(t, empty.Description, "an empty description is no description")
}

func TestAListsEditsAreCheckedAndFieldsTheCallerCannotChangeAreRefused(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	_, grace := e.otherUser("Grace")
	list := e.primaryListOf(grace)

	for name, tc := range map[string]struct {
		body map[string]any
		code string
		want int
	}{
		"null name":       {map[string]any{"name": nil}, "invalid_input", http.StatusUnprocessableEntity},
		"empty name":      {map[string]any{"name": "  "}, "invalid_input", http.StatusUnprocessableEntity},
		"long name":       {map[string]any{"name": string(make([]byte, 101))}, "invalid_input", http.StatusUnprocessableEntity},
		"long note":       {map[string]any{"description": string(make([]byte, 2001))}, "invalid_input", http.StatusUnprocessableEntity},
		"no such field":   {map[string]any{"shade": "red"}, "invalid_input", http.StatusUnprocessableEntity},
		"read-only field": {map[string]any{"is_primary": false}, "field_not_writable", http.StatusForbidden},
		"recipients":      {map[string]any{"recipients": []string{}}, "field_not_writable", http.StatusForbidden},
		"item count":      {map[string]any{"item_count": 3}, "field_not_writable", http.StatusForbidden},
	} {
		res := e.patchList(grace, list.ID, tc.body, nil)
		require.Equal(t, tc.want, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	// A caller who can't see the list can't tell it exists.
	require.Equal(t, http.StatusNotFound, e.patchList(admin, list.ID, map[string]any{"name": "x"}, nil).StatusCode)
	require.Equal(t, "Grace", decode[listView](t, e.getList(grace, list.ID)).Name, "nothing changed")
}

func TestAListsETagGuardsItsEdits(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	list := e.primaryListOf(grace)
	before := e.getList(grace, list.ID).Header.Get("ETag")
	require.Equal(t, before, list.Version, "a row of the collection has the same version")

	stale := e.patchList(grace, list.ID, map[string]any{"name": "x"}, http.Header{"If-Match": {`"nope"`}})
	require.Equal(t, http.StatusPreconditionFailed, stale.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, stale).Code)
	require.Equal(t, "Grace", decode[listView](t, e.getList(grace, list.ID)).Name)

	ok := e.patchList(grace, list.ID, map[string]any{"name": "x"}, http.Header{"If-Match": {before}})
	require.Equal(t, http.StatusOK, ok.StatusCode)
	require.NotEqual(t, before, ok.Header.Get("ETag"))
}

func TestTheListsOfACallerFilterBySortAndPage(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, _ := e.otherUser("Hedy")
	e.extraList("Cousins", graceID, hedyID)
	e.extraList("Attic", graceID)

	all := decode[listList](t, e.listLists(grace, ""))
	require.Equal(t, []string{"Attic", "Cousins", "Grace"}, listNames(all), "by name, the default")
	require.Equal(t, 3, all.TotalCount)
	require.Equal(t, []string{"Grace", "Cousins", "Attic"}, listNames(decode[listList](t, e.listLists(grace, "sort=-name"))))
	// created_at: Grace's list was made with her, before the others.
	require.Equal(t, "Grace", listNames(decode[listList](t, e.listLists(grace, "sort=created_at")))[0])

	require.Equal(t, []string{"Grace"}, listNames(decode[listList](t, e.listLists(grace, "is_primary=true"))))
	require.Equal(t, []string{"Attic", "Cousins"}, listNames(decode[listList](t, e.listLists(grace, "is_primary=false"))))
	require.Equal(t, []string{"Cousins"}, listNames(decode[listList](t, e.listLists(grace, "recipient_id="+hedyID))),
		"the lists that have this user among their recipients")
	require.Equal(t, []string{"Attic", "Cousins", "Grace"}, listNames(decode[listList](t, e.listLists(grace, "recipient_id="+graceID))))
	// A repeated parameter means any of; an ID that names nobody matches nothing.
	require.Equal(t, []string{"Cousins"}, listNames(decode[listList](t, e.listLists(grace, "recipient_id="+hedyID+"&recipient_id=9999"))))
	require.Equal(t, []string{"Attic", "Cousins", "Grace"}, listNames(decode[listList](t, e.listLists(grace, "recipient_id="+hedyID+"&recipient_id="+graceID))))
	require.Empty(t, decode[listList](t, e.listLists(grace, "recipient_id="+hedyID+"&is_primary=true")).Data, "Hedy's primary list is hers alone")
}

func TestAListThatTheCallerCanEditFiltersByCanEdit(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, grace := e.otherUser("Grace")
	e.extraList("Attic", graceID)

	require.Equal(t, []string{"Attic", "Grace"}, listNames(decode[listList](t, e.listLists(grace, "can_edit=true"))))
	require.Empty(t, decode[listList](t, e.listLists(grace, "can_edit=false")).Data)
}

func TestListsPageWithACursorAndRefuseWhatTheyDoNotKnow(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, grace := e.otherUser("Grace")
	for _, n := range []string{"B", "C", "D"} {
		e.extraList(n, graceID)
	}

	first := decode[listList](t, e.listLists(grace, "limit=2"))
	require.Equal(t, []string{"B", "C"}, listNames(first))
	require.NotNil(t, first.NextCursor)
	require.Equal(t, 4, first.TotalCount)
	second := decode[listList](t, e.listLists(grace, "limit=2&cursor="+*first.NextCursor))
	require.Equal(t, []string{"D", "Grace"}, listNames(second))
	require.Nil(t, second.NextCursor)

	for _, tc := range []struct{ query, code string }{
		{"shade=red", "unknown_filter"},
		{"sort=price", "unknown_sort"},
		{"sort=created_at&cursor=" + *first.NextCursor, "cursor_mismatch"},
		{"can_edit=maybe", "invalid_input"},
	} {
		res := e.listLists(grace, tc.query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, tc.query)
		require.Equal(t, tc.code, readProblem(t, res).Code, tc.query)
	}
}

// The primary list belongs to user.Create, so a path that calls it without the
// service (setup does) can't skip the list.
func TestEveryPathThatMakesAUserMakesTheirPrimaryList(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, err := user.Create(t.Context(), e.db, e.clock.now(), user.NewUser{Name: "Direct", Birthday: user.PartialDate{Month: 1, Day: 1}})
	require.NoError(t, err)
	var n int
	require.NoError(t, e.db.GetContext(t.Context(), &n, `SELECT count(*) FROM lists l JOIN list_recipients r ON r.list_id = l.id WHERE l.name = 'Direct' AND r.user_id = ?`, id))
	require.Equal(t, 1, n)
}
