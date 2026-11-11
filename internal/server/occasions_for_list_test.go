package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover GET /occasions?list_id=: the occasions a giver can pick for
// a gift on a list (docs/spec.md, Gifts, Occasions for a gift).

func TestAGiverPicksFromTheOccasionsOfTheRecipientsTheySee(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addOccasion(f.mom, "Anniversary", annual(11, 20), "")
	e.addOccasion(f.dad, "Fishing day", annual(11, 21), "")

	names := func(token, listID string) []string {
		return occasionNames(decode[occasionList](t, e.listOccasions(token, "list_id="+listID)))
	}

	// Sue sees Mom (a shared group) and not Dad, though "Our house" is theirs.
	require.Equal(t, []string{"Anniversary", "Birthday"}, names(f.sue, f.house))
	got := decode[occasionList](t, e.listOccasions(f.sue, "list_id="+f.house))
	require.Equal(t, f.momID, got.Data[0].User.ID, "Dad's occasions are not in the list")
	require.Equal(t, 2, got.TotalCount)

	// A recipient never picks their own occasions: Mom on her own list has none.
	require.Empty(t, names(f.mom, f.momList))
	// Nor on a list she shares: she doesn't see Dad.
	require.Empty(t, names(f.mom, f.house))
	// A list the caller can't see has no occasions to pick.
	require.Empty(t, names(f.hedy, f.momList))
	require.Equal(t, http.StatusOK, e.listOccasions(f.hedy, "list_id="+f.momList).StatusCode)
}

func TestASharedOccasionThatReachesSeveralRecipientsIsListedOnce(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.join(f.admin, e.newGroup(f.admin, "Neighbors").ID, f.dadID, f.sueID)
	e.addShared(f.admin, "Christmas", annual(12, 25), true)

	got := decode[occasionList](t, e.listOccasions(f.sue, "list_id="+f.house))

	require.Equal(t, []string{"Christmas", "Birthday", "Birthday"}, occasionNames(got), "Sue now sees Mom and Dad")
	require.Equal(t, 3, got.TotalCount)
}

func TestTheOccasionsOfAListLeaveOutADeactivatedRecipientAndOnesWithNoNextDate(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addOccasion(f.mom, "Old news", once(2020, 5, 5), "")
	e.deactivate(f.admin, f.momID)

	require.Empty(t, decode[occasionList](t, e.listOccasions(f.sue, "list_id="+f.momList)).Data,
		"no giver can see a deactivated recipient, for gifts")

	e.reactivate(f.admin, f.momID)
	require.Equal(t, []string{"Birthday"}, occasionNames(decode[occasionList](t, e.listOccasions(f.sue, "list_id="+f.momList))),
		"an occasion with no upcoming date can't be picked")
}
