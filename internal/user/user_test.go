package user_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

func ptr(n int) *int { return &n }

func TestChangesValidateChecksTheTimezone(t *testing.T) {
	zone := func(s string) user.Changes {
		return user.Changes{Timezone: patch.Optional[string]{Set: true, Value: &s}}
	}
	for _, ok := range []string{"Europe/Paris", "America/Los_Angeles", "UTC"} {
		_, err := zone(ok).Validate(now)
		require.NoError(t, err, ok)
	}
	for _, bad := range []string{"Mars/Base", "Local", "", "paris"} {
		_, err := zone(bad).Validate(now)
		require.ErrorIs(t, err, user.ErrInvalid, bad)
	}
	_, err := user.Changes{Timezone: patch.Optional[string]{Set: true}}.Validate(now)
	require.NoError(t, err, "null clears the timezone")
}

var now = time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)

func TestPartialDateValidateChecksOnlyTheCalendar(t *testing.T) {
	for _, tc := range []struct {
		name string
		date user.PartialDate
		ok   bool
	}{
		{"a day in the month", user.PartialDate{Month: 11, Day: 30}, true},
		{"the 31st of a short month", user.PartialDate{Month: 11, Day: 31}, false},
		{"February 29 with no year", user.PartialDate{Month: 2, Day: 29}, true},
		{"February 30 with no year", user.PartialDate{Month: 2, Day: 30}, false},
		{"February 29 in a leap year", user.PartialDate{Month: 2, Day: 29, Year: ptr(2000)}, true},
		{"February 29 in a century that is not a leap year", user.PartialDate{Month: 2, Day: 29, Year: ptr(1900)}, false},
		{"February 29 in a year that is not a leap year", user.PartialDate{Month: 2, Day: 29, Year: ptr(2001)}, false},
		{"month 13", user.PartialDate{Month: 13, Day: 1}, false},
		{"month 0", user.PartialDate{Month: 0, Day: 1}, false},
		{"day 0", user.PartialDate{Month: 1, Day: 0}, false},
		// The limits of a birthday are not the limits of a partial date.
		{"a year before 1900", user.PartialDate{Month: 1, Day: 1, Year: ptr(1899)}, true},
		{"a year in the future", user.PartialDate{Month: 1, Day: 1, Year: ptr(2999)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.date.Validate()
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, user.ErrInvalid)
			}
		})
	}
}

func TestValidateBirthdayAddsTheBirthdayLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		date user.PartialDate
		ok   bool
	}{
		{"no year", user.PartialDate{Month: 11, Day: 9}, true},
		{"February 29 with no year", user.PartialDate{Month: 2, Day: 29}, true},
		{"not in the calendar", user.PartialDate{Month: 2, Day: 30}, false},
		{"1900", user.PartialDate{Month: 1, Day: 1, Year: ptr(1900)}, true},
		{"a year before 1900", user.PartialDate{Month: 12, Day: 31, Year: ptr(1899)}, false},
		{"today", user.PartialDate{Month: 11, Day: 8, Year: ptr(2026)}, true},
		{"a day in the future", user.PartialDate{Month: 11, Day: 9, Year: ptr(2026)}, false},
		{"a year in the future", user.PartialDate{Month: 1, Day: 1, Year: ptr(2027)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := user.ValidateBirthday(tc.date, now)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, user.ErrInvalid)
			}
		})
	}
}

func TestNewUserValidateTrimsAndChecksTheNames(t *testing.T) {
	cleaned, err := user.NewUser{Name: "  Ada ", LastName: " Lovelace ", Birthday: user.PartialDate{Month: 12, Day: 10}}.Validate(now)
	require.NoError(t, err)
	require.Equal(t, "Ada", cleaned.Name)
	require.Equal(t, "Lovelace", cleaned.LastName)

	_, err = user.NewUser{Name: "   ", Birthday: user.PartialDate{Month: 12, Day: 10}}.Validate(now)
	require.ErrorIs(t, err, user.ErrInvalid)
}

func TestCreateThenGet(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	svc := user.NewService(st.DB(), func() time.Time { return now })

	id, err := user.Create(t.Context(), st.DB(), now, user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 2, Day: 29}, IsAdmin: true})
	require.NoError(t, err)

	got, err := svc.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "Ada", got.Name)
	require.Nil(t, got.LastName)
	require.Equal(t, user.PartialDate{Month: 2, Day: 29}, got.Birthday)
	require.True(t, got.IsAdmin)
	require.Nil(t, got.ProfileConfirmedAt)

	_, err = svc.Get(t.Context(), id+1)
	require.ErrorIs(t, err, user.ErrNotFound)
}

func TestThereIsOnlyOneAdmin(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))

	n := user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 1, Day: 1}, IsAdmin: true}
	_, err = user.Create(t.Context(), st.DB(), now, n)
	require.NoError(t, err)
	_, err = user.Create(t.Context(), st.DB(), now, n)
	require.Error(t, err)
}
