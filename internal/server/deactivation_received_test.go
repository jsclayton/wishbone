package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the gifts given to a deactivated user (docs/spec.md,
// Deactivation and deletion, A deactivated recipient and Who acts while a user is
// deactivated; Gifts, Reveal). A gift kept on their list is still for them, and
// their timezone counts at reveal. Their parent still sees the gifts, and can't
// restore them or add them again until the user is reactivated.

// emmasShared makes Emma, Mom's child, and an extra list "Shared" for Mom and
// Emma. Sue sees it through Mom.
func (e *env) emmasShared(f family) (emmaID, shared string) {
	e.t.Helper()
	emmaID, _ = e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	return emmaID, e.extraList("Shared", f.momID, emmaID)
}

func TestAGiftKeptOnTheListOfADeactivatedUserIsGivenAtTheirMidnightToo(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, shared := e.emmasShared(f)
	e.setZone(f.mom, f.momID, "America/New_York")
	e.setZone(f.admin, emmaID, "America/Los_Angeles")
	kettle := e.addItem(f.mom, shared, "Kettle")
	g := e.claim(f.sue, kettle.ID, map[string]any{"occasion_id": e.birthdayOf(f.sue, f.momID)})
	require.Equal(t, "2027-01-02T08:00:00Z", g.RevealAt, "00:00 on Jan 2 in Los Angeles, the later of the two")
	e.deactivated(f.admin, emmaID)
	require.Equal(t, "2027-01-02T08:00:00Z", e.gift(f.sue, g.ID).RevealAt, "her timezone still counts")

	require.Zero(t, e.revealAt("2027-01-02T05:00:00Z"), "midnight in New York is not enough")
	require.Equal(t, 1, e.revealAt("2027-01-02T08:00:00Z"))

	// The gift is for her too: her parent sees it as given to her, and so does Mom.
	given := e.gift(f.mom, g.ID)
	require.Equal(t, []string{f.momID, emmaID}, summaryIDs(anySlice(given.Recipients)))
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.mom, "recipient_id="+emmaID))))
	// After a reactivation it is in Emma's own Received.
	require.Equal(t, http.StatusOK, e.reactivate(f.admin, emmaID).StatusCode)
	emma := e.signInAs(f.admin, emmaID)
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(emma, "recipient_id=me"))))
}

func TestAParentThroughADeactivatedChildSeesTheirGiftsButCannotRestoreOrAddThemAgain(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	zedID, zed := e.otherUser("Zed")
	e.join(f.admin, e.newGroup(f.admin, "Friends").ID, f.sueID, emmaID, zedID)
	wishes := e.extraList("Wishes", emmaID, zedID)
	cup := e.addItem(f.mom, wishes, "Cup", map[string]any{"price_minor": 900})
	g := e.claim(f.sue, cup.ID, nil)
	e.deactivated(f.admin, emmaID)
	// The gift stays on the list, and is given to both of them.
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))
	zedsList := e.primaryListFor(zed, zedID)

	// Mom still sees what was given to her child.
	given := e.gift(f.mom, g.ID)
	require.Equal(t, "given", given.State)
	require.ElementsMatch(t, []string{emmaID, zedID}, summaryIDs(anySlice(given.Recipients)))
	require.Equal(t, []string{g.ID}, e.giftIDs(decode[giftList](t, e.listGifts(f.mom, "recipient_id="+emmaID))))
	// But her powers through Emma stop: can_restore and can_add_again, and both routes.
	require.False(t, given.CanRestore)
	require.False(t, given.CanAddAgain)
	requireDeactivated(t, e.restore(f.mom, g.ID))
	requireDeactivated(t, e.addAgain(f.mom, f.momList, g.ID))
	require.Equal(t, "given", e.gift(f.mom, g.ID).State, "nothing changed")
	// A stranger still gets a 404 and the giver a forbidden: the codes say nothing new.
	requireCoded(t, e.restore(f.hedy, g.ID), http.StatusNotFound, "not_found")
	requireCoded(t, e.restore(f.sue, g.ID), http.StatusForbidden, "forbidden")
	requireCoded(t, e.addAgain(f.sue, e.primaryListFor(f.sue, f.sueID), g.ID), http.StatusForbidden, "reference_not_allowed")
	// Zed is active, so he receives it with the powers of a recipient.
	zedsView := e.gift(zed, g.ID)
	require.True(t, zedsView.CanRestore)
	require.True(t, zedsView.CanAddAgain)
	require.Equal(t, http.StatusCreated, e.addAgain(zed, zedsList, g.ID).StatusCode)

	// A reactivation gives Mom her powers back.
	require.Equal(t, http.StatusOK, e.reactivate(f.admin, emmaID).StatusCode)
	require.True(t, e.gift(f.mom, g.ID).CanRestore)
	require.True(t, e.gift(f.mom, g.ID).CanAddAgain)
	require.Equal(t, http.StatusCreated, e.addAgain(f.mom, f.momList, g.ID).StatusCode)
	require.Equal(t, http.StatusNoContent, e.restore(f.mom, g.ID).StatusCode)
}

func TestAGiftOnlyADeactivatedUserReceivesIsOneNoParentCanRestore(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID, f.dadID)
	e.join(f.admin, e.newGroup(f.admin, "Friends").ID, f.sueID, emmaID)
	wishes := e.extraList("Wishes", emmaID)
	book := e.addItem(f.mom, wishes, "Book")
	g := e.claim(f.sue, book.ID, nil)
	e.deactivated(f.admin, emmaID)
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	for name, parent := range map[string][2]string{"Mom": {f.mom, f.momList}, "Dad": {f.dad, f.dadList}} {
		token, own := parent[0], parent[1]
		got := e.gift(token, g.ID)
		require.False(t, got.CanRestore, name)
		require.False(t, got.CanAddAgain, name)
		requireDeactivated(t, e.restore(token, g.ID))
		requireDeactivated(t, e.addAgain(token, own, g.ID))
	}
	// The admin sees Emma, and isn't her parent: the gift is a 404 for them.
	requireCoded(t, e.getGift(f.admin, g.ID), http.StatusNotFound, "not_found")
}

func TestAParentThroughADeactivatedChildCannotTellAnOpenGiftFromNoGift(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	e.join(f.admin, e.newGroup(f.admin, "Friends").ID, f.sueID, emmaID)
	book := e.addItem(f.mom, e.extraList("Wishes", emmaID), "Book")
	open := e.claim(f.sue, book.ID, nil)
	e.deactivated(f.admin, emmaID)

	for _, id := range []string{open.ID, "999999"} { // an open gift, and no gift
		requireCoded(t, e.restore(f.mom, id), http.StatusNotFound, "not_found")
		requireCoded(t, e.getGift(f.mom, id), http.StatusNotFound, "not_found")
		requireCoded(t, e.addAgain(f.mom, f.momList, id), http.StatusUnprocessableEntity, "reference_not_found")
	}
}
