package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/gift"
)

// These tests cover reveal: when a gift is given, what it keeps, who it is for,
// and who sees it afterwards (docs/spec.md, Gifts, Who a gift is for, Who
// sees a gift and Reveal; docs/routes.md, Gift fields and Gift routes). Time
// is the env's clock, and the revealer is the one the scheduler runs.

// revealAt moves the clock to a time, runs the revealer once, and returns how
// many gifts it gave.
func (e *env) revealAt(at string) int {
	e.t.Helper()
	moment, err := time.Parse(time.RFC3339, at)
	require.NoError(e.t, err)
	e.clock.set(moment)
	n, err := e.revealer.Run(e.t.Context())
	require.NoError(e.t, err)
	return n
}

func (e *env) setZone(token, userID, zone string) {
	e.t.Helper()
	res := e.patchUser(token, userID, map[string]any{"timezone": zone}, nil)
	require.Equal(e.t, http.StatusOK, res.StatusCode, "set the timezone of user %s", userID)
}

// rawGift is the JSON of a gift as an object, so a test can tell a key that is
// absent from one that is null.
func (e *env) rawGift(token, id string) map[string]any {
	e.t.Helper()
	res := e.getGift(token, id)
	require.Equal(e.t, http.StatusOK, res.StatusCode, "get gift %s", id)
	return decode[map[string]any](e.t, res)
}

func TestAGiftIsGivenAtTheLatestMidnightAmongItsRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.setZone(f.mom, f.momID, "America/New_York")
	e.setZone(f.dad, f.dadID, "America/Los_Angeles")
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": advent.ID})
	require.Equal(t, "2026-12-07T08:00:00Z", g.RevealAt, "00:00 on Dec 7 in Los Angeles, 03:00 in New York")

	require.Zero(t, e.revealAt("2026-12-07T05:00:00Z"), "midnight in New York: Dad's day has not started")
	require.Equal(t, "open", e.gift(f.sue, g.ID).State)
	require.Zero(t, e.revealAt("2026-12-07T07:59:59Z"))
	require.Equal(t, 1, e.revealAt("2026-12-07T08:00:00Z"))

	got := e.gift(f.sue, g.ID)
	require.Equal(t, "given", got.State)
	require.Equal(t, "2026-12-07T08:00:00Z", *got.GivenAt)
	require.Zero(t, e.revealAt("2026-12-07T09:00:00Z"), "a gift is given once")
	require.Equal(t, "2026-12-07T08:00:00Z", *e.gift(f.sue, g.ID).GivenAt)
}

// The test server's fallback zone is UTC.
func TestARecipientWithNoKnownTimezoneCountsAsTheFallbackZone(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.setZone(f.mom, f.momID, "Pacific/Auckland")
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil) // Jan 1: both birthdays are Jan 1

	// Auckland is ahead of UTC, so Dad's midnight in UTC is the later one.
	require.Equal(t, "2027-01-02T00:00:00Z", g.RevealAt)
	require.Zero(t, e.revealAt("2027-01-01T23:59:59Z"))
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
}

func TestTheServerGivesEveryGiftWhoseMomentPassedWhileItWasDown(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	a := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})
	b := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID)})

	// Nobody ran the revealer for months.
	require.Equal(t, 2, e.revealAt("2027-03-01T00:00:00Z"))

	for _, id := range []string{a.ID, b.ID} {
		require.Equal(t, "given", e.gift(f.sue, id).State)
	}
}

