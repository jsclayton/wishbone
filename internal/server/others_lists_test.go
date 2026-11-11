package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover who sees and who edits a list (docs/spec.md, Visibility,
// Who can see a list; Lists, Who edits a list): a user sees a list when they are
// a recipient or can see one, and the admin's access to every user doesn't count.

// family builds Mom, Dad and Sue (who share the group "Cousins" with Mom only),
// and the admin Ada, who is in no group. "Our house" has Mom and Dad as recipients.
type family struct {
	admin, mom, dad, sue, hedy         string // session tokens
	adminID                            string
	momID, dadID, sueID, hedyID        string
	momList, dadList, house, hedysList string
}

func (e *env) family() family {
	e.t.Helper()
	adminID, admin := e.signUp()
	f := family{admin: admin, adminID: adminID}
	f.momID, f.mom = e.otherUser("Mom")
	f.dadID, f.dad = e.otherUser("Dad")
	f.sueID, f.sue = e.otherUser("Sue")
	f.hedyID, f.hedy = e.otherUser("Hedy")
	e.join(admin, e.newGroup(admin, "Cousins").ID, f.momID, f.sueID)
	f.momList = e.primaryListFor(f.mom, f.momID)
	f.dadList = e.primaryListFor(f.dad, f.dadID)
	f.hedysList = e.primaryListFor(f.hedy, f.hedyID)
	f.house = e.extraList("Our house", f.momID, f.dadID)
	return f
}

func TestAUserSeesTheListsOfTheUsersTheySeeAndNotTheAdminsAccess(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")

	// Sue shares a group with Mom: she sees Mom's list and its items, as a giver.
	got := decode[listView](t, e.getList(f.sue, f.momList))
	require.Equal(t, "Mom", got.Name)
	require.False(t, got.CanEdit, "seeing a list is not editing it")
	require.False(t, got.CanDelete)
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, []string{"Kettle"}, e.order(f.sue, f.momList))
	require.Equal(t, http.StatusOK, e.getItem(f.sue, kettle.ID).StatusCode)
	require.False(t, decode[itemView](t, e.getItem(f.sue, kettle.ID)).CanEdit)

	// Sue's collection: her own list and Mom's, and the total follows. She
	// doesn't see Dad's or Hedy's lists, and "Our house" comes through Mom.
	all := decode[listList](t, e.listLists(f.sue, ""))
	require.Equal(t, []string{"Mom", "Our house", "Sue"}, listNames(all))
	require.Equal(t, 3, all.TotalCount)
	require.Equal(t, []string{"Sue"}, listNames(decode[listList](t, e.listLists(f.sue, "can_edit=true"))))
	require.Equal(t, []string{"Mom", "Our house"}, listNames(decode[listList](t, e.listLists(f.sue, "can_edit=false"))))
	require.Equal(t, []string{"Mom"}, listNames(decode[listList](t, e.listLists(f.sue, "recipient_id="+f.momID+"&is_primary=true"))))

	// A user outside every group and link sees only their own.
	for name, id := range map[string]string{"Mom's list": f.momList, "Dad's list": f.dadList, "Our house": f.house} {
		require.Equal(t, http.StatusNotFound, e.getList(f.hedy, id).StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.listItems(f.hedy, id, "").StatusCode, name)
	}
	require.Equal(t, http.StatusNotFound, e.getItem(f.hedy, kettle.ID).StatusCode)
	require.Equal(t, []string{"Hedy"}, listNames(decode[listList](t, e.listLists(f.hedy, ""))))
	require.Equal(t, 1, decode[listList](t, e.listLists(f.hedy, "")).TotalCount)

	// The admin sees every user, and no list through that.
	require.Equal(t, http.StatusOK, e.getUser(f.admin, f.momID).StatusCode)
	for name, id := range map[string]string{"Mom's list": f.momList, "Our house": f.house} {
		require.Equal(t, http.StatusNotFound, e.getList(f.admin, id).StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.listItems(f.admin, id, "").StatusCode, name)
	}
	require.Equal(t, http.StatusNotFound, e.getItem(f.admin, kettle.ID).StatusCode)
	require.Equal(t, []string{"Ada"}, listNames(decode[listList](t, e.listLists(f.admin, ""))))
	require.Empty(t, decode[listList](t, e.listLists(f.admin, "recipient_id="+f.momID)).Data)
}

func TestAUsersPrimaryListShowsOnTheUserOnlyToWhoSeesTheList(t *testing.T) {
	e := newEnv(t)
	f := e.family()

	asSue := decode[map[string]any](t, e.getUser(f.sue, f.momID))
	require.Equal(t, map[string]any{"id": f.momList, "name": "Mom"}, asSue["primary_list"])
	asAdmin := decode[map[string]any](t, e.getUser(f.admin, f.momID))
	require.NotContains(t, asAdmin, "primary_list", "the admin's access doesn't reach the list")

	// The collection follows the same rule.
	for _, row := range decode[struct {
		Data []map[string]any `json:"data"`
	}](t, e.do(request{path: "/users", header: cookie(f.admin)})).Data {
		if row["id"] == f.adminID {
			continue
		}
		require.NotContains(t, row, "primary_list", row["name"])
	}
	for _, row := range decode[struct {
		Data []map[string]any `json:"data"`
	}](t, e.do(request{path: "/users", header: cookie(f.sue)})).Data {
		require.Contains(t, row, "primary_list", row["name"])
	}
}

