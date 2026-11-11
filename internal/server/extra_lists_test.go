package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover extra lists: creating one for yourself or a child, deleting
// one, and moving an item between lists (docs/spec.md, Lists, Extra lists and
// Leaving and deleting; Items, Moving an item).

func (e *env) postList(token string, body map[string]any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/lists", body: body, header: cookie(token)})
}

func (e *env) deleteList(token, id string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodDelete, path: "/lists/" + id, header: h})
}

func TestAUserCreatesAnExtraListForThemselvesByDefault(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")

	res := e.postList(grace, map[string]any{"name": "  Garden  ", "description": "Spring planting"})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	l := decode[listView](t, res)
	require.Equal(t, "/api/v2/lists/"+l.ID, res.Header.Get("Location"))
	require.NotEmpty(t, res.Header.Get("ETag"))
	require.Equal(t, "Garden", l.Name)
	require.Equal(t, "Spring planting", *l.Description)
	require.False(t, l.IsPrimary)
	require.Equal(t, []summaryView{{ID: graceID, Name: "Grace"}}, l.Recipients)
	require.Zero(t, l.ItemCount)
	require.True(t, l.CanEdit)
	require.True(t, l.CanDelete)
	require.True(t, l.CanInvite)
	require.False(t, l.CanLeave)
	require.Equal(t, []string{"Garden", "Grace"}, listNames(decode[listList](t, e.listLists(grace, "can_edit=true"))))
	// Nobody else sees it, and the primary list stays the only primary list.
	require.Equal(t, http.StatusNotFound, e.getList(admin, l.ID).StatusCode)
	require.Equal(t, "Grace", e.primaryListOf(grace).Name)
}

func TestAnExtraListNeedsANameAndTakesAnOptionalDescription(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")

	for name, body := range map[string]map[string]any{
		"no name":    {},
		"blank name": {"name": "   "},
		"long name":  {"name": strings.Repeat("a", 101)},
		"huge text":  {"name": "A", "description": strings.Repeat("a", 2001)},
	} {
		res := e.postList(grace, body)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, name)
	}

	plain := decode[listView](t, e.postList(grace, map[string]any{"name": "Plain", "description": "  "}))
	require.Nil(t, plain.Description, "a blank description is none")
	require.Equal(t, 1, decode[listList](t, e.listLists(grace, "is_primary=true")).TotalCount)
}

func TestAParentCreatesAnExtraListForTheirChildAndIsNotItsRecipient(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)

	res := e.postList(f.mom, map[string]any{"name": "Cousins", "recipient_id": emmaID})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	l := decode[listView](t, res)
	require.Equal(t, []summaryView{{ID: emmaID, Name: "Emma"}}, l.Recipients, "the child is the first recipient, and the parent is not")
	require.True(t, l.CanEdit, "the parent edits through the child")
	require.True(t, l.CanDelete, "and deletes it")
	require.False(t, l.CanInvite, "inviting is for recipients")
	require.Equal(t, http.StatusOK, e.getList(emma, l.ID).StatusCode)
	require.Contains(t, listNames(decode[listList](t, e.listLists(emma, "can_edit=true"))), "Cousins")
	// Naming oneself is the default.
	own := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Mine", "recipient_id": f.momID}))
	require.Equal(t, []summaryView{{ID: f.momID, Name: "Mom"}}, own.Recipients)
}

func TestAListIsNotCreatedForAUserTheCallerIsNotTheParentOf(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	// Sue shares a group with Mom, so Mom sees her and can't create for her.
	for name, tc := range map[string]struct {
		token, recipient string
		code             string
		status           int
	}{
		"a user the caller sees":        {f.mom, f.sueID, "reference_not_allowed", http.StatusForbidden},
		"a child the caller can't see":  {f.dad, emmaID, "reference_not_found", http.StatusUnprocessableEntity},
		"a user in no shared group":     {f.hedy, f.momID, "reference_not_found", http.StatusUnprocessableEntity},
		"a user that doesn't exist":     {f.mom, "9999", "reference_not_found", http.StatusUnprocessableEntity},
		"not an ID":                     {f.mom, "abc", "reference_not_found", http.StatusUnprocessableEntity},
		"the admin, who isn't a parent": {f.admin, emmaID, "reference_not_allowed", http.StatusForbidden},
	} {
		res := e.postList(tc.token, map[string]any{"name": "Nope", "recipient_id": tc.recipient})
		require.Equal(t, tc.status, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	require.NotContains(t, listNames(decode[listList](t, e.listLists(f.sue, ""))), "Nope")
}

func TestAListIsNotCreatedForADeactivatedChild(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	e.deactivated(f.admin, emmaID)

	res := e.postList(f.mom, map[string]any{"name": "Cousins", "recipient_id": emmaID})

	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "user_deactivated", readProblem(t, res).Code)
}

func TestARecipientDeletesTheirExtraListAndItsItems(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, primary := e.grace()
	garden := decode[listView](t, e.postList(token, map[string]any{"name": "Garden"})).ID
	a, b := e.uploaded(token), e.uploaded(token)
	rake := e.addItem(token, garden, "Rake", map[string]any{"image_ids": imageIDs(a, b)})
	e.addItem(token, garden, "Hose")
	e.addItem(token, primary, "Kettle")
	require.Zero(t, e.sweep(), "held images are not swept")

	res := e.deleteList(token, garden, nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, http.StatusNotFound, e.getList(token, garden).StatusCode)
	require.Equal(t, http.StatusNotFound, e.listItems(token, garden, "").StatusCode)
	require.Equal(t, http.StatusNotFound, e.getItem(token, rake.ID).StatusCode)
	require.Equal(t, []string{"Kettle"}, e.order(token, primary), "the other lists are untouched")
	require.Equal(t, 2, e.sweep(), "the items' images go to the sweep, as an item delete does")
	require.Equal(t, http.StatusNotFound, e.deleteList(token, garden, nil).StatusCode, "a second delete finds nothing")
}

func TestAParentDeletesTheirChildsExtraList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	cousins := e.extraList("Cousins", emmaID)
	e.addItem(f.mom, cousins, "Skates")

	res := e.deleteList(f.mom, cousins, nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, http.StatusNotFound, e.getList(emma, cousins).StatusCode)
}

