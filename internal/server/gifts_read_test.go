package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover who sees a gift, GET /gifts with its filters and sorts, and
// the effects of claim state on what a giver can load.

func (e *env) giftIDs(l giftList) []string {
	out := make([]string, len(l.Data))
	for i, g := range l.Data {
		out[i] = g.ID
	}
	return out
}

func TestUntilItIsGivenOnlyTheGiverSeesAGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	g := e.claim(f.sue, kettle.ID, nil)
	tess := e.giver("Tess", f)

	for name, token := range map[string]string{"the recipient": f.mom, "another giver": tess, "a stranger": f.hedy, "the admin": f.admin} {
		requireCoded(t, e.getGift(token, g.ID), http.StatusNotFound, "not_found")
		requireCoded(t, e.patchGift(token, g.ID, map[string]any{"quantity": 2}, nil), http.StatusNotFound, "not_found")
		requireCoded(t, e.deleteGift(token, g.ID, nil), http.StatusNotFound, "not_found")
		require.Empty(t, decode[giftList](t, e.listGifts(token, "")).Data, name)
		require.Zero(t, decode[giftList](t, e.listGifts(token, "")).TotalCount, name)
	}
	requireCoded(t, e.getGift(f.sue, "999"), http.StatusNotFound, "not_found")
	requireCoded(t, e.getGift(f.sue, "x"), http.StatusNotFound, "not_found")

	got := decode[giftView](t, e.getGift(f.sue, g.ID))
	require.Equal(t, g.ID, got.ID)
	res := e.getGift(f.sue, g.ID)
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	require.NotContains(t, readBody(t, e.getItem(f.mom, kettle.ID)), "gift", "the recipient's item says nothing of gifts")
}

func TestTheGiftsOfAGiverFilterAndSort(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	socks := e.addItem(f.mom, f.momList, "Socks", map[string]any{"is_unlimited": true})
	grill := e.addItem(f.mom, f.house, "Grill")
	birthday := e.birthdayOf(f.sue, f.momID)

	late := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": birthday})
	soon := e.claim(f.sue, socks.ID, map[string]any{"occasion_id": advent.ID})
	house := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": birthday})
	tess := e.giver("Tess", f)
	e.claim(tess, socks.ID, nil)

	ids := func(token, query string) []string { return e.giftIDs(decode[giftList](t, e.listGifts(token, query))) }

	// By reveal moment, soonest first, and the ID last.
	require.Equal(t, []string{soon.ID, late.ID, house.ID}, ids(f.sue, ""))
	require.Equal(t, []string{house.ID, late.ID, soon.ID}, ids(f.sue, "sort=-reveal_at"))
	require.Equal(t, 3, decode[giftList](t, e.listGifts(f.sue, "")).TotalCount, "Tess's gift is not Sue's")
	// Giving is the open gifts of the caller. me is the caller.
	require.Equal(t, []string{soon.ID, late.ID, house.ID}, ids(f.sue, "giver_id=me&state=open"))
	require.Equal(t, []string{soon.ID, late.ID, house.ID}, ids(f.sue, "giver_id="+f.sueID))
	require.Empty(t, ids(f.sue, "giver_id="+f.momID), "a giver sees only their own")
	require.Empty(t, ids(f.sue, "state=given"))
	require.Equal(t, []string{late.ID}, ids(f.sue, "item_id="+kettle.ID))
	require.Equal(t, []string{soon.ID, late.ID}, ids(f.sue, "list_id="+f.momList))
	require.Equal(t, []string{house.ID}, ids(f.sue, "list_id="+f.house))
	require.Equal(t, []string{late.ID, house.ID}, ids(f.sue, "occasion_id="+birthday))
	require.Equal(t, []string{soon.ID}, ids(f.sue, "occasion_id="+advent.ID))
	require.Equal(t, []string{late.ID}, ids(f.sue, "item_id="+kettle.ID+"&occasion_id="+birthday+"&list_id="+f.momList))
	require.Empty(t, ids(f.sue, "item_id=x"), "an ID that is nobody's has no gifts")

	// A given gift is state=given, and an open one is not.
	require.Equal(t, 2, e.revealAt("2026-12-07T00:00:00Z"), "Advent is over: Sue's socks and Tess's")
	require.Equal(t, []string{soon.ID}, ids(f.sue, "state=given"))
	require.Equal(t, []string{late.ID, house.ID}, ids(f.sue, "state=open"))
	given := decode[giftView](t, e.getGift(f.sue, soon.ID))
	require.Equal(t, "given", given.State)
	require.NotNil(t, given.GivenAt)
	require.False(t, given.CanEdit, "a given gift is the record of what was given")
	require.False(t, given.NeedsAttention)
	require.Equal(t, []string{soon.ID, late.ID, house.ID}, ids(f.sue, "sort=given_at"), "an open gift has no given_at, and is last")
	require.Equal(t, []string{soon.ID, house.ID, late.ID}, ids(f.sue, "sort=-given_at"), "the ID follows the direction, and an open gift is still last")
}

