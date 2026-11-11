package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover what deleting a user does to their lists (docs/spec.md,
// Deactivation and deletion, Deletion; Lists, Leaving and deleting).

func TestDeletingARecipientOfASharedListLeavesItAndFlagsTheOpenGiftsThere(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID) // the cousins see Dad too
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	tess := e.giver("Tess", f)
	// Sue's gift is for Dad's birthday, and Tess's for the next occasion of either.
	sues := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.dadID)})
	tesses := e.claim(tess, grill.ID, nil)

	e.deleteUser(f.dadID)

	// The list stays, with Mom as its only recipient. She can delete it now.
	house := decode[listView](t, e.getList(f.mom, f.house))
	require.Equal(t, []summaryView{{ID: f.momID, Name: "Mom"}}, house.Recipients)
	require.True(t, house.CanDelete)
	// Each open gift there is flagged with the old and the new recipients, without
	// Dad, who is gone: both read Mom.
	for name, g := range map[string]giftView{"Sue": e.gift(f.sue, sues.ID), "Tess": e.gift(tess, tesses.ID)} {
		oldIDs, newIDs := recipientsFlag(t, g)
		require.Equal(t, []string{f.momID}, oldIDs, name)
		require.Equal(t, []string{f.momID}, newIDs, name)
		require.Equal(t, []string{f.momID}, summaryIDs(anySlice(g.Recipients)), name)
	}
	// Sue's gift was for Dad's birthday. It moved to Mom's next occasion and is flagged.
	moved := e.gift(f.sue, sues.ID)
	require.Contains(t, flagCodes(moved), "occasion_moved")
	require.Equal(t, f.birthdayOfMom(e), moved.Occasion.ID)
	require.NotContains(t, flagCodes(e.gift(tess, tesses.ID)), "occasion_moved", "Tess's gift was not for Dad's occasion")
}

func TestDeletingTheOnlyRecipientOfAListDeletesTheListItsItemsAndItsInvitations(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID, f.hedyID)
	tools := e.extraList("Dad's tools", f.dadID)
	saw := e.addItem(f.dad, tools, "Saw", map[string]any{"quantity": 2})
	e.invite(f.dad, tools, f.hedyID)
	socks := e.addItem(f.dad, f.dadList, "Socks")
	sawGift := e.claim(f.sue, saw.ID, nil)
	socksGift := e.claim(f.mom, socks.ID, nil)
	require.Len(t, decode[invitationList](t, e.listInvitations(f.hedy, "")).Data, 1)

	e.deleteUser(f.dadID)

	// Both lists are gone, with their items and invitations, and no gift is kept
	// for its giver: nobody can receive it.
	for _, id := range []string{tools, f.dadList} {
		require.Zero(t, e.listRows(id), "list %s", id)
	}
	require.Zero(t, e.itemRows(), "no item is left")
	requireCoded(t, e.getGift(f.sue, sawGift.ID), http.StatusNotFound, "not_found")
	requireCoded(t, e.getGift(f.mom, socksGift.ID), http.StatusNotFound, "not_found")
	require.Zero(t, e.rowCount("gifts", `1 = 1`))
	require.Empty(t, decode[invitationList](t, e.listInvitations(f.hedy, "")).Data)
	require.Zero(t, e.rowCount("list_invitations", `1 = 1`))
	// The list they shared with Mom stays.
	require.Equal(t, http.StatusOK, e.getList(f.mom, f.house).StatusCode)
}
