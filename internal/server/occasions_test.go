package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the occasions of a user: the birthday occasion that every
// user has, and the custom occasions that a user and their parent keep
// (docs/spec.md, Occasions; docs/routes.md, Occasions).

type ruleView struct {
	Kind    string `json:"kind"`
	Year    *int   `json:"year"`
	Month   int    `json:"month"`
	Day     *int   `json:"day"`
	Weekday *int   `json:"weekday"`
	N       *int   `json:"n"`
}

type occasionView struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	Name     string       `json:"name"`
	Rule     ruleView     `json:"rule"`
	User     *summaryView `json:"user"`
	NextDate *string      `json:"next_date"`
	Version  string       `json:"version"`

	IsEveryone *bool `json:"is_everyone"`

	CanEdit        bool `json:"can_edit"`
	CanDelete      bool `json:"can_delete"`
	CanAttachGroup bool `json:"can_attach_group"`
}

type occasionList struct {
	Data       []occasionView `json:"data"`
	NextCursor *string        `json:"next_cursor"`
	TotalCount int            `json:"total_count"`
}

func (e *env) listOccasions(token, query string) *http.Response {
	e.t.Helper()
	path := "/occasions"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) getOccasion(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/occasions/" + id, header: cookie(token)})
}

func (e *env) postOccasion(token string, body map[string]any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/occasions", body: body, header: cookie(token)})
}

func (e *env) patchOccasion(token, id string, body map[string]any, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodPatch, path: "/occasions/" + id, body: body, header: h})
}

func (e *env) deleteOccasion(token, id string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodDelete, path: "/occasions/" + id, header: cookie(token)})
}

func occasionNames(l occasionList) []string {
	out := make([]string, len(l.Data))
	for i, o := range l.Data {
		out[i] = o.Name
	}
	return out
}

// A visible field is always present, and null when it has no value (docs/spec.md,
// API principles, Values). So a rule has every field, whatever its kind.
func TestARuleHasEveryFieldAndNullForTheOnesItsKindDoesNotUse(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	e.addOccasion(grace, "Anniversary", annual(6, 1), "")
	e.addOccasion(grace, "First Advent", map[string]any{"kind": "nth_weekday", "n": 1, "weekday": 0, "month": 12}, "")
	e.addOccasion(grace, "Fete", map[string]any{"kind": "last_weekday", "weekday": 1, "month": 6}, "")
	e.addOccasion(grace, "Reunion", once(2030, 8, 9), "")

	res := e.listOccasions(grace, "")
	rules := map[string]map[string]any{}
	for _, o := range decode[struct {
		Data []struct {
			Name string         `json:"name"`
			Rule map[string]any `json:"rule"`
		} `json:"data"`
	}](t, res).Data {
		rules[o.Name] = o.Rule
	}

	require.Equal(t, map[string]any{"kind": "annual", "month": 1.0, "day": 1.0, "year": nil, "weekday": nil, "n": nil}, rules["Birthday"])
	require.Equal(t, map[string]any{"kind": "annual", "month": 6.0, "day": 1.0, "year": nil, "weekday": nil, "n": nil}, rules["Anniversary"])
	require.Equal(t, map[string]any{"kind": "nth_weekday", "month": 12.0, "day": nil, "year": nil, "weekday": 0.0, "n": 1.0}, rules["First Advent"], "Sunday is 0, not null")
	require.Equal(t, map[string]any{"kind": "last_weekday", "month": 6.0, "day": nil, "year": nil, "weekday": 1.0, "n": nil}, rules["Fete"])
	require.Equal(t, map[string]any{"kind": "once", "month": 8.0, "day": 9.0, "year": 2030.0, "weekday": nil, "n": nil}, rules["Reunion"])
}

func TestSetupMakesTheAdminABirthdayOccasion(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()

	res := e.listOccasions(admin, "")

	require.Equal(t, http.StatusOK, res.StatusCode)
	list := decode[occasionList](t, res)
	require.Equal(t, 1, list.TotalCount)
	require.Len(t, list.Data, 1)
	b := list.Data[0]
	require.NotEmpty(t, b.ID)
	require.Equal(t, "birthday", b.Kind)
	require.Equal(t, "Birthday", b.Name)
	day, month := 10, 12
	require.Equal(t, ruleView{Kind: "annual", Month: month, Day: &day}, b.Rule, "the rule is the user's birthday")
	require.Equal(t, &summaryView{ID: adminID, Name: "Ada"}, b.User)
	require.Equal(t, "2026-12-10", *b.NextDate, "the clock is Nov 8 2026")
	require.False(t, b.CanEdit, "nobody edits a birthday")
	require.False(t, b.CanDelete, "nobody deletes a birthday")
}
