package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func annual(month, day int) map[string]any {
	return map[string]any{"kind": "annual", "month": month, "day": day}
}

func once(year, month, day int) map[string]any {
	return map[string]any{"kind": "once", "year": year, "month": month, "day": day}
}

// addOccasion makes a custom occasion and fails the test when the server
// refuses. userID is the user it is for, or empty for the caller.
func (e *env) addOccasion(token, name string, rule map[string]any, userID string) occasionView {
	e.t.Helper()
	body := map[string]any{"kind": "custom", "name": name, "rule": rule}
	if userID != "" {
		body["user_id"] = userID
	}
	res := e.postOccasion(token, body)
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "add occasion "+name)
	o := decode[occasionView](e.t, res)
	o.Version = res.Header.Get("ETag") // a resource's own address has no version in its body
	return o
}

func nextDates(l occasionList) []string {
	out := make([]string, len(l.Data))
	for i, o := range l.Data {
		out[i] = "none"
		if o.NextDate != nil {
			out[i] = *o.NextDate
		}
	}
	return out
}

func TestPostUsersAndATestUserEachGetABirthdayOccasion(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.createUser(admin, map[string]any{"name": "Grace", "birthday": map[string]any{"month": 3, "day": 5, "year": 1990}})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	grace := decode[userView](t, res)
	_, _ = e.otherUser("Hedy")

	list := decode[occasionList](t, e.listOccasions(admin, "user_id="+grace.ID))
	require.Len(t, list.Data, 1)
	require.Equal(t, "birthday", list.Data[0].Kind)
	require.Equal(t, grace.ID, list.Data[0].User.ID)
	require.Equal(t, "2027-03-05", *list.Data[0].NextDate)
	require.Equal(t, 3, decode[occasionList](t, e.listOccasions(admin, "kind=birthday")).TotalCount, "Ada, Grace and Hedy")
}

func TestABirthdayOccasionFollowsTheUsersBirthday(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	before := decode[occasionList](t, e.listOccasions(grace, "user_id="+graceID)).Data[0]
	require.Equal(t, "2027-01-01", *before.NextDate)

	// A leap day falls on Feb 28 in a year that isn't a leap year.
	res := e.patchUser(admin, graceID, map[string]any{"birthday": map[string]any{"month": 2, "day": 29, "year": 2000}}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)

	res = e.getOccasion(grace, before.ID)
	after := decode[occasionView](t, res)
	after.Version = res.Header.Get("ETag")
	require.Equal(t, before.ID, after.ID, "the occasion keeps its ID")
	require.Equal(t, "2027-02-28", *after.NextDate)
	day := 29
	require.Equal(t, ruleView{Kind: "annual", Month: 2, Day: &day}, after.Rule)
	require.NotEqual(t, before.Version, after.Version)
}

func TestANextDateIsTodayWhereTheUserLives(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	// The clock is Nov 8 12:00 UTC. It is already Nov 9 in Auckland.
	aucklandID, _ := e.otherUser("Aroha")
	utcID, _ := e.otherUser("Una")
	for _, id := range []string{aucklandID, utcID} {
		require.Equal(t, http.StatusOK, e.patchUser(admin, id, map[string]any{"birthday": map[string]any{"month": 11, "day": 8}}, nil).StatusCode)
	}
	require.Equal(t, http.StatusOK, e.patchUser(admin, aucklandID, map[string]any{"timezone": "Pacific/Auckland"}, nil).StatusCode)

	require.Equal(t, []string{"2027-11-08"}, nextDates(decode[occasionList](t, e.listOccasions(admin, "user_id="+aucklandID))), "Nov 8 is over in Auckland")
	require.Equal(t, []string{"2026-11-08"}, nextDates(decode[occasionList](t, e.listOccasions(admin, "user_id="+utcID))), "and today in UTC")
}

