package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/userdelete"
)

// These tests cover the gifts kept on a list that was deleted earlier, when a user
// who was its last recipient is deleted (docs/spec.md, Deactivation and
// deletion, Deletion; Lists, Leaving and deleting). Such a list keeps its row and
// its last recipients in kept_list_recipients, and nobody sees it.

// keptList makes "Dad's tools", a list with Dad as its only recipient and one item
// that Sue has claimed, and then has Dad delete it, so Sue's gift is kept. It
// returns the list, the item and Sue's gift.
func (e *env) keptList(f family, occasionID string) (listID, itemID string, g giftView) {
	e.t.Helper()
	listID = e.extraList("Dad's tools", f.dadID)
	saw := e.addItem(f.dad, listID, "Saw")
	body := map[string]any{}
	if occasionID != "" {
		body["occasion_id"] = occasionID
	}
	g = e.claim(f.sue, saw.ID, body)
	require.Equal(e.t, http.StatusNoContent, e.deleteList(f.dad, listID, nil).StatusCode)
	require.Equal(e.t, 1, e.rowCount("lists", `id = ? AND deleted_at IS NOT NULL`, listID), "the list is kept for the gift")
	require.Equal(e.t, 1, e.rowCount("kept_list_recipients", `list_id = ? AND user_id = ?`, listID, f.dadID))
	return listID, saw.ID, g
}

// keepFor adds a last recipient to a kept list, as if the list had been deleted with
// a second recipient. The app deletes a list only with one, so a test sets the rest up.
func (e *env) keepFor(listID string, userIDs ...string) {
	e.t.Helper()
	for _, id := range userIDs {
		_, err := e.db.ExecContext(e.t.Context(), `INSERT INTO kept_list_recipients (list_id, user_id) VALUES (?, ?)`, listID, id)
		require.NoError(e.t, err)
	}
}

func TestThePlanCountsTheGiftsOnKeptListsThatTheDeleteWillDelete(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	listID, _, _ := e.keptList(f, "")

	plan, err := e.deleter().Plan(t.Context(), mustParse(t, f.dadID))
	require.NoError(t, err)
	require.Equal(t, 1, plan.GiftsDeleted, "Dad is the list's only last recipient")
	require.Equal(t, 1, plan.ListsLeft, "Our house")
	require.Equal(t, 1, plan.ListsDeleted, "his primary list")

	e.keepFor(listID, f.momID)
	plan, err = e.deleter().Plan(t.Context(), mustParse(t, f.dadID))
	require.NoError(t, err)
	require.Zero(t, plan.GiftsDeleted, "Mom is a last recipient too, so the gift stays for her")
	_, err = e.deleter().Plan(t.Context(), mustParse(t, f.adminID))
	require.ErrorIs(t, err, userdelete.ErrAdmin)
}

func TestDeletingTheLastRecipientOfAKeptListDeletesTheOpenGiftKeptForIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	listID, itemID, g := e.keptList(f, "")
	require.Equal(t, 1, e.rowCount("gifts", `id = ?`, g.ID))

	e.deleteUser(f.dadID)

	// Nobody can receive the gift, and no gift is kept for its giver. The item and
	// the list go with the last gift.
	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	require.Zero(t, e.rowCount("items", `id = ?`, itemID))
	require.Zero(t, e.listRows(listID))
}

func TestDeletingTheOnlyOtherLastRecipientOfAKeptListLeavesTheGiverAloneAndTheGiftIsDeleted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	listID, _, g := e.keptList(f, "")
	e.keepFor(listID, f.sueID) // the list's last recipients were Dad and Sue

	e.deleteUser(f.dadID)

	// Sue is left as the only recipient, and a gift is never for its giver.
	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	require.Zero(t, e.listRows(listID))
}

func TestDeletingALastRecipientOfAKeptListMovesTheGiftForTheirOccasionToTheRemainingOnes(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	dads := e.addOccasion(f.dad, "Dad's day", annual(3, 4), f.dadID)
	listID, _, g := e.keptList(f, dads.ID)
	e.keepFor(listID, f.momID)
	require.Equal(t, "2027-03-04", e.gift(f.sue, g.ID).OccasionDate)

	e.deleteUser(f.dadID)

	// The gift is for Mom now. Her next occasion is her birthday, and Sue is told.
	moved := e.gift(f.sue, g.ID)
	require.Equal(t, "2027-01-01", moved.OccasionDate)
	require.Contains(t, flagCodes(moved), "occasion_moved")
	require.Equal(t, 1, e.listRows(listID), "the list stays for the gift")
	require.Equal(t, 1, e.rowCount("kept_list_recipients", `list_id = ? AND user_id = ?`, listID, f.momID))
}

func TestDeletingAnotherUserLeavesAGiftOnAListKeptForItsGiverAlone(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	listID, _, g := e.keptList(f, "")
	_, err := e.db.ExecContext(t.Context(), `DELETE FROM kept_list_recipients WHERE list_id = ?`, listID)
	require.NoError(t, err)
	e.keepFor(listID, f.sueID) // kept for the giver alone: a restore reopens such a gift

	e.deleteUser(f.hedyID)

	require.Equal(t, http.StatusOK, e.getGift(f.sue, g.ID).StatusCode, "settle treats a kept list as a restore does")
	require.Equal(t, 1, e.listRows(listID))
}

func TestAGivenGiftOnAKeptListForTheDeletedUserAloneIsDeleted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	listID, itemID, g := e.keptList(f, "")
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	require.Equal(t, 1, e.rowCount("gift_recipients", `gift_id = ?`, g.ID), "given to Dad alone")

	e.deleteUser(f.dadID)

	requireCoded(t, e.getGift(f.sue, g.ID), http.StatusNotFound, "not_found")
	require.Empty(t, decode[giftList](t, e.listGifts(f.sue, "giver_id=me")).Data, "it leaves Sue's Given")
	require.Zero(t, e.rowCount("items", `id = ?`, itemID))
	require.Zero(t, e.listRows(listID))
}

func TestAGivenGiftOnAKeptListWithAnotherLastRecipientStaysForThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	listID, _, g := e.keptList(f, "")
	e.keepFor(listID, f.momID)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"), "the kept gift is given to its last recipients")
	require.Equal(t, "given", e.gift(f.sue, g.ID).State)
	require.Equal(t, 2, e.rowCount("gift_recipients", `gift_id = ?`, g.ID), "Dad and Mom")

	e.deleteUser(f.dadID)

	// It stays for Mom, who is still one of its recipients.
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.mom, "recipient_id=me"))))
	require.Equal(t, []string{f.momID}, summaryIDs(anySlice(e.gift(f.mom, g.ID).Recipients)))
}