func TestRevealCopiesWhatWasGivenAndLaterChangesDoNotTouchIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{
		"price_minor": 45000, "top_price_minor": 52000, "currency": "USD", "image_ids": imageIDs(photo), "quantity": 2,
	})
	g := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": advent.ID, "from_label": "Emma", "note": "Buy at Costco", "is_bought": true})
	require.Equal(t, 1, e.revealAt("2026-12-07T00:00:00Z"))

	// Dad edits the item and Mom deletes the occasion, and the image of the item is
	// replaced and swept.
	other := e.uploaded(f.mom)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, grill.ID, map[string]any{
		"title": "Big grill", "price_minor": 99900, "top_price_minor": nil, "image_ids": imageIDs(other),
	}, nil).StatusCode)
	require.Equal(t, http.StatusNoContent, e.deleteOccasion(f.mom, advent.ID).StatusCode)
	e.clock.advance(30 * 24 * time.Hour)
	e.sweep()

	for name, token := range map[string]string{"the giver": f.sue, "a recipient": f.mom, "the other recipient": f.dad} {
		got := decode[giftView](t, e.getGift(token, g.ID))
		require.Equal(t, "given", got.State, name)
		require.Equal(t, "Grill", got.Title, name)
		require.Equal(t, "Grill", got.Item.Title, name)
		require.EqualValues(t, 45000, *got.PriceMinor, name)
		require.EqualValues(t, 52000, *got.TopPriceMinor, name)
		require.Equal(t, "USD", *got.Currency, name)
		require.Equal(t, "Advent", *got.OccasionName, name, "the name stays when the occasion is deleted")
		require.Nil(t, got.Occasion, name+": the summary goes with the occasion")
		require.Equal(t, "2026-12-06", got.OccasionDate, name)
		require.Equal(t, "Emma", *got.FromLabel, name)
		require.Equal(t, 1, got.Quantity, name)
		require.NotNil(t, got.Image, name)
		require.NotEqual(t, photo.ID, got.Image.ID, name+": the image is a copy, a new image")
		require.NotEqual(t, other.ID, got.Image.ID, name)
		require.Equal(t, got.Image, got.Item.PrimaryImage, name)
		res := e.getImage(token, got.Image.ID)
		require.Equal(t, http.StatusOK, res.StatusCode, name+": the copy loads for whoever sees the gift")
	}
	require.Equal(t, http.StatusNotFound, e.getImage(f.mom, photo.ID).StatusCode, "the item's own image was replaced and swept")
	// Nobody else loads it.
	for name, token := range map[string]string{"another giver": e.giver("Tess", f), "a stranger": f.hedy, "the admin": f.admin} {
		require.Equal(t, http.StatusNotFound, e.getImage(token, e.gift(f.sue, g.ID).Image.ID).StatusCode, name)
	}
}

func TestAGivenGiftHasTheSameBytesAsTheImageItCopied(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"image_ids": imageIDs(photo), "quantity": 2})
	g := e.claim(f.sue, kettle.ID, nil)
	before := e.gift(f.sue, g.ID)
	require.Equal(t, photo.ID, before.Image.ID, "an open gift shows the item's own image")

	e.revealAt("2027-01-02T00:00:00Z")

	after := e.gift(f.mom, g.ID)
	require.NotEqual(t, photo.ID, after.Image.ID)
	want, err := io.ReadAll(e.getImage(f.mom, photo.ID).Body)
	require.NoError(t, err)
	got, err := io.ReadAll(e.getImage(f.mom, after.Image.ID).Body)
	require.NoError(t, err)
	require.Equal(t, want, got)
	// Deleting the item's image leaves the copy.
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, kettle.ID, map[string]any{"image_ids": []string{}}, nil).StatusCode)
	e.sweep()
	require.Equal(t, http.StatusOK, e.getImage(f.mom, after.Image.ID).StatusCode)
}