func TestAUserAddsCustomOccasionsWithEachDateRule(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	graceID, grace := e.otherUser("Grace")

	res := e.postOccasion(grace, map[string]any{"kind": "custom", "name": "  Anniversary  ", "rule": annual(6, 1)})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	o := decode[occasionView](t, res)
	require.Equal(t, "/api/v2/occasions/"+o.ID, res.Header.Get("Location"))
	require.NotEmpty(t, res.Header.Get("ETag"))
	require.Equal(t, "custom", o.Kind)
	require.Equal(t, "Anniversary", o.Name)
	require.Equal(t, graceID, o.User.ID)
	require.Equal(t, "2027-06-01", *o.NextDate)
	require.True(t, o.CanEdit)
	require.True(t, o.CanDelete)

	// Sunday is weekday 0, and it is shown.
	sunday := decode[occasionView](t, e.postOccasion(grace, map[string]any{"kind": "custom", "name": "First Advent",
		"rule": map[string]any{"kind": "nth_weekday", "n": 1, "weekday": 0, "month": 12}}))
	zero, one := 0, 1
	require.Equal(t, ruleView{Kind: "nth_weekday", Month: 12, Weekday: &zero, N: &one}, sunday.Rule)
	require.Equal(t, "2026-12-06", *sunday.NextDate)

	for name, tc := range map[string]struct {
		rule map[string]any
		want string
	}{
		"4th Thursday of November": {map[string]any{"kind": "nth_weekday", "n": 4, "weekday": 4, "month": 11}, "2026-11-26"},
		"last Monday of November":  {map[string]any{"kind": "last_weekday", "weekday": 1, "month": 11}, "2026-11-30"},
		"a date in the future":     {once(2026, 12, 31), "2026-12-31"},
		"today":                    {once(2026, 11, 8), "2026-11-08"},
		"a date in the past":       {once(2020, 1, 1), "none"},
		"a leap day":               {annual(2, 29), "2027-02-28"},
	} {
		o := decode[occasionView](t, e.postOccasion(grace, map[string]any{"kind": "custom", "name": name, "rule": tc.rule}))
		got := "none"
		if o.NextDate != nil {
			got = *o.NextDate
		}
		require.Equal(t, tc.want, got, name)
	}
}

func TestAnOccasionNeedsANameAndAValidRule(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")

	for name, body := range map[string]map[string]any{
		"no name":              {"kind": "custom", "rule": annual(6, 1)},
		"blank name":           {"kind": "custom", "name": "  ", "rule": annual(6, 1)},
		"long name":            {"kind": "custom", "name": strings.Repeat("a", 101), "rule": annual(6, 1)},
		"no rule":              {"kind": "custom", "name": "A"},
		"an unknown rule kind": {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "weekly", "month": 1}},
		"February 30":          {"kind": "custom", "name": "A", "rule": annual(2, 30)},
		"month 13":             {"kind": "custom", "name": "A", "rule": annual(13, 1)},
		"no day":               {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "annual", "month": 1}},
		"no year":              {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "once", "month": 1, "day": 1}},
		"Feb 29, 2027":         {"kind": "custom", "name": "A", "rule": once(2027, 2, 29)},
		"the 5th Monday":       {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "nth_weekday", "n": 5, "weekday": 1, "month": 1}},
		"no n":                 {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "nth_weekday", "weekday": 1, "month": 1}},
		"no weekday":           {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "last_weekday", "month": 1}},
		"weekday 7":            {"kind": "custom", "name": "A", "rule": map[string]any{"kind": "last_weekday", "weekday": 7, "month": 1}},
		"no kind":              {"name": "A", "rule": annual(1, 1)},
		"an unknown kind":      {"kind": "holiday", "name": "A", "rule": annual(1, 1)},
	} {
		res := e.postOccasion(grace, body)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, name)
	}
	require.Equal(t, 1, decode[occasionList](t, e.listOccasions(grace, "")).TotalCount, "only the birthday")
}
