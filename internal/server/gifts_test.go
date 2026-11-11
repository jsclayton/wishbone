package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover claiming an item, which starts a gift, and the claim state
// that items show to givers (docs/spec.md, Gifts; docs/routes.md, Item
// fields and Gifts).

type giftOccasionView struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type giftView struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Quantity int    `json:"quantity"`
	Item     struct {
		ID           string         `json:"id"`
		Title        string         `json:"title"`
		PrimaryImage *itemImageView `json:"primary_image"`
	} `json:"item"`
	List struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"list"`
	Title         string            `json:"title"`
	Image         *itemImageView    `json:"image"`
	PriceMinor    *int64            `json:"price_minor"`
	TopPriceMinor *int64            `json:"top_price_minor"`
	Currency      *string           `json:"currency"`
	Recipients    []summaryView     `json:"recipients"`
	Giver         *summaryView      `json:"giver"`
	GiverName     string            `json:"giver_name"`
	FromLabel     *string           `json:"from_label"`
	Occasion      *giftOccasionView `json:"occasion"`
	OccasionName  *string           `json:"occasion_name"`
	OccasionDate  string            `json:"occasion_date"`
	RevealAt      string            `json:"reveal_at"`
	GivenAt       *string           `json:"given_at"`
	IsBought      bool              `json:"is_bought"`
	Note          *string           `json:"note"`
	Flags         []map[string]any  `json:"flags"`
	CreatedAt     string            `json:"created_at"`
	Version       string            `json:"version"`

	NeedsAttention   bool `json:"needs_attention"`
	CanEdit          bool `json:"can_edit"`
	CanDelete        bool `json:"can_delete"`
	CanRaiseQuantity bool `json:"can_raise_quantity"`
	CanAcknowledge   bool `json:"can_acknowledge"`
	CanRestore       bool `json:"can_restore"`
	CanAddAgain      bool `json:"can_add_again"`
}

type giftList struct {
	Data       []giftView `json:"data"`
	NextCursor *string    `json:"next_cursor"`
	TotalCount int        `json:"total_count"`
}

func (e *env) postGift(token, itemID string, body map[string]any) *http.Response {
	e.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	return e.do(request{method: http.MethodPost, path: "/items/" + itemID + "/gifts", body: body, header: cookie(token)})
}

// claim claims an item and fails the test when the server refuses.
func (e *env) claim(token, itemID string, body map[string]any) giftView {
	e.t.Helper()
	res := e.postGift(token, itemID, body)
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "claim item %s", itemID)
	g := decode[giftView](e.t, res)
	g.Version = res.Header.Get("ETag")
	return g
}

func (e *env) getGift(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/gifts/" + id, header: cookie(token)})
}

func (e *env) listGifts(token, query string) *http.Response {
	e.t.Helper()
	path := "/gifts"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) patchGift(token, id string, body any, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodPatch, path: "/gifts/" + id, body: body, header: h})
}

func (e *env) deleteGift(token, id string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodDelete, path: "/gifts/" + id, header: h})
}

// birthdayOf returns the ID of the birthday occasion of a user, as the viewer
// sees it.
func (e *env) birthdayOf(token, userID string) string {
	e.t.Helper()
	l := decode[occasionList](e.t, e.listOccasions(token, "kind=birthday&user_id="+userID))
	require.Len(e.t, l.Data, 1)
	return l.Data[0].ID
}

func TestAGiverClaimsAnItemForTheNextOccasionOfTheRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3, "price_minor": 4500})

	res := e.postGift(f.sue, kettle.ID, nil)

	require.Equal(t, http.StatusCreated, res.StatusCode)
	g := decode[giftView](t, res)
	require.Equal(t, "/api/v2/gifts/"+g.ID, res.Header.Get("Location"))
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	require.Equal(t, "open", g.State)
	require.Equal(t, 1, g.Quantity, "one unless the body says")
	require.Equal(t, kettle.ID, g.Item.ID)
	require.Equal(t, "Kettle", g.Item.Title)
	require.Equal(t, f.momList, g.List.ID)
	require.Equal(t, "Mom", g.List.Name)
	require.Equal(t, "Kettle", g.Title)
	require.Equal(t, int64(4500), *g.PriceMinor)
	require.Equal(t, "USD", *g.Currency)
	require.Len(t, g.Recipients, 1)
	require.Equal(t, f.momID, g.Recipients[0].ID)
	require.Equal(t, f.sueID, g.Giver.ID)
	require.Equal(t, "Sue", g.GiverName)
	require.Nil(t, g.FromLabel)
	require.Nil(t, g.Note)
	require.False(t, g.IsBought)
	require.Nil(t, g.GivenAt)
	require.NotEmpty(t, g.CreatedAt)
	require.Empty(t, g.Flags, "a new gift has no flags")
	require.NotNil(t, g.Flags, "an empty array, not null")

	// The server picked the first occasion: Mom's birthday on Jan 1, with the
	// reveal at midnight after it, in the fallback zone (UTC here) because Mom has no timezone.
	require.NotNil(t, g.Occasion)
	require.Equal(t, f.birthdayOfMom(e), g.Occasion.ID)
	require.Equal(t, "birthday", g.Occasion.Kind)
	require.Equal(t, "Birthday", *g.OccasionName)
	require.Equal(t, "2027-01-01", g.OccasionDate)
	require.Equal(t, "2027-01-02T00:00:00Z", g.RevealAt)

	require.True(t, g.CanEdit)
	require.True(t, g.CanDelete)
	require.True(t, g.CanRaiseQuantity, "two more are still wanted, and Sue is not a recipient")
	require.False(t, g.CanAcknowledge, "it has no flags")
	require.False(t, g.CanRestore)
	require.False(t, g.CanAddAgain)
}

// birthdayOfMom is Mom's birthday occasion ID, as Sue sees it.
func (f family) birthdayOfMom(e *env) string { return e.birthdayOf(f.sue, f.momID) }

func TestAGiverChoosesTheQuantityOccasionLabelNoteAndBoughtMark(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")

	g := e.claim(f.sue, kettle.ID, map[string]any{
		"quantity": 2, "occasion_id": advent.ID, "from_label": "Emma", "note": "Blue one", "is_bought": true,
	})

	require.Equal(t, 2, g.Quantity)
	require.Equal(t, advent.ID, g.Occasion.ID)
	require.Equal(t, "Advent", *g.OccasionName)
	require.Equal(t, "2026-12-06", g.OccasionDate)
	require.Equal(t, "Emma", *g.FromLabel)
	require.Equal(t, "Blue one", *g.Note)
	require.True(t, g.IsBought)
}

func TestTheRevealIsAtTheLatestMidnightOfTheRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	// Mom is in New York and Dad in Los Angeles: the day after Jan 1 starts
	// last in Los Angeles. Sue sees Mom only, and Dad's zone counts all the same.
	for id, zone := range map[string]string{f.momID: "America/New_York", f.dadID: "America/Los_Angeles"} {
		_, err := e.db.ExecContext(t.Context(), `UPDATE users SET timezone = ? WHERE id = ?`, zone, id)
		require.NoError(t, err)
	}
	grill := e.addItem(f.mom, f.house, "Grill")

	g := e.claim(f.sue, grill.ID, nil)

	require.Equal(t, "2027-01-02T08:00:00Z", g.RevealAt, "00:00 on Jan 2 in Los Angeles")
}

func TestAnItemShowsItsClaimStateToGiversAndNeverToRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})

	before := decode[itemView](t, e.getItem(f.sue, kettle.ID))
	require.NotNil(t, before.StillWanted)
	require.Equal(t, 3, *before.StillWanted)
	require.Equal(t, 0, *before.MyClaimedQuantity)
	require.True(t, before.CanClaim)

	e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2})

	mine := decode[itemView](t, e.getItem(f.sue, kettle.ID))
	require.Equal(t, 1, *mine.StillWanted)
	require.Equal(t, 2, *mine.MyClaimedQuantity)
	require.True(t, mine.CanClaim)
	// Another giver sees how many are still wanted, and nothing of Sue's.
	other := e.giver("Tess", f)
	theirs := decode[itemView](t, e.getItem(other, kettle.ID))
	require.Equal(t, 1, *theirs.StillWanted)
	require.Equal(t, 0, *theirs.MyClaimedQuantity)

	// The recipient gets neither field, and can_claim is false.
	body := readBody(t, e.getItem(f.mom, kettle.ID))
	require.NotContains(t, body, "still_wanted")
	require.NotContains(t, body, "my_claimed_quantity")
	require.Contains(t, body, `"can_claim":false`)
	// The same on the list's page.
	require.NotContains(t, readBody(t, e.listItems(f.mom, f.momList, "")), "still_wanted")
	listed := decode[itemList](t, e.listItems(f.sue, f.momList, ""))
	require.Equal(t, 1, *listed.Data[0].StillWanted)
	require.Equal(t, 2, *listed.Data[0].MyClaimedQuantity)
}

func TestAnUnlimitedItemHasNoLimitToWhatIsStillWanted(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	socks := e.addItem(f.mom, f.momList, "Socks", map[string]any{"is_unlimited": true})

	e.claim(f.sue, socks.ID, map[string]any{"quantity": 40})

	got := decode[itemView](t, e.getItem(f.sue, socks.ID))
	require.Nil(t, got.StillWanted, "null for an unlimited item")
	require.Contains(t, readBody(t, e.getItem(f.sue, socks.ID)), `"still_wanted":null`)
	require.Equal(t, 40, *got.MyClaimedQuantity)
	require.True(t, got.CanClaim)
}

// giver is a user with a session who shares a group with Mom, so they see her
// list as a giver.
func (e *env) giver(name string, f family) string {
	e.t.Helper()
	id, token := e.otherUser(name)
	groups := decode[groupList](e.t, e.listGroups(f.admin, ""))
	for _, g := range groups.Data {
		if g.Name == "Cousins" {
			e.join(f.admin, g.ID, id)
			return token
		}
	}
	e.t.Fatal("no Cousins group")
	return ""
}
