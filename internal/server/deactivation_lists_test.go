package server

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover a parent's powers over the lists of a deactivated child
// (docs/spec.md, Deactivation and deletion, Who acts while a user is
// deactivated; docs/routes.md, Roles, A deactivated user). The parent keeps
// every read, and loses every write that comes through the child.

// emmasLists makes Emma, Mom's child, with an extra list of her own ("Emma's
// books", with one item), and an extra list that she shares with Zed ("Shared").
type emmasLists struct {
	emmaID, zedID, zed      string
	books, shared, bookItem string
}

func (e *env) emmasLists(f family) emmasLists {
	e.t.Helper()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	zedID, zed := e.otherUser("Zed")
	l := emmasLists{emmaID: emmaID, zedID: zedID, zed: zed}
	l.books = e.extraList("Emma's books", emmaID)
	l.shared = e.extraList("Shared", emmaID, zedID)
	l.bookItem = e.addItem(f.mom, l.books, "Atlas").ID
	return l
}

func TestAParentKeepsEveryReadOfADeactivatedChildsListsAndLosesEveryEdit(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	em := e.emmasLists(f)
	before := decode[listView](t, e.getList(f.mom, em.books))
	require.True(t, before.CanEdit)
	require.True(t, before.CanDelete)
	res, err := e.db.ExecContext(t.Context(), `INSERT INTO store_links (item_id, url, store, created_at) VALUES (?, 'https://shop.example/atlas', 'shop.example', ?)`, em.bookItem, e.clock.now())
	require.NoError(t, err)
	linkID, err := res.LastInsertId()
	require.NoError(t, err)
	link := strconv.FormatInt(linkID, 10)
	e.deactivated(f.admin, em.emmaID)

	for name, id := range map[string]string{"books": em.books, "shared": em.shared} {
		got := decode[listView](t, e.getList(f.mom, id))
		require.False(t, got.CanEdit, name)
		require.False(t, got.CanDelete, name)
		require.False(t, got.CanInvite, name)
		require.False(t, got.CanLeave, name)
	}
	// The reads stay: the list, its items, its recipients, and the part of the child.
	require.Equal(t, http.StatusOK, e.listItems(f.mom, em.books, "").StatusCode)
	require.Equal(t, 1, decode[listView](t, e.getList(f.mom, em.books)).ItemCount)
	byChild := decode[listList](t, e.listLists(f.mom, "recipient_id="+em.emmaID))
	require.ElementsMatch(t, []string{"Emma", "Emma's books", "Shared"}, listNames(byChild))
	require.ElementsMatch(t, []string{"Mom", "Our house"}, listNames(decode[listList](t, e.listLists(f.mom, "can_edit=true"))),
		"a list that Mom edits only through Emma is not in ?can_edit=true")
	row := e.recipientOf(f.mom, em.shared, em.emmaID)
	require.False(t, row.CanDelete)
	require.NotNil(t, row.SeesListAfterLeaving, "a parent still reads it")
	item := decode[itemView](t, e.getItem(f.mom, em.bookItem))
	require.False(t, item.CanEdit)
	require.False(t, item.CanDelete)
	require.False(t, item.CanRefreshPrices)
	require.False(t, item.CanClaim, "Mom sees the list only through a deactivated recipient")

	// Every write is a 403 user_deactivated, not forbidden.
	requireDeactivated(t, e.patchList(f.mom, em.books, map[string]any{"name": "Nope"}, nil))
	requireDeactivated(t, e.deleteList(f.mom, em.books, nil))
	requireDeactivated(t, e.leave(f.mom, em.shared, em.emmaID, nil))
	requireDeactivated(t, e.postItem(f.mom, em.books, map[string]any{"title": "Nope"}))
	requireDeactivated(t, e.patchItem(f.mom, em.bookItem, map[string]any{"title": "Nope"}, nil))
	requireDeactivated(t, e.deleteItem(f.mom, em.bookItem, nil))
	requireDeactivated(t, e.postStoreLink(f.mom, em.bookItem, "https://shop.example/atlas"))
	requireDeactivated(t, e.refreshPrices(f.mom, em.bookItem))
	requireDeactivated(t, e.patchStoreLink(f.mom, em.bookItem, link, map[string]any{"price_minor": 100}))
	requireDeactivated(t, e.deleteStoreLink(f.mom, em.bookItem, link))
	requireDeactivated(t, e.patchImages(f.mom, em.bookItem, map[string]any{"image_ids": []string{}}))
	// An item moves out of the child's list or into it only for a parent who edits both.
	requireDeactivated(t, e.patchItem(f.mom, em.bookItem, map[string]any{"list_id": f.momList}, nil))
	other := e.addItem(f.mom, f.momList, "Kettle")
	requireDeactivated(t, e.patchItem(f.mom, other.ID, map[string]any{"list_id": em.books}, nil))

	// Nothing changed, and the lists are still Emma's.
	require.Equal(t, "Emma's books", decode[listView](t, e.getList(f.mom, em.books)).Name)
	require.Equal(t, 1, decode[listView](t, e.getList(f.mom, em.books)).ItemCount)
	require.Len(t, decode[listView](t, e.getList(f.mom, em.shared)).Recipients, 2)
	require.Equal(t, "Atlas", decode[itemView](t, e.getItem(f.mom, em.bookItem)).Title)

	// A user who is not a parent of anyone in it keeps getting a plain forbidden or 404.
	requireCoded(t, e.patchList(f.dad, em.books, map[string]any{"name": "Nope"}, nil), http.StatusNotFound, "not_found")
}

