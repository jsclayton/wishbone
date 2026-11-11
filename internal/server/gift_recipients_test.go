package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover what a change in a list's recipients does to the open gifts
// on it (docs/spec.md, Gifts, Who a gift is for, Occasions for a gift and
// Changes while a gift is open; docs/routes.md, Gift fields and Gift routes).

// removeFromGroup takes a user out of a group as the admin.
func (e *env) removeFromGroup(admin, groupID, userID string) {
	e.t.Helper()
	res := e.do(request{method: http.MethodDelete, path: "/groups/" + groupID + "/users/" + userID, header: cookie(admin)})
	require.Equal(e.t, http.StatusNoContent, res.StatusCode, "remove user %s from group %s", userID, groupID)
}

// cousins is the ID of the group "Cousins" of the family fixture.
func (e *env) cousins(f family) string {
	e.t.Helper()
	for _, g := range decode[groupList](e.t, e.listGroups(f.admin, "")).Data {
		if g.Name == "Cousins" {
			return g.ID
		}
	}
	e.t.Fatal("no Cousins group")
	return ""
}

// joinList makes a user a recipient of a list: a recipient invites them and they accept.
func (e *env) joinList(inviter, listID, userID, userToken string) {
	e.t.Helper()
	inv := e.invite(inviter, listID, userID)
	require.Equal(e.t, http.StatusOK, e.acceptInvitation(userToken, inv.ID).StatusCode, "accept")
}

func summaryIDs(v any) []string {
	rows, _ := v.([]any)
	out := []string{}
	for _, r := range rows {
		out = append(out, r.(map[string]any)["id"].(string))
	}
	return out
}

func TestAGiverWhoCannotSeeAnyRecipientCannotPickAnOccasionAndSeesOnlyTheRevealDate(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	require.NotNil(t, g.Occasion)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)

	// Sue sees none of the recipients now, so her occasion is hidden and she can't pick another.
	got := e.gift(f.sue, g.ID)
	require.Nil(t, got.Occasion)
	require.Nil(t, got.OccasionName)
	require.Equal(t, "2027-01-01", got.OccasionDate, "the reveal date still shows")
	require.Equal(t, "2027-01-02T00:00:00Z", got.RevealAt)
	body := readBody(t, e.getGift(f.sue, g.ID))
	require.Contains(t, body, `"occasion":null`)
	require.Contains(t, body, `"occasion_name":null`)
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"occasion_id": g.Occasion.ID}, nil), http.StatusUnprocessableEntity, "occasion_locked")
	// Anything else about the gift still changes.
	require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": true}, nil).StatusCode)
}

func TestAGiverWhoBecomesARecipientKeepsTheirGiftForTheOthersAndCanOnlyLowerTheQuantity(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 4})
	g := e.claim(f.sue, grill.ID, map[string]any{"quantity": 2})
	require.True(t, g.CanRaiseQuantity)

	e.joinList(f.mom, f.house, f.sueID, f.sue)

	got := e.gift(f.sue, g.ID)
	require.Equal(t, 2, got.Quantity, "the gift stays")
	require.False(t, got.CanRaiseQuantity)
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(got.Recipients)), "it is for the others, never the giver")
	require.Empty(t, got.Flags, "nothing changed for the gift's recipients")

	// Every raise is the same 403, whether or not more are still wanted.
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 3}, nil), http.StatusForbidden, "quantity_raise_not_allowed")
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 30}, nil), http.StatusForbidden, "quantity_raise_not_allowed")
	lowered := e.patchGift(f.sue, g.ID, map[string]any{"quantity": 1}, nil)
	require.Equal(t, http.StatusOK, lowered.StatusCode)
	require.Equal(t, 1, decode[giftView](t, lowered).Quantity)
	// She can't claim on the list as a recipient either.
	requireCoded(t, e.postGift(f.sue, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
}

func anySlice(v []summaryView) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = map[string]any{"id": s.ID}
	}
	return out
}