func TestWhatARecipientAndAParentSeeOfAGivenGiftNeverHoldsTheGiversFields(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID, f.dadID)
	e.join(f.admin, e.newGroup(f.admin, "Kids").ID, f.sueID, emmaID)
	book := e.addItem(emma, e.primaryListFor(emma, emmaID), "Book")
	g := e.claim(f.sue, book.ID, map[string]any{"note": "Hide it", "is_bought": true, "from_label": "Gran"})

	// Until reveal the gift is a 404 for the recipient and for her parents.
	for _, token := range []string{emma, f.mom, f.dad, f.hedy} {
		requireCoded(t, e.getGift(token, g.ID), http.StatusNotFound, "not_found")
	}
	e.revealAt("2027-01-02T00:00:00Z")

	for name, token := range map[string]string{"Emma": emma, "her mother": f.mom, "her father": f.dad} {
		raw := e.rawGift(token, g.ID)
		for _, key := range []string{"note", "is_bought", "flags", "needs_attention", "created_at"} {
			require.NotContains(t, raw, key, name)
		}
		for _, key := range []string{"id", "state", "quantity", "item", "list", "title", "image", "price_minor", "top_price_minor",
			"currency", "recipients", "giver", "giver_name", "from_label", "occasion", "occasion_name", "occasion_date",
			"reveal_at", "given_at"} {
			require.Contains(t, raw, key, name)
		}
		got := decode[giftView](t, e.getGift(token, g.ID))
		require.Equal(t, "Sue", got.GiverName, name)
		require.Equal(t, f.sueID, got.Giver.ID, name)
		require.Equal(t, "Gran", *got.FromLabel, name)
		require.Equal(t, []string{emmaID}, summaryIDs(anyIDs(got.Recipients)), name)
		require.False(t, got.CanEdit, name)
		require.False(t, got.CanDelete, name)
		require.False(t, got.CanRaiseQuantity, name)
		require.False(t, got.CanAcknowledge, name)
		// Emma's Received lists it, for her parents too, with the version of what the
		// caller sees.
		listed := decode[giftList](t, e.listGifts(token, "recipient_id="+emmaID+"&sort=-given_at"))
		require.Equal(t, []string{g.ID}, e.giftIDs(listed), name)
		require.Equal(t, e.getGift(token, g.ID).Header.Get("ETag"), listed.Data[0].Version, name)
	}
	// The giver keeps what is theirs, and sees no flags on a given gift.
	raw := e.rawGift(f.sue, g.ID)
	require.Equal(t, "Hide it", raw["note"])
	require.Equal(t, true, raw["is_bought"])
	require.Contains(t, raw, "created_at")
	require.NotContains(t, raw, "flags")
	require.NotContains(t, raw, "needs_attention")
	// Strangers still see nothing.
	for _, token := range []string{f.hedy, f.admin, e.giver("Tess", f)} {
		requireCoded(t, e.getGift(token, g.ID), http.StatusNotFound, "not_found")
		require.Empty(t, decode[giftList](t, e.listGifts(token, "")).Data)
	}
}

func anyIDs(v []summaryView) []any { return anySlice(v) }

func TestAGiftIsNeverForItsGiver(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	e.joinList(f.mom, f.house, f.sueID, f.sue)

	e.revealAt("2027-01-02T00:00:00Z")

	got := e.gift(f.sue, g.ID)
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(got.Recipients)))
	require.Empty(t, decode[giftList](t, e.listGifts(f.sue, "recipient_id=me")).Data, "Sue gave it, and she is not one of the people it was given to")
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.mom, "recipient_id=me"))))
}

func TestTheRecipientsAreFixedAtRevealAndStayAfterARecipientLeaves(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill")
	g := e.claim(f.sue, grill.ID, nil)
	e.revealAt("2027-01-02T00:00:00Z")

	require.Equal(t, http.StatusNoContent, e.leave(f.dad, f.house, f.dadID, nil).StatusCode)

	for _, token := range []string{f.mom, f.dad} {
		got := decode[giftView](t, e.getGift(token, g.ID))
		require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(got.Recipients)))
	}
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.dad, "recipient_id=me"))), "it stays in Dad's Received")
}