func TestGiftsPageWithACursorAndRefuseWhatTheyDoNotKnow(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	var want []string
	for _, title := range []string{"A", "B", "C"} {
		it := e.addItem(f.mom, f.momList, title)
		want = append(want, e.claim(f.sue, it.ID, nil).ID)
	}

	first := decode[giftList](t, e.listGifts(f.sue, "limit=2"))
	require.Equal(t, want[:2], e.giftIDs(first))
	require.NotNil(t, first.NextCursor)
	require.Equal(t, 3, first.TotalCount)
	second := decode[giftList](t, e.listGifts(f.sue, "limit=2&cursor="+*first.NextCursor))
	require.Equal(t, want[2:], e.giftIDs(second))
	require.Nil(t, second.NextCursor)

	for name, tc := range map[string]struct{ query, code string }{
		"an unknown filter":  {"mood=red", "unknown_filter"},
		"an unknown sort":    {"sort=created_at", "unknown_sort"},
		"an unknown state":   {"state=lost", "invalid_input"},
		"a cursor of a sort": {"sort=given_at&cursor=" + *first.NextCursor, "cursor_mismatch"},
	} {
		res := e.listGifts(f.sue, tc.query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	requireCoded(t, e.do(request{path: "/gifts"}), http.StatusUnauthorized, "unauthorized")
}

func TestAGiftNeedsAttentionWhenItIsNotBoughtAndTheRevealIsNear(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	soon := e.addOccasion(f.mom, "Soon", once(2026, 11, 20), "")
	far := e.addOccasion(f.mom, "Far", once(2026, 12, 20), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 4})

	near := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": soon.ID})
	later := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": far.ID})
	bought := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID), "is_bought": true})

	require.True(t, near.NeedsAttention, "revealed in 13 days and not bought")
	require.False(t, later.NeedsAttention, "revealed in 43 days")
	require.False(t, bought.NeedsAttention)
	require.True(t, decode[giftView](t, e.getGift(f.sue, near.ID)).NeedsAttention)
}

func TestTheAvatarOfWhoAddedAnItemOnlyLoadsWhileTheGiverSeesAnItemTheyAdded(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grandmaID, grandma := e.otherUser("Grandma")
	e.linkParents(f.admin, f.dadID, grandmaID)
	img := e.uploaded(grandma)
	require.Equal(t, http.StatusOK, e.setAvatar(grandma, grandmaID, img.ID).StatusCode)
	rug := e.addItem(grandma, f.house, "Rug")
	lamp := e.addItem(grandma, f.house, "Lamp")
	tess := e.giver("Tess", f)
	require.Equal(t, http.StatusOK, e.getImage(tess, img.ID).StatusCode, "Tess sees what Grandma added")

	e.claim(f.sue, rug.ID, nil)
	require.Equal(t, http.StatusOK, e.getImage(tess, img.ID).StatusCode, "the lamp is still shown to Tess")
	e.claim(f.sue, lamp.ID, nil)

	require.Equal(t, http.StatusNotFound, e.getImage(tess, img.ID).StatusCode, "every item Grandma added is fully claimed")
	require.Equal(t, http.StatusOK, e.getImage(f.sue, img.ID).StatusCode, "Sue holds gifts on them")
	require.Equal(t, http.StatusOK, e.getImage(f.mom, img.ID).StatusCode, "the recipients see every item")
}
