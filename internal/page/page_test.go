package page_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/page"
)

var keys = []page.Key{
	{Name: "name"},
	{Name: "created_at", Time: true},
	{Name: "last_used_at", Descending: true, Time: true},
}

func TestParseTakesTheFirstKeyAndTheDefaultLimitWhenTheRequestSaysNothing(t *testing.T) {
	r, err := page.Parse("users", keys, "", "", 0)

	require.NoError(t, err)
	require.Equal(t, "name", r.Sort)
	require.Equal(t, "name", r.Key)
	require.False(t, r.Desc)
	require.Equal(t, page.DefaultLimit, r.Limit)
	require.Nil(t, r.After)
}

func TestParseRefusesACollectionWithNoName(t *testing.T) {
	// Without a name, two collections with a key of the same name would share
	// cursors.
	_, err := page.Parse("", keys, "", "", 0)

	require.Error(t, err)
	require.NotErrorIs(t, err, page.ErrBadCursor)
	require.NotErrorIs(t, err, page.ErrCursorMismatch)
}

func TestParseReadsTheDirectionOfAKeyAndAMinusReversesIt(t *testing.T) {
	for _, tc := range []struct {
		sort string
		desc bool
	}{
		{"name", false},
		{"-name", true},
		{"last_used_at", true},
		{"-last_used_at", false},
	} {
		r, err := page.Parse("users", keys, tc.sort, "", 0)
		require.NoError(t, err, tc.sort)
		require.Equal(t, tc.desc, r.Desc, tc.sort)
		require.Equal(t, tc.sort, r.Sort, tc.sort)
	}
}

func TestParseRefusesAnUnknownKeyAndAMinusAlone(t *testing.T) {
	for _, sort := range []string{"shade", "-shade", "-", "NAME", "name,created_at", "--name"} {
		_, err := page.Parse("users", keys, sort, "", 0)
		require.ErrorIs(t, err, page.ErrUnknownSort, sort)
	}
}

func TestParseCapsTheLimit(t *testing.T) {
	r, err := page.Parse("users", keys, "", "", 5000)

	require.NoError(t, err)
	require.Equal(t, page.MaxLimit, r.Limit)
	r, _ = page.Parse("users", keys, "", "", 7)
	require.Equal(t, 7, r.Limit)
}

// twoPages parses a request and cuts a first page of one row out of three, and
// returns the cursor for the second.
func twoPages(t *testing.T, collection, sort string) string {
	t.Helper()
	r, err := page.Parse(collection, keys, sort, "", 1)
	require.NoError(t, err)
	rows := []page.Position{{Value: "a", ID: 1}, {Value: "b", ID: 2}, {Value: "c", ID: 3}}
	if r.Key != "name" {
		rows = []page.Position{{Value: page.FormatTime(time.Unix(1, 0)), ID: 1}, {Value: page.FormatTime(time.Unix(2, 0)), ID: 2}}
	}
	got, next := page.Slice(r, rows, func(p page.Position) page.Position { return p })
	require.Len(t, got, 1)
	require.NotNil(t, next)
	return *next
}

func TestACursorLeadsBackToThePositionOfTheLastRowOfItsPage(t *testing.T) {
	cur := twoPages(t, "users", "name")

	r, err := page.Parse("users", keys, "name", cur, 1)

	require.NoError(t, err)
	require.Equal(t, &page.Position{Value: "a", ID: 1}, r.After)
}

func TestACursorBelongsToItsCollectionAndItsSort(t *testing.T) {
	cur := twoPages(t, "users", "name")

	_, err := page.Parse("users", keys, "-name", cur, 1)
	require.ErrorIs(t, err, page.ErrCursorMismatch, "another sort")
	_, err = page.Parse("users", keys, "created_at", cur, 1)
	require.ErrorIs(t, err, page.ErrCursorMismatch, "another key")
	_, err = page.Parse("sessions", keys, "name", cur, 1)
	require.ErrorIs(t, err, page.ErrCursorMismatch, "another collection with a key of the same name")
	_, err = page.Parse("users", keys, "", cur, 1)
	require.NoError(t, err, "the default sort and the sort spelled out are one sort")
}

func TestParseRefusesACursorTheServerDidNotMake(t *testing.T) {
	for name, cur := range map[string]string{
		"not base64":   "!!!",
		"not json":     "bm90IGpzb24",
		"an empty one": "e30",
	} {
		_, err := page.Parse("users", keys, "", cur, 1)
		require.ErrorIs(t, err, page.ErrBadCursor, name)
	}
}

func TestParseRefusesACursorWhoseTimeCannotBeRead(t *testing.T) {
	good := twoPages(t, "users", "created_at")
	r, err := page.Parse("users", keys, "created_at", good, 1)
	require.NoError(t, err)
	require.Equal(t, page.FormatTime(time.Unix(1, 0)), r.After.Value)

	forged := func(sort, value string) string {
		raw, err := json.Marshal(map[string]any{"c": "users", "s": sort, "v": value, "i": 1})
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	_, err = page.Parse("users", keys, "created_at", forged("created_at", "yesterday"), 1)
	require.ErrorIs(t, err, page.ErrBadCursor)
	_, err = page.Parse("users", keys, "-last_used_at", forged("-last_used_at", ""), 1)
	require.ErrorIs(t, err, page.ErrBadCursor)

	// A name key has text values, so any text is a position.
	_, err = page.Parse("users", keys, "name", forged("name", "yesterday"), 1)
	require.NoError(t, err)
}

func TestSliceReturnsEveryRowAndNoCursorOnTheLastPage(t *testing.T) {
	r, err := page.Parse("users", keys, "", "", 3)
	require.NoError(t, err)
	rows := []int{1, 2, 3}

	got, next := page.Slice(r, rows, func(i int) page.Position { return page.Position{Value: "x", ID: int64(i)} })

	require.Equal(t, rows, got)
	require.Nil(t, next)
}

func TestSliceCutsTheExtraRowAndMakesACursor(t *testing.T) {
	r, err := page.Parse("users", keys, "", "", 2)
	require.NoError(t, err)

	got, next := page.Slice(r, []int{1, 2, 3}, func(i int) page.Position { return page.Position{Value: "v", ID: int64(i)} })

	require.Equal(t, []int{1, 2}, got)
	require.NotNil(t, next)
	after, err := page.Parse("users", keys, "", *next, 2)
	require.NoError(t, err)
	require.Equal(t, &page.Position{Value: "v", ID: 2}, after.After)
}

func TestParseNeedsAKey(t *testing.T) {
	_, err := page.Parse("users", nil, "", "", 0)
	require.Error(t, err)
}
