package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// itemView is a full item as the API shows it, in the fields these tests read.
type itemView struct {
	ID            string       `json:"id"`
	ListID        string       `json:"list_id"`
	Title         string       `json:"title"`
	Notes         *string      `json:"notes"`
	PriceMinor    *int64       `json:"price_minor"`
	TopPriceMinor *int64       `json:"top_price_minor"`
	Currency      *string      `json:"currency"`
	Quantity      int          `json:"quantity"`
	IsUnlimited   bool         `json:"is_unlimited"`
	Position      int          `json:"position"`
	AddedBy       *summaryView `json:"added_by"`
	CreatedAt     string       `json:"created_at"`
	Version       string       `json:"version"`

	StillWanted       *int `json:"still_wanted"`
	MyClaimedQuantity *int `json:"my_claimed_quantity"`

	CanEdit          bool `json:"can_edit"`
	CanDelete        bool `json:"can_delete"`
	CanClaim         bool `json:"can_claim"`
	CanRefreshPrices bool `json:"can_refresh_prices"`

	StoreLinks   []storeLinkView `json:"store_links"`
	Images       []itemImageView `json:"images"`
	PrimaryImage *itemImageView  `json:"primary_image"`
}

type itemList struct {
	Data       []itemView `json:"data"`
	NextCursor *string    `json:"next_cursor"`
	TotalCount int        `json:"total_count"`
}

func (e *env) listItems(token, listID, query string) *http.Response {
	e.t.Helper()
	path := "/lists/" + listID + "/items"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) postItem(token, listID string, body map[string]any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/lists/" + listID + "/items", body: body, header: cookie(token)})
}

// addItem adds an item and fails the test when the server refuses.
func (e *env) addItem(token, listID, title string, more ...map[string]any) itemView {
	e.t.Helper()
	body := map[string]any{"title": title}
	for _, m := range more {
		for k, v := range m {
			body[k] = v
		}
	}
	res := e.postItem(token, listID, body)
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "add %q", title)
	return decode[itemView](e.t, res)
}

func (e *env) getItem(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/items/" + id, header: cookie(token)})
}

func (e *env) patchItem(token, id string, body any, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodPatch, path: "/items/" + id, body: body, header: h})
}

func (e *env) deleteItem(token, id string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodDelete, path: "/items/" + id, header: h})
}

func titles(l itemList) []string {
	out := make([]string, len(l.Data))
	for i, it := range l.Data {
		out[i] = it.Title
	}
	return out
}

// order lists the titles of a list's items in the default order.
func (e *env) order(token, listID string) []string {
	e.t.Helper()
	return titles(decode[itemList](e.t, e.listItems(token, listID, "")))
}

// grace is a user with a primary list and a session.
func (e *env) grace() (token, listID string) {
	e.t.Helper()
	_, token = e.otherUser("Grace")
	return token, e.primaryListOf(token).ID
}

func TestARecipientAddsAnItemToTheirListWithTheServersDefaults(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	res := e.postItem(token, listID, map[string]any{"title": "  A kettle  "})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	it := decode[itemView](t, res)
	require.Equal(t, "/api/v2/items/"+it.ID, res.Header.Get("Location"))
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	require.Equal(t, "A kettle", it.Title, "the title is trimmed")
	require.Equal(t, listID, it.ListID)
	require.Nil(t, it.Notes)
	require.Nil(t, it.PriceMinor)
	require.Nil(t, it.TopPriceMinor)
	require.Nil(t, it.Currency)
	require.Equal(t, 1, it.Quantity, "the quantity is 1 unless the body says")
	require.False(t, it.IsUnlimited)
	require.Equal(t, 0, it.Position)
	require.True(t, it.CanEdit)
	require.True(t, it.CanDelete)
	require.False(t, it.CanClaim, "a recipient never claims")

	again := e.getItem(token, it.ID)
	require.Equal(t, res.Header.Get("ETag"), again.Header.Get("ETag"))
	require.Equal(t, it, decode[itemView](t, again))
	require.Equal(t, 1, decode[listView](t, e.getList(token, listID)).ItemCount, "the list counts its items")
}

