package server

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the refusals of POST /items/{id}/gifts, what a giver sees of
// a fully claimed item, and two givers who claim the last one at once.

func requireCoded(t *testing.T, res *http.Response, status int, code string) {
	t.Helper()
	require.Equal(t, status, res.StatusCode)
	require.Equal(t, code, readProblem(t, res).Code)
}

func TestARecipientNeverClaimsOnTheirOwnList(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.dad, f.house, "Grill")

	requireCoded(t, e.postGift(f.mom, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	requireCoded(t, e.postGift(f.dad, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	got := decode[itemView](t, e.getItem(f.mom, grill.ID))
	require.False(t, got.CanClaim)
}

func TestAnItemTheCallerCannotSeeCannotBeClaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")

	for _, token := range []string{f.hedy, f.admin} {
		requireCoded(t, e.postGift(token, kettle.ID, nil), http.StatusNotFound, "not_found")
	}
	requireCoded(t, e.postGift(f.sue, "999", nil), http.StatusNotFound, "not_found")
	requireCoded(t, e.postGift(f.sue, "kettle", nil), http.StatusNotFound, "not_found")
	requireCoded(t, e.do(request{method: http.MethodPost, path: "/items/" + kettle.ID + "/gifts", body: map[string]any{}}), http.StatusUnauthorized, "unauthorized")
}

func TestAClaimIsCheckedAgainstWhatIsStillWanted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	tess := e.giver("Tess", f)

	e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2})
	res := e.postGift(tess, kettle.ID, map[string]any{"quantity": 2})

	requireCoded(t, res, http.StatusConflict, "item_fully_claimed")
	require.Equal(t, 1, *decode[itemView](t, e.getItem(tess, kettle.ID)).StillWanted, "a refused claim takes nothing")
	e.claim(tess, kettle.ID, map[string]any{"quantity": 1})
}

func TestAGiverClaimsAnItemOnceForAnOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 5})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	first := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})

	requireCoded(t, e.postGift(f.sue, kettle.ID, nil), http.StatusConflict, "already_claimed")
	requireCoded(t, e.postGift(f.sue, kettle.ID, map[string]any{"occasion_id": first.Occasion.ID}), http.StatusConflict, "already_claimed")
	second := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID)})
	require.NotEqual(t, first.ID, second.ID, "another occasion is another gift")
	require.Equal(t, 2, *decode[itemView](t, e.getItem(f.sue, kettle.ID)).MyClaimedQuantity)
}

func TestAGiverCannotPickAnOccasionOfARecipientTheyCannotSee(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	dadsBirthday := e.birthdayOf(f.dad, f.dadID)
	fishing := e.addOccasion(f.dad, "Fishing day", annual(11, 21), "")

	for _, id := range []string{dadsBirthday, fishing.ID, "999", "x"} {
		res := e.postGift(f.sue, grill.ID, map[string]any{"occasion_id": id})
		requireCoded(t, res, http.StatusUnprocessableEntity, "occasion_not_allowed")
	}
	// Without a choice, the server picks one of Mom's.
	g := e.claim(f.sue, grill.ID, nil)
	require.Equal(t, e.birthdayOf(f.sue, f.momID), g.Occasion.ID)
}

func TestAClaimsFieldsAreChecked(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})

	for _, body := range []map[string]any{
		{"from_label": strings.Repeat("é", 61)}, // over 60 characters
		{"quantity": 0},
		{"quantity": -2},
		{"note": strings.Repeat("n", 2001)},
	} {
		requireCoded(t, e.postGift(f.sue, kettle.ID, body), http.StatusUnprocessableEntity, "invalid_input")
	}
	g := e.claim(f.sue, kettle.ID, map[string]any{"from_label": strings.Repeat("é", 60), "note": "  "})
	require.Len(t, []rune(*g.FromLabel), 60)
	require.Nil(t, g.Note, "a blank note is no note")
}

func TestAGiverWhoSeesTheListOnlyThroughADeactivatedRecipientCannotClaim(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	emmasList := e.primaryListFor(emma, emmaID)
	book := e.addItem(emma, emmasList, "Book")
	before := decode[itemView](t, e.getItem(f.mom, book.ID))
	require.True(t, before.CanClaim, "Mom is a parent of the recipient, so she is a giver")
	e.deactivate(f.admin, emmaID)

	requireDeactivated(t, e.postGift(f.mom, book.ID, nil))
	after := decode[itemView](t, e.getItem(f.mom, book.ID))
	require.False(t, after.CanClaim, "no giver sees a deactivated recipient, for gifts")
}

func TestAGiverDoesNotSeeAFullyClaimedItemUnlessTheyHoldAGiftOnIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	socks := e.addItem(f.mom, f.momList, "Socks", map[string]any{"is_unlimited": true})
	tess := e.giver("Tess", f)

	e.claim(f.sue, kettle.ID, nil)
	e.claim(f.sue, socks.ID, nil)

	// Sue holds a gift on the kettle, so she still sees it, with nothing wanted.
	mine := decode[itemView](t, e.getItem(f.sue, kettle.ID))
	require.Equal(t, 0, *mine.StillWanted)
	require.False(t, mine.CanClaim, "nothing is still wanted")
	require.Equal(t, []string{"Kettle", "Socks"}, e.order(f.sue, f.momList))
	// Tess doesn't: the item is a 404, left out of the list and its count.
	requireCoded(t, e.getItem(tess, kettle.ID), http.StatusNotFound, "not_found")
	require.Equal(t, []string{"Socks"}, e.order(tess, f.momList), "an unlimited item is never fully claimed")
	require.Equal(t, 1, decode[itemList](t, e.listItems(tess, f.momList, "")).TotalCount)
	require.Equal(t, 1, decode[listView](t, e.getList(tess, f.momList)).ItemCount)
	require.Equal(t, 2, decode[listView](t, e.getList(f.sue, f.momList)).ItemCount)
	// The recipient sees every item, and the claim state of none.
	require.Equal(t, []string{"Kettle", "Socks"}, e.order(f.mom, f.momList))
	require.Equal(t, 2, decode[listView](t, e.getList(f.mom, f.momList)).ItemCount)
	// A claim on the item that Tess no longer sees is a conflict, as if she had lost a race.
	requireCoded(t, e.postGift(tess, kettle.ID, nil), http.StatusConflict, "item_fully_claimed")
}

func TestTwoGiversWhoClaimTheLastOneAtOnceHaveOneWinner(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	tess := e.giver("Tess", f)

	var wg sync.WaitGroup
	results := make([]*http.Response, 2)
	start := make(chan struct{})
	for i, token := range []string{f.sue, tess} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = e.postGift(token, kettle.ID, nil)
		}()
	}
	close(start)
	wg.Wait()

	statuses := []int{results[0].StatusCode, results[1].StatusCode}
	require.ElementsMatch(t, []int{http.StatusCreated, http.StatusConflict}, statuses)
	for _, res := range results {
		if res.StatusCode == http.StatusConflict {
			require.Equal(t, "item_fully_claimed", readProblem(t, res).Code)
		}
	}
	var claimed int
	require.NoError(t, e.db.GetContext(t.Context(), &claimed, `SELECT COALESCE(SUM(quantity), 0) FROM gifts WHERE item_id = ?`, kettle.ID))
	require.Equal(t, 1, claimed, "one kettle is claimed, once")
}