func TestAListWithSeveralRecipientsIsSeenThroughAnyOneOfThemAndTheOtherIsASummary(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	img := e.uploaded(f.dad)
	require.Equal(t, http.StatusOK, e.setAvatar(f.dad, f.dadID, img.ID).StatusCode)

	// Sue sees "Our house" through Mom, and Dad is a summary she can't open.
	house := decode[listView](t, e.getList(f.sue, f.house))
	require.Equal(t, []string{"Mom", "Dad"}, summaryNames(house.Recipients))
	require.Equal(t, http.StatusNotFound, e.getUser(f.sue, f.dadID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getList(f.sue, f.dadList).StatusCode, "Dad's own list needs Dad")

	// The avatar in that summary loads for everyone who sees the list, and for no one else.
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.hedy, img.ID).StatusCode)
}

func TestTheAvatarOfWhoAddedAnItemLoadsForWhoSeesTheItem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	// Sue sees "Our house" through Mom. Grandma is Dad's parent: Sue can't see
	// her, but she added an item, so her summary reaches Sue as added_by.
	grandmaID, grandma := e.otherUser("Grandma")
	e.linkParents(f.admin, f.dadID, grandmaID)
	img := e.uploaded(grandma)
	require.Equal(t, http.StatusOK, e.setAvatar(grandma, grandmaID, img.ID).StatusCode)
	it := e.addItem(grandma, f.house, "Rug")

	got := decode[itemView](t, e.getItem(f.sue, it.ID))
	require.Equal(t, grandmaID, got.AddedBy.ID)
	require.Equal(t, http.StatusNotFound, e.getUser(f.sue, grandmaID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.hedy, img.ID).StatusCode)
}

func TestAParentSeesAndEditsEveryListOfTheirChildAndAddedByNamesThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID, f.dadID)
	emmaList := e.primaryListFor(emma, emmaID)
	cousins := e.extraList("Cousins", emmaID)
	own := e.addItem(emma, emmaList, "Bike")

	// A parent sees the child's lists and can edit them.
	l := decode[listView](t, e.getList(f.mom, emmaList))
	require.True(t, l.CanEdit)
	editable := listNames(decode[listList](t, e.listLists(f.mom, "can_edit=true")))
	require.Equal(t, []string{"Cousins", "Emma", "Mom", "Our house"}, editable)
	require.True(t, decode[itemView](t, e.getItem(f.mom, own.ID)).CanEdit)

	// The parent adds, edits, reorders and deletes as a recipient does.
	added := e.postItem(f.mom, emmaList, map[string]any{"title": "Helmet"})
	require.Equal(t, http.StatusCreated, added.StatusCode)
	helmet := decode[itemView](t, added)
	require.Equal(t, f.momID, helmet.AddedBy.ID, "added_by names the parent")
	require.Equal(t, "Mom", helmet.AddedBy.Name)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, helmet.ID, map[string]any{"title": "Red helmet"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, helmet.ID, map[string]any{"position": 0}, nil).StatusCode)
	require.Equal(t, []string{"Red helmet", "Bike"}, e.order(emma, emmaList))
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, own.ID, map[string]any{"title": "Blue bike"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.patchList(f.mom, cousins, map[string]any{"name": "Cousins!", "description": "Fun"}, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, helmet.ID, nil).StatusCode)
	require.Equal(t, []string{"Blue bike"}, e.order(emma, emmaList))

	// The second parent can edit too. The child's primary list renames the same.
	require.Equal(t, http.StatusCreated, e.postItem(f.dad, emmaList, map[string]any{"title": "Skates"}).StatusCode)
}

func TestAParentMovesAnItemBetweenListsTheyCanEditAndNotToOneTheyOnlySee(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	emmaList := e.primaryListFor(emma, emmaID)
	bike := e.addItem(emma, emmaList, "Bike")
	// Mom sees Sue's list through the group, and can't edit it.
	sueList := e.primaryListFor(f.sue, f.sueID)

	denied := e.patchItem(f.mom, bike.ID, map[string]any{"list_id": sueList}, nil)
	require.Equal(t, http.StatusForbidden, denied.StatusCode)
	require.Equal(t, "cannot_edit_target_list", readProblem(t, denied).Code)

	moved := e.patchItem(f.mom, bike.ID, map[string]any{"list_id": f.momList}, nil)
	require.Equal(t, http.StatusOK, moved.StatusCode)
	require.Equal(t, []string{"Bike"}, e.order(f.mom, f.momList))
	require.Empty(t, e.order(emma, emmaList))
}