func TestAParentGetsTheirPowersBackWhenTheChildIsReactivated(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	em := e.emmasLists(f)
	e.deactivated(f.admin, em.emmaID)

	require.Equal(t, http.StatusOK, e.reactivate(f.admin, em.emmaID).StatusCode)

	require.True(t, decode[listView](t, e.getList(f.mom, em.books)).CanEdit)
	require.Equal(t, http.StatusOK, e.patchList(f.mom, em.books, map[string]any{"name": "Atlases"}, nil).StatusCode)
	require.True(t, e.recipientOf(f.mom, em.shared, em.emmaID).CanDelete)
	require.Equal(t, http.StatusNoContent, e.leave(f.mom, em.shared, em.emmaID, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, em.books, nil).StatusCode)
}

func TestAParentKeepsThePowersThatComeThroughAnotherRecipient(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.linkParents(f.admin, f.dadID, f.momID) // Dad is Mom's child here
	e.deactivated(f.admin, f.dadID)

	// Mom is a recipient of "Our house" with Dad, so she edits it, and Dad's
	// deactivation only stops what comes through Dad.
	got := decode[listView](t, e.getList(f.mom, f.house))
	require.True(t, got.CanEdit)
	require.True(t, got.CanLeave)
	require.Equal(t, http.StatusOK, e.patchList(f.mom, f.house, map[string]any{"name": "Our home"}, nil).StatusCode)
	require.Equal(t, http.StatusCreated, e.postItem(f.mom, f.house, map[string]any{"title": "Sofa"}).StatusCode, "the item is added")
	require.True(t, e.recipientOf(f.mom, f.house, f.momID).CanDelete)
	require.False(t, e.recipientOf(f.mom, f.house, f.dadID).CanDelete, "she can't make a deactivated Dad leave")
	requireDeactivated(t, e.leave(f.mom, f.house, f.dadID, nil))
	require.Equal(t, http.StatusNoContent, e.leave(f.mom, f.house, f.momID, nil).StatusCode, "she can leave for herself")
}

func TestAParentWhoIsAlsoARecipientKeepsTheirOwnPowersOnAChildsList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	em := e.emmasLists(f)
	e.join(f.admin, e.cousins(f), em.zedID)       // Zed sees Mom
	e.joinList(em.zed, em.shared, f.momID, f.mom) // Zed invites Mom: she is now a recipient too
	e.deactivated(f.admin, em.emmaID)

	got := decode[listView](t, e.getList(f.mom, em.shared))
	require.True(t, got.CanEdit, "as a recipient")
	require.True(t, got.CanLeave)
	require.True(t, got.CanInvite)
	require.Equal(t, http.StatusOK, e.patchList(f.mom, em.shared, map[string]any{"name": "Together"}, nil).StatusCode)
	require.False(t, e.recipientOf(f.mom, em.shared, em.emmaID).CanDelete, "but not through Emma")
	requireDeactivated(t, e.leave(f.mom, em.shared, em.emmaID, nil))
}