func TestAnItemRemembersWhoAddedIt(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	it := e.addItem(token, listID, "Kettle")

	require.NotNil(t, it.AddedBy)
	require.Equal(t, "Grace", it.AddedBy.Name)
	require.Nil(t, it.AddedBy.Avatar)
	me := decode[userView](t, e.do(request{path: "/users/me", header: cookie(token)}))
	require.Equal(t, me.ID, it.AddedBy.ID)

	// It never changes, and no edit can set it.
	res := e.patchItem(token, it.ID, map[string]any{"added_by": nil}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "field_not_writable", readProblem(t, res).Code)
	require.Equal(t, me.ID, decode[itemView](t, e.getItem(token, it.ID)).AddedBy.ID)
}

func TestAnItemKeepsOnWithNoAdderOnceThatUserIsDeleted(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	hedyID, _ := e.otherUser("Hedy")
	_, err := e.db.ExecContext(t.Context(), `UPDATE items SET added_by = ? WHERE id = ?`, hedyID, it.ID)
	require.NoError(t, err)
	_, err = e.db.ExecContext(t.Context(), `DELETE FROM sessions WHERE user_id = ?`, hedyID)
	require.NoError(t, err)
	_, err = e.db.ExecContext(t.Context(), `DELETE FROM users WHERE id = ?`, hedyID)
	require.NoError(t, err)

	got := decode[map[string]any](t, e.getItem(token, it.ID))

	require.Contains(t, got, "added_by")
	require.Nil(t, got["added_by"], "added_by is null once that user is deleted")
}

func TestARecipientNeverGetsStillWantedOrTheirClaimedQuantity(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")

	for name, body := range map[string]map[string]any{
		"create": decode[map[string]any](t, e.postItem(token, listID, map[string]any{"title": "Mug"})),
		"get":    decode[map[string]any](t, e.getItem(token, it.ID)),
		"patch":  decode[map[string]any](t, e.patchItem(token, it.ID, map[string]any{"notes": "blue"}, nil)),
		"list":   decode[map[string]any](t, e.listItems(token, listID, ""))["data"].([]any)[0].(map[string]any),
	} {
		require.NotContains(t, body, "still_wanted", name)
		require.NotContains(t, body, "my_claimed_quantity", name)
		require.Equal(t, false, body["can_claim"], name)
	}
}

func TestItemsGoAtTheEndOfTheListInTheOrderTheyWereAdded(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	var positions []int
	for _, title := range []string{"One", "Two", "Three"} {
		positions = append(positions, e.addItem(token, listID, title).Position)
	}

	require.Equal(t, []int{0, 1, 2}, positions)
	require.Equal(t, []string{"One", "Two", "Three"}, e.order(token, listID))
}

func TestAnItemTakesNotesAPriceARangeAQuantityAndUnlimited(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	it := e.addItem(token, listID, "Grill", map[string]any{
		"notes": "Charcoal, please", "price_minor": 19900, "top_price_minor": 29900, "currency": "EUR", "quantity": 3, "is_unlimited": true,
	})

	require.Equal(t, "Charcoal, please", *it.Notes)
	require.EqualValues(t, 19900, *it.PriceMinor)
	require.EqualValues(t, 29900, *it.TopPriceMinor)
	require.Equal(t, "EUR", *it.Currency)
	require.Equal(t, 3, it.Quantity)
	require.True(t, it.IsUnlimited)
}

func TestAPriceWithNoCurrencyIsInDollarsAndNoPriceHasNoCurrency(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	priced := e.addItem(token, listID, "Pot", map[string]any{"price_minor": 500})
	plain := e.addItem(token, listID, "Lid")

	require.Equal(t, "USD", *priced.Currency)
	require.Nil(t, plain.Currency)
	require.Nil(t, plain.PriceMinor)
}

