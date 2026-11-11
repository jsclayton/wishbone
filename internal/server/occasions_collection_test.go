package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover GET /occasions: its filters, its two sorts, and its pages
// (docs/routes.md, Occasion routes).

// graceWithOccasions makes Grace, whose birthday is Jan 1, with custom occasions
// on a fixed clock (Nov 8 2026): Advent (Dec 6), Zeta day (Dec 6), Old news (a
// one-time date in the past), and Mid (Mar 3 2027, as an annual rule).
func (e *env) graceWithOccasions() (id, token string) {
	e.t.Helper()
	id, token = e.otherUser("Grace")
	e.addOccasion(token, "Zeta day", annual(12, 6), "")
	e.addOccasion(token, "Advent", annual(12, 6), "")
	e.addOccasion(token, "Old news", once(2020, 5, 5), "")
	e.addOccasion(token, "Mid", annual(3, 3), "")
	return id, token
}

func TestOccasionsSortByNextDateWithNoneLastThenByName(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.graceWithOccasions()

	byDate := decode[occasionList](t, e.listOccasions(grace, ""))

	require.Equal(t, []string{"Advent", "Zeta day", "Birthday", "Mid", "Old news"}, occasionNames(byDate), "the default is next_date, then name")
	require.Equal(t, []string{"2026-12-06", "2026-12-06", "2027-01-01", "2027-03-03", "none"}, nextDates(byDate))
	require.Equal(t, 5, byDate.TotalCount)
	require.Equal(t, []string{"Mid", "Birthday", "Zeta day", "Advent", "Old news"}, occasionNames(decode[occasionList](t, e.listOccasions(grace, "sort=-next_date"))),
		"latest first, and an occasion with no next date is last in both directions")
	require.Equal(t, []string{"Advent", "Birthday", "Mid", "Old news", "Zeta day"}, occasionNames(decode[occasionList](t, e.listOccasions(grace, "sort=name"))))
	require.Equal(t, []string{"Zeta day", "Old news", "Mid", "Birthday", "Advent"}, occasionNames(decode[occasionList](t, e.listOccasions(grace, "sort=-name"))))
}

func TestOccasionsFilterByUserKindAndNextDate(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	e.addOccasion(f.mom, "Wedding day", annual(5, 4), "")
	e.addOccasion(f.mom, "Soon", once(2026, 11, 20), "")
	e.addOccasion(f.sue, "Sue's day", annual(11, 10), "")

	names := func(query string) []string {
		return occasionNames(decode[occasionList](t, e.listOccasions(f.sue, query)))
	}

	require.Equal(t, []string{"Soon", "Birthday", "Wedding day"}, names("user_id="+f.momID))
	require.Equal(t, []string{"Birthday"}, names("kind=birthday&user_id="+f.sueID))
	require.Equal(t, []string{"Sue's day", "Soon", "Wedding day"}, names("kind=custom"))
	require.Empty(t, names("kind=shared"), "this fixture has no shared occasion")
	require.Empty(t, names("user_id="+f.dadID), "Sue can't see Dad")
	require.Empty(t, names("user_id=abc"), "an ID that is no user's has no occasions")

	// next_date_before keeps the dates on or before it, and never an occasion with none.
	require.Equal(t, []string{"Sue's day", "Soon"}, names("next_date_before=2026-11-20"))
	require.Equal(t, []string{"Sue's day"}, names("next_date_before=2026-11-19"))
	require.Empty(t, names("next_date_before=2026-11-09"))
	require.Equal(t, []string{"Sue's day"}, names("next_date_before=2026-11-19&kind=custom&user_id="+f.sueID))
	filtered := decode[occasionList](t, e.listOccasions(f.sue, "next_date_before=2026-11-20"))
	require.Equal(t, 2, filtered.TotalCount, "the total follows the filter")
}

func TestAnOccasionWithNoNextDateIsNeverBeforeADate(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.graceWithOccasions()

	got := decode[occasionList](t, e.listOccasions(grace, "next_date_before=9999-12-31"))

	require.NotContains(t, occasionNames(got), "Old news")
	require.Equal(t, 4, got.TotalCount)
}

func TestOccasionFiltersAndSortsAreChecked(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.graceWithOccasions()

	for name, tc := range map[string]struct{ query, code string }{
		"an unknown filter":  {"mood=red", "unknown_filter"},
		"an unknown sort":    {"sort=created_at", "unknown_sort"},
		"an unknown kind":    {"kind=holiday", "invalid_input"},
		"a bad date":         {"next_date_before=tomorrow", "invalid_input"},
		"an impossible date": {"next_date_before=2026-02-30", "invalid_input"},
		"a cursor":           {"cursor=nope", "invalid_input"},
	} {
		res := e.listOccasions(grace, tc.query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
}

func TestOccasionsPageByCursorInEachSort(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.graceWithOccasions()

	for sort, want := range map[string][]string{
		"next_date":  {"Advent", "Zeta day", "Birthday", "Mid", "Old news"},
		"-next_date": {"Mid", "Birthday", "Zeta day", "Advent", "Old news"},
		"name":       {"Advent", "Birthday", "Mid", "Old news", "Zeta day"},
		"-name":      {"Zeta day", "Old news", "Mid", "Birthday", "Advent"},
	} {
		var got []string
		query := "limit=2&sort=" + sort
		for pages := 0; ; pages++ {
			require.Less(t, pages, 5, sort)
			l := decode[occasionList](t, e.listOccasions(grace, query))
			require.Equal(t, 5, l.TotalCount, sort)
			got = append(got, occasionNames(l)...)
			if l.NextCursor == nil {
				break
			}
			query = "limit=2&sort=" + sort + "&cursor=" + url.QueryEscape(*l.NextCursor)
		}
		require.Equal(t, want, got, sort)
	}

	first := decode[occasionList](t, e.listOccasions(grace, "limit=2"))
	res := e.listOccasions(grace, "limit=2&sort=name&cursor="+url.QueryEscape(*first.NextCursor))
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "cursor_mismatch", readProblem(t, res).Code)
}

func TestTheOccasionsCollectionNeedsASession(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/occasions"}).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{path: "/occasions/1"}).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{method: http.MethodPost, path: "/occasions", body: map[string]any{}}).StatusCode)
}