func TestAGiftOnAListThatWasDeletedIsForItsLastRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	golf := e.extraList("Golf", f.dadID)
	putter := e.addItem(f.dad, golf, "Putter")
	g := e.claim(f.sue, putter.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteList(f.dad, golf, nil).StatusCode)

	e.revealAt("2027-02-01T00:00:00Z")

	for name, token := range map[string]string{"the giver": f.sue, "the recipient": f.dad} {
		got := decode[giftView](t, e.getGift(token, g.ID))
		require.Equal(t, "given", got.State, name)
		require.Equal(t, "Golf", got.List.Name, name+": the gift keeps the list's name")
		require.Equal(t, "Putter", got.Title, name)
	}
	require.Equal(t, []string{f.dadID}, summaryIDs(anySlice(e.gift(f.dad, g.ID).Recipients)))
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.dad, "recipient_id=me"))))
}

func TestTheGivenQuantityLeavesTheItem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 5})
	mug := e.addItem(f.mom, f.momList, "Mug", map[string]any{"quantity": 2})
	socks := e.addItem(f.mom, f.momList, "Socks", map[string]any{"is_unlimited": true})
	tess := e.giver("Tess", f)
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2, "occasion_id": advent.ID})
	e.claim(tess, kettle.ID, map[string]any{"quantity": 1, "occasion_id": e.birthdayOf(tess, f.momID)})
	e.claim(f.sue, socks.ID, map[string]any{"quantity": 7, "occasion_id": advent.ID})
	e.claim(f.sue, mug.ID, map[string]any{"quantity": 2, "occasion_id": advent.ID})
	require.Equal(t, []string{"Kettle", "Mug", "Socks"}, e.order(f.mom, f.momList))

	require.Equal(t, 3, e.revealAt("2026-12-07T00:00:00Z"))

	// Five less two given leaves three, and Tess's open claim of one is still claimed.
	k := decode[itemView](t, e.getItem(f.mom, kettle.ID))
	require.Equal(t, 3, k.Quantity)
	require.Equal(t, 2, *decode[itemView](t, e.getItem(tess, kettle.ID)).StillWanted)
	// An unlimited item stays as it is.
	s := decode[itemView](t, e.getItem(f.mom, socks.ID))
	require.True(t, s.IsUnlimited)
	// The mug had two, and both were given, so it leaves the list.
	requireCoded(t, e.getItem(f.mom, mug.ID), http.StatusNotFound, "not_found")
	require.Equal(t, []string{"Kettle", "Socks"}, e.order(f.mom, f.momList))
	require.Equal(t, 2, decode[listView](t, e.getList(f.mom, f.momList)).ItemCount)
	// Its gift keeps the item's title.
	require.Equal(t, "Mug", decode[giftList](t, e.listGifts(f.mom, "recipient_id=me&sort=-given_at")).Data[0].Title)
	// Positions stay 0 to n-1.
	require.Equal(t, []int{0, 1}, []int{decode[itemView](t, e.getItem(f.mom, kettle.ID)).Position, decode[itemView](t, e.getItem(f.mom, socks.ID)).Position})
}

func TestAGivenGiftCanNotBeUnclaimed(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil)
	e.revealAt("2027-01-02T00:00:00Z")

	requireCoded(t, e.deleteGift(f.sue, g.ID, nil), http.StatusForbidden, "gift_given")
	requireCoded(t, e.deleteGift(f.mom, g.ID, nil), http.StatusForbidden, "gift_given")
	requireCoded(t, e.deleteGift(f.hedy, g.ID, nil), http.StatusNotFound, "not_found")
	requireCoded(t, e.patchGift(f.sue, g.ID, map[string]any{"is_bought": false}, nil), http.StatusForbidden, "forbidden")
	require.Equal(t, "given", e.gift(f.sue, g.ID).State)
}