func TestAnItemThatBreaksARuleIs422InvalidInputAndAddsNothing(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()

	for name, body := range map[string]map[string]any{
		"no title":            {},
		"blank title":         {"title": "   "},
		"long title":          {"title": strings.Repeat("a", 301)},
		"long notes":          {"title": "x", "notes": strings.Repeat("a", 2001)},
		"negative price":      {"title": "x", "price_minor": -1},
		"top below price":     {"title": "x", "price_minor": 500, "top_price_minor": 499},
		"top without price":   {"title": "x", "top_price_minor": 500},
		"currency in lower":   {"title": "x", "price_minor": 1, "currency": "usd"},
		"currency too long":   {"title": "x", "price_minor": 1, "currency": "USDX"},
		"currency, no price":  {"title": "x", "currency": "USD"},
		"zero quantity":       {"title": "x", "quantity": 0},
		"negative quantity":   {"title": "x", "quantity": -2},
		"a position":          {"title": "x", "position": 3},
		"someone else's list": {"title": "x", "list_id": "1"},
	} {
		res := e.postItem(token, listID, body)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, name)
	}
	require.Empty(t, e.order(token, listID))
}

func TestOnlyARecipientAddsToAListAndAHiddenListIs404(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	token, listID := e.grace()

	for name, c := range map[string]struct{ token, list string }{
		"the admin":       {admin, listID},
		"missing list":    {token, "9999"},
		"not an ID":       {token, "abc"},
		"no session":      {"", listID},
		"another session": {"nope", listID},
	} {
		res := e.do(request{method: http.MethodPost, path: "/lists/" + c.list + "/items", body: map[string]any{"title": "x"}, header: cookieOrNone(c.token)})
		if name == "no session" || name == "another session" {
			require.Equal(t, http.StatusUnauthorized, res.StatusCode, name)
			continue
		}
		require.Equal(t, http.StatusNotFound, res.StatusCode, name)
		require.Equal(t, "not_found", readProblem(t, res).Code, name)
	}
	require.Empty(t, e.order(token, listID))
}

func cookieOrNone(token string) http.Header {
	if token == "" {
		return nil
	}
	return cookie(token)
}

func TestAnItemOnAnotherUsersListIs404ToTheAdminAndToStrangers(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	_, hedy := e.otherUser("Hedy")

	for name, other := range map[string]string{"the admin": admin, "a stranger": hedy} {
		require.Equal(t, http.StatusNotFound, e.getItem(other, it.ID).StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.listItems(other, listID, "").StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.patchItem(other, it.ID, map[string]any{"title": "x"}, nil).StatusCode, name)
		require.Equal(t, http.StatusNotFound, e.deleteItem(other, it.ID, nil).StatusCode, name)
	}
	for _, id := range []string{"9999", "abc", "0"} {
		require.Equal(t, http.StatusNotFound, e.getItem(token, id).StatusCode, id)
	}
	require.Equal(t, []string{"Kettle"}, e.order(token, listID), "nothing changed")
}

func TestARecipientEditsAnItemWithAMergePatch(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Grill", map[string]any{"notes": "Charcoal", "price_minor": 100, "top_price_minor": 200, "currency": "USD"})

	res := e.patchItem(token, it.ID, map[string]any{"title": " Smoker ", "notes": nil, "quantity": 2}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, res.Header.Get("ETag"), e.getItem(token, it.ID).Header.Get("ETag"))
	got := decode[itemView](t, res)
	require.Equal(t, "Smoker", got.Title)
	require.Nil(t, got.Notes, "null clears a field that can be cleared")
	require.Equal(t, 2, got.Quantity)
	require.EqualValues(t, 100, *got.PriceMinor, "an absent field doesn't change")
	require.EqualValues(t, 200, *got.TopPriceMinor)

	// Clearing the price clears its range and its currency with it.
	cleared := decode[itemView](t, e.patchItem(token, it.ID, map[string]any{"price_minor": nil, "top_price_minor": nil}, nil))
	require.Nil(t, cleared.PriceMinor)
	require.Nil(t, cleared.TopPriceMinor)
	require.Nil(t, cleared.Currency)
	// The merge-patch content type works too.
	res = e.do(request{method: http.MethodPatch, path: "/items/" + it.ID, body: map[string]any{"is_unlimited": true}, contentType: "application/merge-patch+json", header: cookie(token)})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.True(t, decode[itemView](t, res).IsUnlimited)
}

func TestAnEditThatBreaksARuleChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Grill", map[string]any{"price_minor": 100, "top_price_minor": 200})

	for name, tc := range map[string]struct {
		body map[string]any
		want int
		code string
	}{
		"null title":          {map[string]any{"title": nil}, 422, "invalid_input"},
		"blank title":         {map[string]any{"title": " "}, 422, "invalid_input"},
		"null quantity":       {map[string]any{"quantity": nil}, 422, "invalid_input"},
		"zero quantity":       {map[string]any{"quantity": 0}, 422, "invalid_input"},
		"top below the price": {map[string]any{"price_minor": 300}, 422, "invalid_input"},
		"clear only price":    {map[string]any{"price_minor": nil}, 422, "invalid_input"},
		"negative position":   {map[string]any{"position": -1}, 422, "invalid_input"},
		"no such field":       {map[string]any{"shade": "red"}, 422, "invalid_input"},
		"id":                  {map[string]any{"id": "5"}, 403, "field_not_writable"},
		"created_at":          {map[string]any{"created_at": "2026-01-01T00:00:00Z"}, 403, "field_not_writable"},
		"can_edit":            {map[string]any{"can_edit": false}, 403, "field_not_writable"},
	} {
		res := e.patchItem(token, it.ID, tc.body, nil)
		require.Equal(t, tc.want, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	require.Equal(t, it, decode[itemView](t, e.getItem(token, it.ID)))
}

func TestAnItemsETagGuardsItsEditsAndItsDelete(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "Kettle")
	before := e.getItem(token, it.ID).Header.Get("ETag")
	require.Equal(t, before, decode[itemList](t, e.listItems(token, listID, "")).Data[0].Version, "a row has the same version")

	stale := http.Header{"If-Match": {`"nope"`}}
	res := e.patchItem(token, it.ID, map[string]any{"title": "x"}, stale)
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)
	require.Equal(t, http.StatusPreconditionFailed, e.deleteItem(token, it.ID, stale).StatusCode)
	require.Equal(t, "Kettle", decode[itemView](t, e.getItem(token, it.ID)).Title)

	ok := e.patchItem(token, it.ID, map[string]any{"title": "x"}, http.Header{"If-Match": {before}})
	require.Equal(t, http.StatusOK, ok.StatusCode)
	require.NotEqual(t, before, ok.Header.Get("ETag"))
	require.Equal(t, http.StatusNoContent, e.deleteItem(token, it.ID, http.Header{"If-Match": {ok.Header.Get("ETag")}}).StatusCode)
}

func TestMovingAnItemToAPositionShiftsTheOthers(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	items := map[string]itemView{}
	for _, title := range []string{"A", "B", "C", "D"} {
		items[title] = e.addItem(token, listID, title)
	}
	move := func(title string, position int) []string {
		res := e.patchItem(token, items[title].ID, map[string]any{"position": position}, nil)
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, min(position, 3), decode[itemView](t, res).Position, "the item is at the index it was sent to, or the last")
		return e.order(token, listID)
	}

	require.Equal(t, []string{"D", "A", "B", "C"}, move("D", 0), "to the top")
	require.Equal(t, []string{"A", "B", "C", "D"}, move("D", 3), "to the end")
	require.Equal(t, []string{"A", "C", "B", "D"}, move("B", 2), "down one")
	require.Equal(t, []string{"A", "B", "C", "D"}, move("B", 1), "up one")
	require.Equal(t, []string{"A", "B", "C", "D"}, move("B", 1), "to where it is")
	require.Equal(t, []string{"B", "C", "D", "A"}, move("A", 99), "past the end is the end")

	list := decode[itemList](t, e.listItems(token, listID, ""))
	for i, it := range list.Data {
		require.Equal(t, i, it.Position, "positions are 0 to n-1 with no gaps")
	}
}

