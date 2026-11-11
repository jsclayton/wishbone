package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the flags on a giver's open gift when the item or the list
// changes, acknowledging them, and the attention rule (docs/spec.md, Items,
// Who changes an item and Moving an item; Lists, Leaving and deleting; Gifts,
// Changes while a gift is open; docs/routes.md, Gift fields and Gift routes).

func (e *env) acknowledge(token, id string, body any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/gifts/" + id + "/acknowledge", body: body, header: cookie(token)})
}

func flagCodes(g giftView) []string {
	codes := []string{}
	for _, f := range g.Flags {
		codes = append(codes, f["code"].(string))
	}
	return codes
}

func flagOf(t *testing.T, g giftView, code string) map[string]any {
	t.Helper()
	for _, f := range g.Flags {
		if f["code"] == code {
			return f
		}
	}
	t.Fatalf("no %q flag in %v", code, g.Flags)
	return nil
}

func (e *env) gift(token, id string) giftView {
	e.t.Helper()
	res := e.getGift(token, id)
	require.Equal(e.t, http.StatusOK, res.StatusCode)
	return decode[giftView](e.t, res)
}

func TestDeletingAClaimedItemFlagsEachOpenGiftRemovedAndTheGiverKeepsOrUnclaimsIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	sues := e.claim(f.sue, kettle.ID, nil)
	tess := e.giver("Tess", f)
	tesses := e.claim(tess, kettle.ID, map[string]any{"is_bought": true})
	quiet := e.giver("Quiet", f)
	other := e.addItem(f.mom, f.momList, "Other")
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, kettle.ID, nil).StatusCode)

	got := e.gift(f.sue, sues.ID)
	require.Equal(t, []string{"removed"}, flagCodes(got))
	require.Len(t, got.Flags[0], 1, "removed has no other field")
	require.True(t, got.CanAcknowledge)
	require.True(t, got.NeedsAttention)
	require.Equal(t, []string{"removed"}, flagCodes(e.gift(tess, tesses.ID)), "every giver's gift is flagged")
	require.True(t, e.gift(tess, tesses.ID).NeedsAttention, "also a gift that is bought")

	// Keeping it is acknowledging removed: the gift stays, as it was given.
	res := e.acknowledge(f.sue, sues.ID, map[string]any{"codes": []string{"removed"}})
	require.Equal(t, http.StatusOK, res.StatusCode)
	kept := decode[giftView](t, res)
	require.Empty(t, kept.Flags)
	require.NotNil(t, kept.Flags)
	require.False(t, kept.CanAcknowledge)
	require.NotEqual(t, got.Version, res.Header.Get("ETag"))
	require.Equal(t, "Kettle", e.gift(f.sue, sues.ID).Title)
	require.Equal(t, []string{"removed"}, flagCodes(e.gift(tess, tesses.ID)), "another giver's flag stays")

	// Unclaiming the other takes the item for good.
	require.Equal(t, http.StatusNoContent, e.deleteGift(tess, tesses.ID, nil).StatusCode)
	var n int
	require.NoError(t, e.db.GetContext(t.Context(), &n, `SELECT count(*) FROM items WHERE id = ?`, kettle.ID))
	require.Equal(t, 1, n, "Sue's gift still holds it")

	// Nobody else sees a flag: the recipient, a giver with no gift, or a stranger.
	for name, token := range map[string]string{"recipient": f.mom, "giver": quiet, "stranger": f.hedy} {
		requireCoded(t, e.getGift(token, sues.ID), http.StatusNotFound, "not_found")
		requireCoded(t, e.acknowledge(token, sues.ID, nil), http.StatusNotFound, "not_found")
		require.NotContains(t, readBody(t, e.getItem(token, other.ID)), "flags", name)
	}
}

func TestAcknowledgeClearsEveryFlagByDefaultAndOnlyTheCodesGiven(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	g := e.claim(f.sue, grill.ID, nil)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"title": "Big grill"}, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, grill.ID, nil).StatusCode)
	require.ElementsMatch(t, []string{"changed", "removed"}, flagCodes(e.gift(f.sue, g.ID)))

	// A code the gift doesn't have changes nothing.
	got := decode[giftView](t, e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"moved"}}))
	require.ElementsMatch(t, []string{"changed", "removed"}, flagCodes(got))
	one := decode[giftView](t, e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"changed"}}))
	require.Equal(t, []string{"removed"}, flagCodes(one))
	require.True(t, one.CanAcknowledge)

	// An empty array names no flag: it clears none, where leaving codes out clears every one.
	none := decode[giftView](t, e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{}}))
	require.Equal(t, []string{"removed"}, flagCodes(none))

	res := e.acknowledge(f.sue, g.ID, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Empty(t, decode[giftView](t, res).Flags)
	require.Equal(t, http.StatusOK, e.acknowledge(f.sue, g.ID, map[string]any{}).StatusCode, "nothing left to clear is not an error")
}

func TestAcknowledgeRefusesAnUnknownCodeAndAGivenGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)

	res := e.acknowledge(f.sue, g.ID, map[string]any{"codes": []string{"sparkly"}})

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
	requireCoded(t, e.acknowledge(f.sue, "abc", nil), http.StatusNotFound, "not_found")

	// A given gift is not the giver's to answer.
	_, err := e.db.ExecContext(t.Context(), `UPDATE gifts SET given_at = ? WHERE id = ?`, e.clock.now(), g.ID)
	require.NoError(t, err)
	requireCoded(t, e.acknowledge(f.sue, g.ID, nil), http.StatusForbidden, "forbidden")
}
