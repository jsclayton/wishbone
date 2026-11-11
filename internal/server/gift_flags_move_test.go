package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover moving an item with open gifts, the attention rule and its
// filter (docs/spec.md, Items, Moving an item; docs/routes.md, Gift fields
// and Gift routes).

func TestMovingAnItemTakesItsGiftsAlongFlaggedMovedWithTheNewListWhenTheGiverSeesIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	mine := e.claim(f.sue, grill.ID, map[string]any{"is_bought": true})
	tess := e.giver("Tess", f)
	theirs := e.claim(tess, grill.ID, nil)

	// Mom edits both lists, and Sue sees Mom's list (and not Dad's).
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"list_id": f.momList}, nil).StatusCode)

	got := e.gift(f.sue, mine.ID)
	require.Equal(t, f.momList, got.List.ID, "the gift follows the item")
	require.Equal(t, []string{"moved"}, flagCodes(got))
	require.Equal(t, map[string]any{"id": f.momList, "name": "Mom"}, flagOf(t, got, "moved")["list"])
	require.True(t, got.CanAcknowledge)
	require.False(t, got.NeedsAttention, "moved alone does not need attention, and the gift is bought")
	require.Equal(t, []string{"moved"}, flagCodes(e.gift(tess, theirs.ID)))

	// A second move replaces the list in the flag.
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"list_id": f.house}, nil).StatusCode)
	flags := e.gift(f.sue, mine.ID).Flags
	require.Len(t, flags, 1)
	require.Equal(t, f.house, flags[0]["list"].(map[string]any)["id"])

	// Acknowledging it clears it.
	require.Empty(t, decode[giftView](t, e.acknowledge(f.sue, mine.ID, map[string]any{"codes": []string{"moved"}})).Flags)
}

func TestTheMovedFlagHasANullListWhenTheGiverCannotSeeTheNewList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)

	// Dad edits "Our house" and his own list. Sue shares no group with him.
	require.Equal(t, http.StatusOK, e.patchItem(f.dad, grill.ID, map[string]any{"list_id": f.dadList}, nil).StatusCode)

	// Sue now sees none of the list's recipients, so the gift is also no longer
	// shared with her, and the server picked its occasion (see gift_recipients_change_test.go).
	require.Contains(t, readBody(t, e.getGift(f.sue, g.ID)), `"flags":[{"code":"moved","list":null},`)
	require.Equal(t, http.StatusNotFound, e.getList(f.sue, f.dadList).StatusCode, "the flag shows nothing that Sue could not see")
}

func TestAMoveAndAnEditTogetherFlagBothAndAMoveOfAnUnclaimedItemFlagsNothing(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	free := e.addItem(f.mom, f.house, "Free")
	g := e.claim(f.sue, grill.ID, nil)

	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{"list_id": f.momList, "title": "Big grill"}, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, free.ID, map[string]any{"list_id": f.momList}, nil).StatusCode)

	got := e.gift(f.sue, g.ID)
	require.ElementsMatch(t, []string{"changed", "moved"}, flagCodes(got))
	require.Contains(t, changes(t, got), "title")
	require.Zero(t, e.flagRows())
}

// flagRows counts the flags of the gifts that are given.
func (e *env) flagRows() int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM gift_flags JOIN gifts ON gifts.id = gift_flags.gift_id WHERE gifts.given_at IS NOT NULL`))
	return n
}

func TestTheFlagsOfTwoGiftsThatMergeAreAddedTogether(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	birthday := e.birthdayOf(f.sue, f.momID)
	staying := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": birthday})
	moving := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": advent.ID})
	e.patchItem(f.mom, grill.ID, map[string]any{"title": "Big grill"}, nil) // both gifts are flagged changed
	e.acknowledge(f.sue, staying.ID, nil)
	e.deleteItem(f.mom, grill.ID, nil) // both are flagged removed
	require.Equal(t, []string{"removed"}, flagCodes(e.gift(f.sue, staying.ID)))
	require.Equal(t, []string{"changed", "removed"}, flagCodes(e.gift(f.sue, moving.ID)))

	res := e.patchGift(f.sue, moving.ID, map[string]any{"occasion_id": birthday}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	merged := decode[giftView](t, res)
	require.Equal(t, staying.ID, merged.ID)
	require.ElementsMatch(t, []string{"removed", "changed"}, flagCodes(merged), "the moving gift's flags are added")
	require.Equal(t, http.StatusNotFound, e.getGift(f.sue, moving.ID).StatusCode)
}

func TestNeedsAttentionFollowsV1sRuleWithTheFlagsAndTheFilterUsesIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	soon := e.addOccasion(f.mom, "Soon", annual(11, 12), "")
	far := e.birthdayOf(f.sue, f.momID)
	mug := e.addItem(f.mom, f.momList, "Mug")
	pot := e.addItem(f.mom, f.momList, "Pot")
	pan := e.addItem(f.mom, f.momList, "Pan")
	jar := e.addItem(f.mom, f.momList, "Jar")
	notBought := e.claim(f.sue, mug.ID, map[string]any{"occasion_id": soon.ID})
	boughtSoon := e.claim(f.sue, pot.ID, map[string]any{"occasion_id": soon.ID, "is_bought": true})
	changed := e.claim(f.sue, pan.ID, map[string]any{"occasion_id": far, "is_bought": true})
	quiet := e.claim(f.sue, jar.ID, map[string]any{"occasion_id": far, "is_bought": true})
	e.patchItem(f.mom, pan.ID, map[string]any{"title": "Big pan"}, nil)

	require.True(t, e.gift(f.sue, notBought.ID).NeedsAttention, "not bought and revealed within 14 days")
	require.False(t, e.gift(f.sue, boughtSoon.ID).NeedsAttention)
	require.True(t, e.gift(f.sue, changed.ID).NeedsAttention, "flagged changed, also when bought")
	require.False(t, e.gift(f.sue, quiet.ID).NeedsAttention)

	yes := decode[giftList](t, e.listGifts(f.sue, "needs_attention=true"))
	require.ElementsMatch(t, []string{notBought.ID, changed.ID}, e.giftIDs(yes))
	require.Equal(t, 2, yes.TotalCount)
	no := decode[giftList](t, e.listGifts(f.sue, "needs_attention=false&state=open"))
	require.ElementsMatch(t, []string{boughtSoon.ID, quiet.ID}, e.giftIDs(no))
	require.Equal(t, 4, decode[giftList](t, e.listGifts(f.sue, "")).TotalCount)
	require.ElementsMatch(t, []string{notBought.ID, changed.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.sue, "giver_id=me&state=open&needs_attention=true"))))

	// Acknowledging the change takes the gift off the list.
	e.acknowledge(f.sue, changed.ID, nil)
	require.Equal(t, []string{notBought.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.sue, "needs_attention=true"))))

	// Only a bool is a value.
	requireCoded(t, e.listGifts(f.sue, "needs_attention=maybe"), http.StatusUnprocessableEntity, "invalid_input")
	// Another user's gifts are not found by it.
	require.Empty(t, decode[giftList](t, e.listGifts(f.mom, "needs_attention=true")).Data)
}