func TestAChildSeesTheirParentsListsAndCannotEditThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	e.addItem(f.mom, f.momList, "Kettle")

	got := decode[listView](t, e.getList(emma, f.momList))
	require.False(t, got.CanEdit)
	require.Equal(t, 1, got.ItemCount)
	require.Equal(t, http.StatusForbidden, e.postItem(emma, f.momList, map[string]any{"title": "x"}).StatusCode)
	// Emma sees "Our house" through Mom, her parent.
	require.Equal(t, http.StatusOK, e.getList(emma, f.house).StatusCode)
}

func TestAGiverWhoCanSeeAListButNotEditItGets403OnEveryWrite(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")

	for name, res := range map[string]*http.Response{
		"add":    e.postItem(f.sue, f.momList, map[string]any{"title": "x"}),
		"edit":   e.patchItem(f.sue, kettle.ID, map[string]any{"title": "x"}, nil),
		"delete": e.deleteItem(f.sue, kettle.ID, nil),
		"list":   e.patchList(f.sue, f.momList, map[string]any{"name": "x"}, nil),
	} {
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
	}
	require.Equal(t, []string{"Kettle"}, e.order(f.mom, f.momList))
}

func TestAGiverGetsTheClaimStateAndARecipientGetsNoKeyOfIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")

	raw := decode[map[string]json.RawMessage](t, e.getItem(f.sue, kettle.ID))
	require.JSONEq(t, `1`, string(raw["still_wanted"]))
	require.JSONEq(t, `0`, string(raw["my_claimed_quantity"]))
	require.JSONEq(t, `true`, string(raw["can_claim"]))

	mine := decode[map[string]json.RawMessage](t, e.getItem(f.mom, kettle.ID))
	require.NotContains(t, mine, "still_wanted")
	require.NotContains(t, mine, "my_claimed_quantity")
	require.JSONEq(t, `false`, string(mine["can_claim"]))
}

func TestItemCountCountsTheItemsOnTheListForEveryoneWhoSeesIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addItem(f.mom, f.momList, "A")
	e.addItem(f.mom, f.momList, "B")

	require.Equal(t, 2, decode[listView](t, e.getList(f.sue, f.momList)).ItemCount)
	rows := decode[listList](t, e.listLists(f.sue, "recipient_id="+f.momID+"&is_primary=true"))
	require.Equal(t, 2, rows.Data[0].ItemCount)
	require.Equal(t, 2, decode[itemList](t, e.listItems(f.sue, f.momList, "")).TotalCount)
}

func TestTheAdminSeesAndEditsAChildsListsOnlyAsTheirParent(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	emmaList := e.primaryListFor(emma, emmaID)
	e.addItem(emma, emmaList, "Bike")

	require.Equal(t, http.StatusNotFound, e.getList(f.admin, emmaList).StatusCode)
	e.linkParents(f.admin, emmaID, f.adminID)

	l := decode[listView](t, e.getList(f.admin, emmaList))
	require.True(t, l.CanEdit, "the admin links themselves as a parent, and is a parent like any other")
	require.Equal(t, http.StatusCreated, e.postItem(f.admin, emmaList, map[string]any{"title": "Helmet"}).StatusCode)
}

func TestADeactivatedRecipientKeepsTheirParentsSightAndLosesTheGroupsSight(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	graceID, grace := e.otherUser("Grace")
	graceList := e.primaryListFor(grace, graceID)
	e.join(f.admin, e.newGroup(f.admin, "Choir").ID, graceID, f.sueID)
	hoppers := e.extraList("Hoppers", graceID, f.momID)
	require.Equal(t, http.StatusOK, e.getList(f.sue, graceList).StatusCode)
	gracesParentID, gracesParent := e.otherUser("Hopper")
	e.linkParents(f.admin, graceID, gracesParentID)

	require.Equal(t, http.StatusOK, e.deactivate(f.admin, graceID).StatusCode)

	require.Equal(t, http.StatusNotFound, e.getList(f.sue, graceList).StatusCode, "a group mate can't see a deactivated user, so not their list")
	require.Equal(t, http.StatusOK, e.getList(gracesParent, graceList).StatusCode, "a parent still sees all of their lists")
	require.Equal(t, http.StatusOK, e.getList(f.sue, hoppers).StatusCode, "a list stays visible through another recipient")
}

// primaryListFor is the ID of a user's primary list, as the session sees it. A
// session sees the primary lists of other users too, so it names the recipient.
func (e *env) primaryListFor(token, userID string) string {
	e.t.Helper()
	list := decode[listList](e.t, e.listLists(token, "is_primary=true&recipient_id="+userID))
	require.Len(e.t, list.Data, 1, "a user has exactly one primary list")
	return list.Data[0].ID
}
