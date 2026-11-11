package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover deleting an extra list that has gifts: each item is deleted
// as a delete by a user who can edit the list does, and a kept gift is for the
// list's last recipients (docs/spec.md, Lists, Leaving and deleting).

func (e *env) listRows(listID string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM lists WHERE id = ?`, listID))
	return n
}

func (e *env) itemRows() int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM items`))
	return n
}

func listNamesOf(t *testing.T, e *env, token string) []string {
	t.Helper()
	return listNames(decode[listList](t, e.listLists(token, "")))
}

func TestDeletingAListKeepsItsOpenGiftsFlaggedRemovedAndNobodySeesTheListAgain(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	garden := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Garden"})).ID
	rake := e.addItem(f.mom, garden, "Rake")
	hose := e.addItem(f.mom, garden, "Hose")
	e.addItem(f.mom, garden, "Unclaimed")
	img := e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.patchImages(f.mom, rake.ID, map[string]any{"image_ids": []string{img.ID}}).StatusCode)
	require.Equal(t, http.StatusOK, e.patchList(f.mom, garden, map[string]any{"description": "Back fence"}, nil).StatusCode)
	mine := e.claim(f.sue, rake.ID, nil)
	tess := e.giver("Tess", f)
	theirs := e.claim(tess, hose.ID, nil)

	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, garden, nil).StatusCode, "a gift never stops a delete")

	// Nobody sees the list or its items again, the recipient included.
	for name, token := range map[string]string{"recipient": f.mom, "giver": f.sue, "admin": f.admin} {
		require.Equal(t, http.StatusNotFound, e.getList(token, garden).StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.listItems(token, garden, "").StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.getItem(token, rake.ID).StatusCode, name)
		require.NotContains(t, listNamesOf(t, e, token), "Garden", name)
	}
	require.Equal(t, http.StatusNotFound, e.deleteList(f.mom, garden, nil).StatusCode)

	// The gift is flagged removed, with what the item was. The giver keeps it or unclaims it.
	got := e.gift(f.sue, mine.ID)
	require.Equal(t, []string{"removed"}, flagCodes(got))
	require.Equal(t, "Rake", got.Title)
	require.Equal(t, img.ID, got.Image.ID)
	require.Equal(t, garden, got.List.ID)
	require.Equal(t, "Garden", got.List.Name)
	require.Nil(t, got.Recipients, "the giver can't see a list that is gone")
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.patchGift(f.sue, mine.ID, map[string]any{"is_bought": true}, nil).StatusCode)
	require.Equal(t, []string{"removed"}, flagCodes(e.gift(tess, theirs.ID)))

	// Only the items with gifts stay, and they go for good with their last gift.
	require.Equal(t, 2, e.itemRows())
	require.Equal(t, 1, e.listRows(garden))
	require.Equal(t, http.StatusOK, e.acknowledge(f.sue, mine.ID, nil).StatusCode, "keeping the gift")
	require.Equal(t, http.StatusNoContent, e.deleteGift(tess, theirs.ID, nil).StatusCode)
	require.Equal(t, 1, e.itemRows())
	require.Equal(t, 1, e.listRows(garden), "the kept gift still needs its list")
	var description *string
	require.NoError(t, e.db.GetContext(t.Context(), &description, `SELECT description FROM lists WHERE id = ?`, garden))
	require.Nil(t, description, "the shell keeps only what the gifts need")
	require.Equal(t, http.StatusNoContent, e.deleteGift(f.sue, mine.ID, nil).StatusCode)
	require.Zero(t, e.itemRows())
	require.Zero(t, e.listRows(garden), "nothing needs the list any more")
	var kept int
	require.NoError(t, e.db.GetContext(t.Context(), &kept, `SELECT count(*) FROM kept_list_recipients`))
	require.Zero(t, kept)
}

func TestAKeptGiftIsStillForTheListsLastRecipientsAtReveal(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	_, err := e.db.ExecContext(t.Context(), `UPDATE users SET timezone = 'Pacific/Auckland' WHERE id = ?`, f.momID)
	require.NoError(t, err)
	garden := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Garden"})).ID
	rake := e.addItem(f.mom, garden, "Rake")
	g := e.claim(f.sue, rake.ID, nil)
	before := e.gift(f.sue, g.ID).RevealAt
	require.Equal(t, "2027-01-01T11:00:00Z", before, "midnight after Jan 1 in Auckland (UTC+13)")

	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, garden, nil).StatusCode)

	require.Equal(t, before, e.gift(f.sue, g.ID).RevealAt, "Mom's zone still counts, though nobody sees her through the list")
	var recipients []int64
	require.NoError(t, e.db.SelectContext(t.Context(), &recipients, `SELECT user_id FROM kept_list_recipients WHERE list_id = ?`, garden))
	require.Len(t, recipients, 1)
	var live int
	require.NoError(t, e.db.GetContext(t.Context(), &live, `SELECT count(*) FROM list_recipients WHERE list_id = ?`, garden))
	require.Zero(t, live, "a deleted list has no recipients, so no rule shows it")
}

func TestDeletingAListWithNoGiftsRemovesItAndItsInvitationsForGood(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	garden := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Garden"})).ID
	e.addItem(f.mom, garden, "Rake")
	e.invite(f.mom, garden, f.sueID)
	claimed := e.addItem(f.mom, garden, "Hose")
	g := e.claim(f.sue, claimed.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteGift(f.sue, g.ID, nil).StatusCode, "unclaimed before the delete")

	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, garden, nil).StatusCode)

	require.Zero(t, e.listRows(garden))
	require.Zero(t, e.itemRows())
	require.Empty(t, decode[invitationList](t, e.listInvitations(f.sue, "")).Data)
}

func TestAKeptGiftsListLosesItsInvitations(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	garden := decode[listView](t, e.postList(f.mom, map[string]any{"name": "Garden"})).ID
	rake := e.addItem(f.mom, garden, "Rake")
	e.claim(f.sue, rake.ID, nil)
	e.invite(f.mom, garden, f.sueID)

	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, garden, nil).StatusCode)

	require.Empty(t, decode[invitationList](t, e.listInvitations(f.sue, "")).Data, "Sue is not invited to a list that is gone")
	require.Empty(t, decode[invitationList](t, e.listInvitations(f.mom, "")).Data)
}

func TestAParentsDeleteOfTheirChildsListFlagsTheGiftsToo(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	toys := e.extraList("Toys", emmaID)
	e.join(f.admin, decodeGroupID(t, e, f.admin, "Cousins"), emmaID)
	skates := e.addItem(f.mom, toys, "Skates")
	g := e.claim(f.sue, skates.ID, nil)

	require.Equal(t, http.StatusNoContent, e.deleteList(f.mom, toys, nil).StatusCode)

	require.Equal(t, []string{"removed"}, flagCodes(e.gift(f.sue, g.ID)))
}

func decodeGroupID(t *testing.T, e *env, admin, name string) string {
	t.Helper()
	for _, g := range decode[groupList](t, e.listGroups(admin, "")).Data {
		if g.Name == name {
			return g.ID
		}
	}
	t.Fatalf("no group %q", name)
	return ""
}
