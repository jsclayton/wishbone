package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the promise that an ID is never used again (docs/spec.md,
// API principles, Values). In each one, the resource with the highest ID
// of its kind is deleted, and the next one made gets a new ID. So a client that
// kept the old ID never reaches a different resource through it.

func TestAnItemsIDIsNotGivenToTheNextItem(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	e.addItem(token, listID, "Kettle")
	book := e.addItem(token, listID, "Book")
	require.Equal(t, http.StatusNoContent, e.deleteItem(token, book.ID, nil).StatusCode)
	require.Zero(t, e.rowCount("items", "id = ?", book.ID), "the item's row is gone, not hidden")

	rake := e.addItem(token, listID, "Rake")

	require.Greater(t, mustParse(t, rake.ID), mustParse(t, book.ID))
	require.Equal(t, http.StatusNotFound, e.getItem(token, book.ID).StatusCode, "the old ID still finds nothing")
}

func TestAListsIDIsNotGivenToTheNextList(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.signUp()
	token, _ := e.grace()
	garden := decode[listView](t, e.postList(token, map[string]any{"name": "Garden"})).ID
	require.Equal(t, http.StatusNoContent, e.deleteList(token, garden, nil).StatusCode)
	require.Zero(t, e.rowCount("lists", "id = ?", garden), "the list's row is gone, not hidden")

	camping := decode[listView](t, e.postList(token, map[string]any{"name": "Camping"})).ID

	require.Greater(t, mustParse(t, camping), mustParse(t, garden))
	require.Equal(t, http.StatusNotFound, e.getList(token, garden).StatusCode, "the old ID still finds nothing")
}

// A user's delete takes their sessions with it. The next user doesn't get the
// user's ID, and the next session doesn't get the ID of one of theirs.
func TestADeletedUsersIDAndTheirSessionsIDAreNotGivenAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	graceSessions := decode[sessionList](t, e.listSessions(graceID, cookie(grace)))
	require.Len(t, graceSessions.Data, 1)
	e.deleteUser(graceID)

	linusID, linus := e.otherUser("Linus")

	require.Greater(t, mustParse(t, linusID), mustParse(t, graceID))
	linusSessions := decode[sessionList](t, e.listSessions(linusID, cookie(linus)))
	require.Len(t, linusSessions.Data, 1)
	require.Greater(t, mustParse(t, linusSessions.Data[0].ID), mustParse(t, graceSessions.Data[0].ID))
	require.Equal(t, http.StatusNotFound, e.listSessions(graceID, cookie(admin)).StatusCode, "the old ID still finds nothing")
}

func TestAGiftsIDIsNotGivenToTheNextGift(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	first := e.claim(f.sue, kettle.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteGift(f.sue, first.ID, nil).StatusCode)
	require.Zero(t, e.rowCount("gifts", "id = ?", first.ID), "the gift's row is gone, not hidden")

	second := e.claim(f.sue, kettle.ID, nil)

	require.Greater(t, mustParse(t, second.ID), mustParse(t, first.ID))
	require.Equal(t, http.StatusNotFound, e.getGift(f.sue, first.ID).StatusCode, "the old ID still finds nothing")
}

func TestAnOccasionsIDIsNotGivenToTheNextOccasion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.signUp()
	token, _ := e.grace()
	graduation := e.addOccasion(token, "Graduation", once(2027, 6, 5), "")
	require.Equal(t, http.StatusNoContent, e.deleteOccasion(token, graduation.ID).StatusCode)

	wedding := e.addOccasion(token, "Wedding", once(2027, 8, 14), "")

	require.Greater(t, mustParse(t, wedding.ID), mustParse(t, graduation.ID))
	require.Equal(t, http.StatusNotFound, e.getOccasion(token, graduation.ID).StatusCode, "the old ID still finds nothing")
}

func TestAGroupsIDIsNotGivenToTheNextGroup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, admin := e.signUp()
	cousins := e.newGroup(admin, "Cousins")
	res := e.do(request{method: http.MethodDelete, path: "/groups/" + cousins.ID, header: cookie(admin)})
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	friends := e.newGroup(admin, "Friends")

	require.Greater(t, mustParse(t, friends.ID), mustParse(t, cousins.ID))
	require.Equal(t, http.StatusNotFound, e.do(request{path: "/groups/" + cousins.ID, header: cookie(admin)}).StatusCode,
		"the old ID still finds nothing")
}