func TestReceivedAndGivenAreFilteredViewsOfTheSameGifts(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	grill := e.addItem(f.mom, f.house, "Grill")
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	spring := e.addOccasion(f.mom, "Spring", once(2027, 4, 1), "")
	first := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})
	second := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID)})
	open := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": spring.ID})
	ids := func(token, query string) []string { return e.giftIDs(decode[giftList](t, e.listGifts(token, query))) }

	require.Equal(t, 1, e.revealAt("2026-12-07T00:00:00Z"))
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	// Received: given to me, newest first.
	require.Equal(t, []string{second.ID, first.ID}, ids(f.mom, "recipient_id=me&sort=-given_at"))
	require.Equal(t, []string{first.ID, second.ID}, ids(f.mom, "recipient_id=me&sort=given_at"))
	require.Equal(t, []string{second.ID}, ids(f.dad, "recipient_id=me&sort=-given_at"), "the kettle was for Mom alone")
	require.Equal(t, []string{second.ID}, ids(f.mom, "recipient_id="+f.dadID), "the given gifts that have this user among their recipients")
	require.Empty(t, ids(f.mom, "recipient_id="+f.sueID))
	require.Empty(t, ids(f.mom, "recipient_id=x"))
	// Mom doesn't see Sue's open gift, with a filter or without one.
	require.NotContains(t, ids(f.mom, ""), open.ID)
	require.Empty(t, ids(f.mom, "giver_id=me"))
	// Given: the gifts I gave, newest first. Giving: the open ones.
	require.Equal(t, []string{second.ID, first.ID}, ids(f.sue, "giver_id=me&state=given&sort=-given_at"))
	require.Equal(t, []string{open.ID}, ids(f.sue, "giver_id=me&state=open"))
	// A recipient filter finds only given gifts, also for the giver.
	require.Equal(t, []string{first.ID, second.ID}, ids(f.sue, "recipient_id="+f.momID+"&sort=given_at"))
	require.Equal(t, 2, decode[giftList](t, e.listGifts(f.mom, "recipient_id=me")).TotalCount)
}

func TestTheSchedulerGivesWhatPassedWhileTheServerWasDownWhenItStarts(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	g := e.claim(f.sue, kettle.ID, nil)
	e.clock.set(time.Date(2027, time.February, 1, 0, 0, 0, 0, time.UTC))

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.revealer.Schedule(ctx, time.Hour, nil)
	}()
	require.Eventually(t, func() bool { return e.gift(f.sue, g.ID).State == "given" }, 5*time.Second, 10*time.Millisecond,
		"the first run is the catch-up, before the first tick")
	stop()
	<-done
}

func TestAGiverWhoCouldNotSeeTheListAtRevealSeesNeitherItsRecipientsNorItsOccasion(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	g := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": advent.ID})
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	require.Equal(t, 1, e.revealAt("2026-12-07T00:00:00Z"))

	raw := e.rawGift(f.sue, g.ID)
	require.NotContains(t, raw, "recipients", "absent, not null: null would say nobody")
	require.Nil(t, raw["occasion"])
	require.Nil(t, raw["occasion_name"])
	require.Equal(t, "2026-12-06", raw["occasion_date"], "the date still shows")
	require.Empty(t, decode[giftList](t, e.listGifts(f.sue, "recipient_id="+f.momID)).Data, "a filter never finds a gift by a field that is hidden")
	// The recipients see it all.
	seen := decode[giftView](t, e.getGift(f.mom, g.ID))
	require.Equal(t, "Advent", *seen.OccasionName)
	require.Equal(t, advent.ID, seen.Occasion.ID)
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(seen.Recipients)))
}

func TestAGiftKeptAfterItsItemWasDeletedIsGivenWithItsCopyAndWithoutFlags(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2, "price_minor": 3000, "currency": "EUR"})
	g := e.claim(f.sue, kettle.ID, nil)
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, kettle.ID, nil).StatusCode)
	require.Len(t, e.gift(f.sue, g.ID).Flags, 1, "removed")

	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	require.NotContains(t, e.rawGift(f.sue, g.ID), "flags")
	require.Zero(t, e.flagRows(), "a given gift has no flags")
	got := decode[giftView](t, e.getGift(f.mom, g.ID))
	require.Equal(t, "Kettle", got.Title)
	require.EqualValues(t, 3000, *got.PriceMinor)
	require.Equal(t, "EUR", *got.Currency)
	requireCoded(t, e.getItem(f.mom, kettle.ID), http.StatusNotFound, "not_found")
	var quantity int
	require.NoError(t, e.db.GetContext(t.Context(), &quantity, `SELECT quantity FROM items WHERE id = ?`, kettle.ID))
	require.Equal(t, 2, quantity, "a deleted item keeps the quantity it had, so 0 only means that it was all given (restore tells them apart)")
}

