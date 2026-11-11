package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/user"
)

// These tests cover what deactivating and reactivating a user does to gifts
// (docs/spec.md, Deactivation and deletion, Deactivation and A deactivated
// recipient; Gifts, Changes while a gift is open). The clock starts on
// 2026-11-08, and every birthday is on Jan 1.

// openGiftRows counts the open gifts of a giver, whoever sees them.
func (e *env) openGiftRows(giverID string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM gifts WHERE giver_id = ? AND given_at IS NULL`, giverID))
	return n
}

// seen is what a viewer reads at a path: the body and the ETag. Two reads that
// are equal show the viewer nothing that changed.
type seen struct{ status, etag, body string }

func (e *env) seen(token, path string) seen {
	e.t.Helper()
	res := e.do(request{path: path, header: cookie(token)})
	return seen{status: res.Status, etag: res.Header.Get("ETag"), body: readBody(e.t, res)}
}

func TestDeactivatingAGiverReleasesTheirOpenGiftsAndKeepsTheOnesTheyGave(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	kettle := e.addItem(f.mom, f.house, "Kettle", map[string]any{"quantity": 2})
	tess := e.giver("Tess", f)
	given := e.given(f.sue, grill.ID, nil)
	theirs := e.claim(tess, grill.ID, nil)
	open := e.claim(f.sue, kettle.ID, map[string]any{"quantity": 2})
	require.Equal(t, 1, e.openGiftRows(f.sueID))
	requireCoded(t, e.getItem(tess, kettle.ID), http.StatusNotFound, "not_found") // fully claimed

	e.deactivated(f.admin, f.sueID)

	// The open gift is deleted, and its quantity is back for every giver.
	require.Zero(t, e.openGiftRows(f.sueID))
	var rows int
	require.NoError(t, e.db.GetContext(t.Context(), &rows, `SELECT count(*) FROM gifts WHERE id = ?`, open.ID))
	require.Zero(t, rows)
	require.Equal(t, 2, *decode[itemView](t, e.getItem(tess, kettle.ID)).StillWanted)
	// What she gave stays in Received, and another giver's gift stays.
	received := decode[giftList](t, e.listGifts(f.mom, "recipient_id=me"))
	require.Equal(t, []string{given.ID}, e.giftIDs(received))
	require.Equal(t, http.StatusOK, e.getGift(tess, theirs.ID).StatusCode)
	require.Equal(t, 2, decode[itemView](t, e.getItem(f.mom, grill.ID)).Quantity, "the given one left the item, nothing else changed")
}

func TestDeactivatingAGiverShowsTheirRecipientsNothingOfClaims(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2, "image_ids": imageIDs(photo)})
	kettle := e.addItem(f.mom, f.house, "Kettle", map[string]any{"quantity": 1})
	e.claim(f.sue, grill.ID, map[string]any{"quantity": 2})
	e.claim(f.sue, kettle.ID, nil)
	reads := func() map[string]seen {
		out := map[string]seen{}
		for name, token := range map[string]string{"Mom": f.mom, "Dad": f.dad} {
			out[name+" house"] = e.seen(token, "/lists/"+f.house)
			out[name+" items"] = e.seen(token, "/lists/"+f.house+"/items")
			out[name+" grill"] = e.seen(token, "/items/"+grill.ID)
			out[name+" kettle"] = e.seen(token, "/items/"+kettle.ID)
			out[name+" photo"] = e.seen(token, "/images/"+photo.ID)
			out[name+" gifts"] = e.seen(token, "/gifts")
		}
		return out
	}
	before := reads()

	e.deactivated(f.admin, f.sueID)

	after := reads()
	for name, was := range before {
		require.Equal(t, was, after[name], "%s: a recipient reads the same body, ETag and image", name)
	}
	// Nor does it show in the code of a refusal: a recipient's claim is refused the same way.
	requireCoded(t, e.postGift(f.mom, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	requireCoded(t, e.postGift(f.mom, kettle.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
}

func TestDeactivatingARecipientShowsTheOtherRecipientsNothingOfClaims(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID)
	photo := e.uploaded(f.mom)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3, "image_ids": imageIDs(photo)})
	kettle := e.addItem(f.mom, f.house, "Kettle")
	tess := e.giver("Tess", f)
	// Claims that the deactivation moves, flags, and leaves alone.
	theirs := e.claim(tess, grill.ID, map[string]any{"occasion_id": e.birthdayOf(tess, f.dadID), "quantity": 2})
	e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.dadID)})
	reads := func() map[string]seen {
		return map[string]seen{
			"house":  e.seen(f.mom, "/lists/"+f.house),
			"items":  e.seen(f.mom, "/lists/"+f.house+"/items"),
			"grill":  e.seen(f.mom, "/items/"+grill.ID),
			"kettle": e.seen(f.mom, "/items/"+kettle.ID),
			"photo":  e.seen(f.mom, "/images/"+photo.ID),
			"gifts":  e.seen(f.mom, "/gifts"),
			"mine":   e.seen(f.mom, "/lists?recipient_id="+f.momID),
		}
	}
	before := reads()

	e.deactivated(f.admin, f.dadID)

	after := reads()
	for name, was := range before {
		require.Equal(t, was, after[name], "%s: Mom reads the same body, ETag and image", name)
	}
	// A recipient's claim is refused the same way for a claimed item and an unclaimed one.
	requireCoded(t, e.postGift(f.mom, grill.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	requireCoded(t, e.postGift(f.mom, kettle.ID, nil), http.StatusForbidden, "recipient_cannot_claim")
	// Only the giver learns that anything moved.
	require.Contains(t, flagCodes(e.gift(tess, theirs.ID)), "occasion_moved")
}

func TestDeactivatingARecipientMovesTheGiftsForTheirOccasionsAndFlagsThem(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID) // the cousins now see Dad too
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 5})
	tess := e.giver("Tess", f)
	moms := e.birthdayOf(tess, f.momID)
	forDad := e.claim(tess, grill.ID, map[string]any{"occasion_id": e.birthdayOf(tess, f.dadID)})
	forMom := e.claim(f.sue, grill.ID, map[string]any{"occasion_id": moms})

	e.deactivated(f.admin, f.dadID)

	moved := e.gift(tess, forDad.ID)
	require.Equal(t, moms, moved.Occasion.ID, "Mom's next occasion")
	require.Equal(t, []string{"occasion_moved"}, flagCodes(moved))
	require.Empty(t, e.gift(f.sue, forMom.ID).Flags, "her occasion still applies")
	// Dad is still a recipient: nothing about the list changed.
	require.Equal(t, []string{f.momID, f.dadID}, summaryIDs(anySlice(decode[listView](t, e.getList(f.mom, f.house)).Recipients)))
	// No giver picks Dad's occasions now.
	require.NotContains(t, occasionIDs(t, e, tess, f.house), e.birthdayOf(f.admin, f.dadID))
	// Reactivating changes nothing for the gift: it stays moved and flagged.
	require.Equal(t, http.StatusOK, e.reactivate(f.admin, f.dadID).StatusCode)
	again := e.gift(tess, forDad.ID)
	require.Equal(t, moms, again.Occasion.ID)
	require.Equal(t, []string{"occasion_moved"}, flagCodes(again))
}

// occasionIDs lists the IDs of the occasions a giver can pick on a list.
func occasionIDs(t *testing.T, e *env, token, listID string) []string {
	t.Helper()
	var out []string
	for _, o := range decode[occasionList](t, e.listOccasions(token, "list_id="+listID)).Data {
		out = append(out, o.ID)
	}
	return out
}

func TestAGiverWhoSeesNoActiveRecipientIsFlaggedNoLongerSharedAndCannotClaim(t *testing.T) {
	// The example of docs/spec.md, A deactivated recipient: Grandma is Dad's
	// parent and shares no group with Mom.
	e := newEnv(t)
	f := e.family()
	grandmaID, grandma := e.otherUser("Grandma")
	e.linkParents(f.admin, f.dadID, grandmaID)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 3})
	kettle := e.addItem(f.mom, f.house, "Kettle")
	tools := e.extraList("Dad's tools", f.dadID)
	saw := e.addItem(f.dad, tools, "Saw")
	hers := e.claim(grandma, grill.ID, nil)
	sawGift := e.claim(grandma, saw.ID, nil)
	require.True(t, decode[itemView](t, e.getItem(grandma, kettle.ID)).CanClaim)

	e.deactivated(f.admin, f.dadID)

	for name, id := range map[string]string{"Our house": hers.ID, "Dad's tools": sawGift.ID} {
		got := e.gift(grandma, id)
		require.Equal(t, []string{"no_longer_shared"}, flagCodes(got), name)
		require.Equal(t, "2027-01-01", got.OccasionDate, name+": Dad's birthday still applies to a recipient other than the giver")
		require.Nil(t, got.Occasion, name+": she sees only the reveal date of an occasion she can't see")
	}
	// She still sees the list through her child, but can't claim on it.
	require.Equal(t, http.StatusOK, e.getList(grandma, f.house).StatusCode)
	for _, id := range []string{kettle.ID, grill.ID, saw.ID} {
		got := decode[itemView](t, e.getItem(grandma, id))
		require.False(t, got.CanClaim, id)
	}
	requireDeactivated(t, e.postGift(grandma, kettle.ID, nil))
	// The refusal is the same whether or not the item is claimed all the way.
	e.claim(f.sue, kettle.ID, nil)
	requireDeactivated(t, e.postGift(grandma, kettle.ID, nil))

	// Reactivation leaves the flag. Answering it, and a second deactivation, flags it again.
	require.Equal(t, http.StatusOK, e.reactivate(f.admin, f.dadID).StatusCode)
	require.Equal(t, []string{"no_longer_shared"}, flagCodes(e.gift(grandma, hers.ID)), "an answered flag is the giver's to answer")
	require.True(t, decode[itemView](t, e.getItem(grandma, grill.ID)).CanClaim)
	require.Equal(t, http.StatusOK, e.acknowledge(grandma, hers.ID, nil).StatusCode)
	e.deactivated(f.admin, f.dadID)
	require.Equal(t, []string{"no_longer_shared"}, flagCodes(e.gift(grandma, hers.ID)))
}

func TestAGiverWhoIsARecipientIsNotFlaggedWhenTheOtherRecipientIsDeactivated(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.cousins(f), f.dadID) // Mom sees Dad
	golf := e.extraList("Golf", f.dadID)
	putter := e.addItem(f.dad, golf, "Putter")
	g := e.claim(f.mom, putter.ID, nil)
	e.joinList(f.dad, golf, f.momID, f.mom)

	e.deactivated(f.admin, f.dadID)

	got := e.gift(f.mom, g.ID)
	require.Empty(t, got.Flags, "a giver who is a recipient sees herself")
	require.Equal(t, "2027-01-01", got.OccasionDate, "still an occasion of a recipient other than the giver")
}

// failingObserver stands in for the gift service and fails a change.
type failingObserver struct{}

func (failingObserver) BirthdayChanged(context.Context, *sqlx.Tx, int64) error { return nil }
func (failingObserver) VisibilityChanged(context.Context, *sqlx.Tx, []int64) error {
	return errors.New("settle failed")
}
func (failingObserver) Deactivated(context.Context, *sqlx.Tx, int64) error { return nil }

func TestDeactivationAndReactivationSettleInsideTheirOwnTransaction(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	svc := e.srv.opts.Users
	svc.Observe(failingObserver{})

	_, err := svc.Deactivate(t.Context(), mustParse(t, f.sueID))
	require.Error(t, err)
	require.True(t, decode[userView](t, e.getUser(f.admin, f.sueID)).DeactivatedAt == nil, "the deactivation is undone with the failed settle")
	require.Equal(t, http.StatusOK, e.getUser(f.sue, f.sueID).StatusCode, "and so is the revoking of the sessions")

	svc.Observe(nil)
	e.deactivated(f.admin, f.sueID)
	svc.Observe(failingObserver{})
	_, err = svc.Reactivate(t.Context(), mustParse(t, f.sueID))
	require.Error(t, err)
	require.NotNil(t, decode[userView](t, e.getUser(f.admin, f.sueID)).DeactivatedAt, "the reactivation is undone too")
}

var _ user.Observer = failingObserver{}
