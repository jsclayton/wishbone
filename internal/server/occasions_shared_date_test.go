package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// A shared occasion has no user, so there is no user's "today" for its next date.
// Its next date is worked out where the caller lives (the caller's timezone, the
// server's zone with none; the test server's is UTC), wherever the caller reads
// it: the rule is in docs/routes.md, Occasion fields. A birthday and a custom
// occasion keep the zone of their user.

func TestASharedOccasionsNextDateFollowsTheCallersTimezone(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, hedy := e.otherUser("Hedy")
	e.join(admin, e.newGroup(admin, "Friends").ID, graceID, hedyID)
	// Nov 8 2026 12:00 UTC is already Nov 9 in Auckland (UTC+13).
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"timezone": "Pacific/Auckland"}, nil).StatusCode)
	today := e.addShared(admin, "Today", annual(11, 8), true)

	nextFor := func(token string) string {
		return *decode[occasionView](t, e.getOccasion(token, today.ID)).NextDate
	}
	require.Equal(t, "2026-11-08", nextFor(hedy), "UTC: it is Nov 8")
	require.Equal(t, "2027-11-08", nextFor(grace), "Auckland: Nov 8 has passed")
	require.Equal(t, "2026-11-08", nextFor(admin), "no timezone is the server's zone, UTC here")

	// The same date wherever the caller reads the occasion.
	for name, query := range map[string]string{"the collection": "kind=shared", "a user's occasions": "kind=shared&user_id=" + graceID, "another user's": "kind=shared&user_id=" + hedyID} {
		got := decode[occasionList](t, e.listOccasions(grace, query))
		require.Equal(t, []string{"2027-11-08"}, nextDates(got), "Grace, "+name)
		got = decode[occasionList](t, e.listOccasions(hedy, query))
		require.Equal(t, []string{"2026-11-08"}, nextDates(got), "Hedy, "+name)
	}
	// So Hedy reading Grace's occasions learns nothing of Grace's zone, and the
	// next_date_before filter and the sort follow the same date.
	require.Equal(t, []string{"Today"}, occasionNames(decode[occasionList](t, e.listOccasions(hedy, "kind=shared&next_date_before=2026-11-08"))))
	require.Empty(t, decode[occasionList](t, e.listOccasions(grace, "kind=shared&next_date_before=2026-11-08")).Data)
}

func TestABirthdayAndACustomOccasionKeepTheirUsersTimezone(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	hedyID, hedy := e.otherUser("Hedy")
	e.join(admin, e.newGroup(admin, "Friends").ID, graceID, hedyID)
	require.Equal(t, http.StatusOK, e.patchUser(grace, graceID, map[string]any{"timezone": "Pacific/Auckland"}, nil).StatusCode)
	custom := e.addOccasion(grace, "Today", annual(11, 8), "")

	require.Equal(t, "2027-11-08", *decode[occasionView](t, e.getOccasion(grace, custom.ID)).NextDate)
	require.Equal(t, "2027-11-08", *decode[occasionView](t, e.getOccasion(hedy, custom.ID)).NextDate, "Hedy is in UTC, and the date is still Grace's")
}