func TestTheRevealOfOneRecipientLeavesOtherGiftsAlone(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	mug := e.addItem(f.dad, f.dadList, "Mug", map[string]any{"quantity": 2})
	e.join(f.admin, e.cousins(f), f.dadID)
	a, b := e.claim(f.sue, kettle.ID, nil), e.claim(f.sue, mug.ID, nil)
	momID, err := strconv.ParseInt(f.momID, 10, 64)
	require.NoError(t, err)

	n, err := e.revealer.RunFor(t.Context(), momID, time.Date(2027, time.January, 2, 0, 0, 0, 0, time.UTC))

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "given", e.gift(f.sue, a.ID).State)
	require.Equal(t, "open", e.gift(f.sue, b.ID).State)
	require.Equal(t, time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC), e.clock.now(), "the clock does not move")
}

func TestAGiverWhoCouldNotSeeTheListAtRevealLoadsNoAvatarOfItsRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	momAvatar, dadAvatar, sueAvatar := e.uploaded(f.mom), e.uploaded(f.dad), e.uploaded(f.sue)
	require.Equal(t, http.StatusOK, e.setAvatar(f.mom, f.momID, momAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.setAvatar(f.dad, f.dadID, dadAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.setAvatar(f.sue, f.sueID, sueAvatar.ID).StatusCode)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	e.claim(f.sue, grill.ID, nil)
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	// The gift hides its recipients from Sue, so it gives her no way to their avatars.
	require.Equal(t, http.StatusNotFound, e.getImage(f.sue, momAvatar.ID).StatusCode)
	require.Equal(t, http.StatusNotFound, e.getImage(f.sue, dadAvatar.ID).StatusCode)
	// The recipients still get the giver's, and each other's.
	require.Equal(t, http.StatusOK, e.getImage(f.mom, sueAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.dad, sueAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.dad, momAvatar.ID).StatusCode)
}

func TestAGiverWhoSawTheListAtRevealLoadsTheAvatarsOfItsRecipientsAfterLosingIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	momAvatar, dadAvatar := e.uploaded(f.mom), e.uploaded(f.dad)
	require.Equal(t, http.StatusOK, e.setAvatar(f.mom, f.momID, momAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.setAvatar(f.dad, f.dadID, dadAvatar.ID).StatusCode)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	g := e.claim(f.sue, grill.ID, nil)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)

	require.Contains(t, e.rawGift(f.sue, g.ID), "recipients", "Sue saw the list at reveal")
	require.Equal(t, http.StatusOK, e.getImage(f.sue, momAvatar.ID).StatusCode)
	require.Equal(t, http.StatusOK, e.getImage(f.sue, dadAvatar.ID).StatusCode)
}

// changeAfterFind makes the revealer run fn once, after it has found the open
// gifts and before it gives the first.
func (e *env) changeAfterFind(fn func()) {
	e.t.Helper()
	e.revealer.AfterFind(func() {
		e.revealer.AfterFind(nil)
		fn()
	})
}

func TestRevealGivesTheQuantityTheGiftHasWhenItIsGivenNotWhenItWasFound(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	g := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2})
	e.changeAfterFind(func() {
		require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"quantity": 1}, nil).StatusCode)
	})

	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	require.Equal(t, 1, e.gift(f.sue, g.ID).Quantity)
	require.Equal(t, 2, decode[itemView](t, e.getItem(f.mom, kettle.ID)).Quantity, "one of three left the item, not two")
}

