package occasion_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

// It is Nov 8 2026, 12:00 in UTC, which is already Nov 9 in Auckland (UTC+13).
var zoneNow = time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)

// zoneFixture has an admin and two members with birthdays on Nov 8: Grace has no
// timezone, and Hedy lives in London. "Today" is a shared occasion on Nov 8.
type zoneFixture struct {
	svc               *occasion.Service
	admin, grace, hed user.User
	shared            occasion.Occasion
}

func newZoneFixture(t *testing.T, opts ...occasion.Option) zoneFixture {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	clock := func() time.Time { return zoneNow }
	users, svc := user.NewService(st.DB(), clock), occasion.NewService(st.DB(), clock, opts...)

	birthday := user.PartialDate{Month: 11, Day: 8}
	add := func(n user.NewUser) user.User {
		id, err := user.Create(t.Context(), st.DB(), zoneNow, n)
		require.NoError(t, err)
		u, err := users.Get(t.Context(), id)
		require.NoError(t, err)
		return u
	}
	f := zoneFixture{svc: svc}
	f.admin = add(user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 3, Day: 3}, IsAdmin: true})
	f.grace = add(user.NewUser{Name: "Grace", Birthday: birthday})
	f.hed = add(user.NewUser{Name: "Hedy", Birthday: birthday, Timezone: "Europe/London"})
	f.shared, err = svc.CreateShared(t.Context(), f.admin, occasion.NewShared{Name: "Today", Rule: occasion.Annual(time.November, 8), IsEveryone: true})
	require.NoError(t, err)
	return f
}

// birthdayOf is the next date of a user's birthday, as the admin sees it.
func (f zoneFixture) birthdayOf(t *testing.T, u user.User) string {
	t.Helper()
	kind := occasion.TypeBirthday
	got, _, _, err := f.svc.Visible(t.Context(), f.admin, occasion.Filter{UserID: &u.ID, Type: &kind}, page.Request{Key: "next_date", Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	return nextDate(t, got[0])
}

// sharedFor is the next date of the shared occasion, as the caller sees it.
func (f zoneFixture) sharedFor(t *testing.T, caller user.User) string {
	t.Helper()
	got, err := f.svc.GetVisible(t.Context(), caller, f.shared.ID)
	require.NoError(t, err)
	return nextDate(t, got)
}

func nextDate(t *testing.T, o occasion.Occasion) string {
	t.Helper()
	require.NotNil(t, o.NextDate)
	return o.NextDate.String()
}

func TestWithoutAFallbackZoneAUserWithNoTimezoneIsInUTC(t *testing.T) {
	f := newZoneFixture(t)
	require.Equal(t, "2026-11-08", f.birthdayOf(t, f.grace))
	require.Equal(t, "2026-11-08", f.sharedFor(t, f.grace))
}

func TestAUserWithNoTimezoneUsesTheFallbackZone(t *testing.T) {
	auckland, err := time.LoadLocation("Pacific/Auckland")
	require.NoError(t, err)
	f := newZoneFixture(t, occasion.WithFallbackZone(auckland))

	require.Equal(t, "2027-11-08", f.birthdayOf(t, f.grace), "Grace has none: it is already Nov 9 in Auckland")
	require.Equal(t, "2026-11-08", f.birthdayOf(t, f.hed), "Hedy's own zone wins: it is Nov 8 in London")

	// A shared occasion has no user, so it follows the caller's zone, or the fallback.
	require.Equal(t, "2027-11-08", f.sharedFor(t, f.grace), "Grace is the caller and has none")
	require.Equal(t, "2026-11-08", f.sharedFor(t, f.hed), "Hedy is the caller, in London")
}