func TestDeletingAnItemClosesTheGapAndTheNextItemAddedGoesLast(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	e.addItem(token, listID, "A")
	b := e.addItem(token, listID, "B")
	e.addItem(token, listID, "C")

	require.Equal(t, http.StatusNoContent, e.deleteItem(token, b.ID, nil).StatusCode)

	require.Equal(t, http.StatusNotFound, e.getItem(token, b.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.deleteItem(token, b.ID, nil).StatusCode, "a second delete finds nothing")
	list := decode[itemList](t, e.listItems(token, listID, ""))
	require.Equal(t, []string{"A", "C"}, titles(list))
	require.Equal(t, []int{0, 1}, []int{list.Data[0].Position, list.Data[1].Position})
	require.Equal(t, 2, decode[listView](t, e.getList(token, listID)).ItemCount)
	require.Equal(t, 2, e.addItem(token, listID, "D").Position)
}

func TestAnItemMovesToAnotherListTheCallerCanEdit(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, token := e.otherUser("Grace")
	primary := e.primaryListOf(token).ID
	attic := e.extraList("Attic", graceID)
	a := e.addItem(token, primary, "A")
	b := e.addItem(token, primary, "B")
	e.addItem(token, primary, "C")
	e.addItem(token, attic, "X")

	res := e.patchItem(token, b.ID, map[string]any{"list_id": attic}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	moved := decode[itemView](t, res)
	require.Equal(t, attic, moved.ListID)
	require.Equal(t, 1, moved.Position, "it goes to the end of the other list")
	require.Equal(t, []string{"A", "C"}, e.order(token, primary), "the first list closes the gap")
	require.Equal(t, []string{"X", "B"}, e.order(token, attic))

	// Naming the list the item is on changes nothing.
	same := e.patchItem(token, a.ID, map[string]any{"list_id": primary}, nil)
	require.Equal(t, http.StatusOK, same.StatusCode)
	require.Equal(t, 0, decode[itemView](t, same).Position)
}

func TestAnItemCannotMoveToAListTheCallerCannotSee(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	token, listID := e.grace()
	it := e.addItem(token, listID, "A")
	adminList := e.primaryListOf(admin).ID

	for name, target := range map[string]string{"hidden": adminList, "missing": "9999", "not an ID": "abc"} {
		res := e.patchItem(token, it.ID, map[string]any{"list_id": target}, nil)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "reference_not_found", readProblem(t, res).Code, name)
	}
	require.Equal(t, listID, decode[itemView](t, e.getItem(token, it.ID)).ListID)
	require.Empty(t, e.order(admin, adminList))
}

// price sorts the items of a list by price and returns the titles.
func (e *env) priceOrder(token, listID, sort string) []string {
	e.t.Helper()
	return titles(decode[itemList](e.t, e.listItems(token, listID, "sort="+sort)))
}

func TestItemsSortByPriceByTheLowEndAndByCurrencyThenAmountWithNoPriceLast(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	e.addItem(token, listID, "usd 10", map[string]any{"price_minor": 1000, "currency": "USD"})
	e.addItem(token, listID, "none a")
	e.addItem(token, listID, "eur 50", map[string]any{"price_minor": 5000, "currency": "EUR"})
	e.addItem(token, listID, "usd 5-90", map[string]any{"price_minor": 500, "top_price_minor": 9000, "currency": "USD"})
	e.addItem(token, listID, "jpy 100", map[string]any{"price_minor": 100, "currency": "JPY"})
	e.addItem(token, listID, "none b")

	require.Equal(t, []string{"eur 50", "jpy 100", "usd 5-90", "usd 10", "none a", "none b"}, e.priceOrder(token, listID, "price"),
		"by currency code, then the low end; no price last")
	require.Equal(t, []string{"usd 10", "usd 5-90", "jpy 100", "eur 50", "none b", "none a"}, e.priceOrder(token, listID, "-price"),
		"reversed, and still no price last (the ID tiebreaker reverses)")
}

func TestEverySortPagesToTheSameOrderAsOnePage(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	for i, title := range []string{"d", "a", "c", "none", "b", "none 2", "e"} {
		more := map[string]any{}
		if !strings.HasPrefix(title, "none") {
			more = map[string]any{"price_minor": 100 * (i%3 + 1), "currency": []string{"USD", "EUR"}[i%2]}
		}
		e.clock.advance(1_000_000_000)
		e.addItem(token, listID, title, more)
	}
	e.addItem(token, listID, "same price", map[string]any{"price_minor": 100, "currency": "USD"})
	e.addItem(token, listID, "same price too", map[string]any{"price_minor": 100, "currency": "USD"})

	for _, sort := range []string{"position", "-position", "created_at", "-created_at", "price", "-price"} {
		whole := titles(decode[itemList](t, e.listItems(token, listID, "sort="+sort)))
		var paged []string
		cursor := ""
		for {
			q := "limit=2&sort=" + sort
			if cursor != "" {
				q += "&cursor=" + url.QueryEscape(cursor)
			}
			page := decode[itemList](t, e.listItems(token, listID, q))
			require.Equal(t, 9, page.TotalCount, sort)
			paged = append(paged, titles(page)...)
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		require.Equal(t, whole, paged, sort)
		require.Len(t, whole, 9, sort)
	}
	require.Equal(t, []string{"d", "a", "c", "none", "b", "none 2", "e", "same price", "same price too"}, e.order(token, listID),
		"position is the default")
	require.Equal(t, e.order(token, listID), titles(decode[itemList](t, e.listItems(token, listID, "sort=created_at"))))
	require.Equal(t, "same price too", e.order(token, listID)[8])
	require.Equal(t, "same price too", titles(decode[itemList](t, e.listItems(token, listID, "sort=-position")))[0])
}

func TestItemsRefuseFiltersSortsAndCursorsTheyDoNotKnow(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	e.addItem(token, listID, "A")
	e.addItem(token, listID, "B")
	first := decode[itemList](t, e.listItems(token, listID, "limit=1"))

	for _, tc := range []struct{ query, code string }{
		{"shade=red", "unknown_filter"},
		{"sort=title", "unknown_sort"},
		{"sort=price&cursor=" + url.QueryEscape(*first.NextCursor), "cursor_mismatch"},
		{"cursor=nonsense", "invalid_input"},
		{"limit=0", "invalid_input"},
	} {
		res := e.listItems(token, listID, tc.query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, tc.query)
		require.Equal(t, tc.code, readProblem(t, res).Code, tc.query)
	}
}

func TestACursorOfOneListsItemsDoesNotFitAnotherCollection(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	token, listID := e.grace()
	e.addItem(token, listID, "A")
	e.addItem(token, listID, "B")
	first := decode[itemList](t, e.listItems(token, listID, "limit=1"))

	res := e.listLists(token, "cursor="+url.QueryEscape(*first.NextCursor))

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "cursor_mismatch", readProblem(t, res).Code)
}

func TestTheItemsOfAListDoNotMixWithTheItemsOfAnother(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, token := e.otherUser("Grace")
	primary := e.primaryListOf(token).ID
	attic := e.extraList("Attic", graceID)
	e.addItem(token, primary, "A")
	e.addItem(token, attic, "X")
	e.addItem(token, attic, "Y")

	require.Equal(t, []string{"A"}, e.order(token, primary))
	require.Equal(t, []string{"X", "Y"}, e.order(token, attic))
	require.Equal(t, 1, e.addItem(token, primary, "B").Position, "positions count within the list")
}