func TestAListThatIsNotAnExtraListWithOneRecipientIsNotDeleted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addItem(f.mom, f.house, "Grill")

	primary := e.deleteList(f.mom, f.momList, nil)
	require.Equal(t, http.StatusForbidden, primary.StatusCode)
	require.Equal(t, "primary_list", readProblem(t, primary).Code)

	shared := e.deleteList(f.mom, f.house, nil)
	require.Equal(t, http.StatusForbidden, shared.StatusCode)
	require.Equal(t, "list_has_other_recipients", readProblem(t, shared).Code)

	require.Equal(t, []string{"Grill"}, e.order(f.mom, f.house))
	require.Equal(t, http.StatusOK, e.getList(f.mom, f.momList).StatusCode)
}

func TestAGiverAndAStrangerCannotDeleteAList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	garden := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Garden"})).ID

	// Sue shares a group with Mom: she sees the list and can't delete it, so the
	// answer says forbidden whatever kind of list it is.
	for name, id := range map[string]string{"extra": garden, "primary": f.momList} {
		res := e.deleteList(f.sue, id, nil)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "forbidden", readProblem(t, res).Code, name)
	}
	require.Equal(t, http.StatusNotFound, e.deleteList(f.hedy, garden, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.deleteList(f.mom, "abc", nil).StatusCode)
	require.Equal(t, http.StatusOK, e.getList(f.mom, garden).StatusCode)
}

func TestADeleteHonorsIfMatch(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, _ := e.grace()
	res := e.postList(token, map[string]any{"name": "Garden"})
	l := decode[listView](t, res)

	stale := e.deleteList(token, l.ID, http.Header{"If-Match": {`"stale"`}})
	require.Equal(t, http.StatusPreconditionFailed, stale.StatusCode)
	require.Equal(t, http.StatusOK, e.getList(token, l.ID).StatusCode)

	require.Equal(t, http.StatusNoContent, e.deleteList(token, l.ID, http.Header{"If-Match": {res.Header.Get("ETag")}}).StatusCode)
}

func TestAGrandparentCannotEditAGrandchildsList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	// Mom is Emma's parent, and Dad is Mom's parent: Emma's grandparent. A group
	// lets Dad see Emma's lists, so the answer is 403 and not 404.
	e.linkParents(f.admin, emmaID, f.momID)
	e.linkParents(f.admin, f.momID, f.dadID)
	e.join(f.admin, e.newGroup(f.admin, "Reunion").ID, emmaID, f.dadID)
	emmaList := e.primaryListFor(emma, emmaID)
	cousins := e.extraList("Cousins", emmaID)
	bike := e.addItem(emma, emmaList, "Bike")

	require.False(t, decode[listView](t, e.getList(f.dad, emmaList)).CanEdit)
	require.False(t, decode[itemView](t, e.getItem(f.dad, bike.ID)).CanEdit)
	require.NotContains(t, listNames(decode[listList](t, e.listLists(f.dad, "can_edit=true"))), "Emma")
	require.Equal(t, http.StatusForbidden, e.postItem(f.dad, emmaList, map[string]any{"title": "Helmet"}).StatusCode)
	require.Equal(t, http.StatusForbidden, e.patchItem(f.dad, bike.ID, map[string]any{"title": "x"}, nil).StatusCode)
	require.Equal(t, http.StatusForbidden, e.patchList(f.dad, cousins, map[string]any{"name": "x"}, nil).StatusCode)
	require.Equal(t, http.StatusForbidden, e.deleteList(f.dad, cousins, nil).StatusCode)
	require.Equal(t, http.StatusForbidden, e.deleteItem(f.dad, bike.ID, nil).StatusCode)
	// Nor can he move an item out of it into his own list.
	moved := e.patchItem(f.dad, bike.ID, map[string]any{"list_id": f.dadList}, nil)
	require.Equal(t, http.StatusForbidden, moved.StatusCode)
	// Mom, Emma's parent, edits Emma's lists. She sees her own parent Dad's list
	// and can't edit it, so she can't move an item there.
	denied := e.patchItem(f.mom, bike.ID, map[string]any{"list_id": f.dadList}, nil)
	require.Equal(t, http.StatusForbidden, denied.StatusCode)
	require.Equal(t, "cannot_edit_target_list", readProblem(t, denied).Code)
}