func TestRevealLeavesAGiftOpenWhoseOccasionMovedPastNowWhileItWasBeingFound(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": advent.ID})
	e.changeAfterFind(func() {
		require.Equal(t, http.StatusOK, e.patchGift(f.sue, g.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID)}, nil).StatusCode)
	})

	require.Zero(t, e.revealAt("2026-12-07T00:00:00Z"), "its occasion is now Jan 1")

	require.Equal(t, "open", e.gift(f.sue, g.ID).State)
	require.Equal(t, 2, decode[itemView](t, e.getItem(f.mom, kettle.ID)).Quantity)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"), "and it is given then")
}

func TestRevealGivesAGiftToTheRecipientsOfTheListItMovedToWhileItWasBeingFound(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	g := e.claim(f.sue, grill.ID, nil)
	e.changeAfterFind(func() {
		require.Equal(t, http.StatusOK, e.patchItem(f.dad, grill.ID, map[string]any{"list_id": f.dadList}, nil).StatusCode)
	})

	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	got := decode[giftView](t, e.getGift(f.dad, g.ID))
	require.Equal(t, f.dadList, got.List.ID)
	require.Equal(t, []string{f.dadID}, summaryIDs(anySlice(got.Recipients)), "Dad's list, not Our house")
	requireCoded(t, e.getGift(f.mom, g.ID), http.StatusNotFound, "not_found")
}

// breakGift gives a gift a date that reveal can't read, so that giving it fails.
func (e *env) breakGift(id string) {
	e.t.Helper()
	_, err := e.db.ExecContext(e.t.Context(), `UPDATE gifts SET occasion_date = '2020-bad' WHERE id = ?`, id)
	require.NoError(e.t, err)
}

func TestAGiftThatFailsDoesNotStopTheRunFromGivingTheRest(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	broken := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 1, "occasion_id": e.birthdayOf(f.sue, f.momID)})
	fine := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 1, "occasion_id": advent.ID})
	e.breakGift(broken.ID) // it sorts first, by its date

	e.clock.set(time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC))
	n, err := e.revealer.Run(t.Context())

	require.Equal(t, 1, n, "the gift after the broken one is still given")
	require.Equal(t, "given", e.gift(f.sue, fine.ID).State)
	var givenAt *time.Time
	require.NoError(t, e.db.GetContext(t.Context(), &givenAt, `SELECT given_at FROM gifts WHERE id = ?`, broken.ID))
	require.Nil(t, givenAt, "the broken gift is still open")
	var failed *gift.GiftError
	require.ErrorAs(t, err, &failed)
	wantID, convErr := strconv.ParseInt(broken.ID, 10, 64)
	require.NoError(t, convErr)
	require.Equal(t, wantID, failed.GiftID)
	// The next run tries it again, and fails again without giving anything twice.
	n, err = e.revealer.Run(t.Context())
	require.Zero(t, n)
	require.Error(t, err)
	require.Equal(t, 2, decode[itemView](t, e.getItem(f.mom, kettle.ID)).Quantity, "only the one given gift left the item")
}

// syncBuffer is a log that a test reads while the scheduler writes to it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestTheSchedulerLogsAGiftThatFailsByItsIDAndGivesTheRest(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 3})
	advent := e.addOccasion(f.mom, "Advent", annual(12, 6), "")
	broken := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 1, "occasion_id": e.birthdayOf(f.sue, f.momID), "note": "a private note"})
	fine := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 1, "occasion_id": advent.ID})
	e.breakGift(broken.ID)
	e.clock.set(time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC))
	var log syncBuffer

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.revealer.Schedule(ctx, time.Hour, slog.New(slog.NewTextHandler(&log, nil)))
	}()
	require.Eventually(t, func() bool { return strings.Contains(log.String(), "gave gifts") }, 5*time.Second, 10*time.Millisecond)
	stop()
	<-done

	require.Equal(t, "given", e.gift(f.sue, fine.ID).State)
	require.Contains(t, log.String(), "gift_id="+broken.ID)
	require.NotContains(t, log.String(), "a private note")
	require.NotContains(t, log.String(), "Kettle")
}
