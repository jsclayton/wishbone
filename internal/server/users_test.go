package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/user"
)

// userView is a full user as the API shows it, in the fields these tests read.
type userView struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	LastName           *string         `json:"last_name"`
	Avatar             *imageView      `json:"avatar"`
	Birthday           *map[string]any `json:"birthday"`
	GoodToKnow         *string         `json:"good_to_know"`
	Timezone           *string         `json:"timezone"`
	IsAdmin            bool            `json:"is_admin"`
	ProfileConfirmedAt *string         `json:"profile_confirmed_at"`
	DeactivatedAt      *string         `json:"deactivated_at"`
	CreatedAt          string          `json:"created_at"`
	Version            string          `json:"version"`
	Parents            []summaryView   `json:"parents"`
	Children           []summaryView   `json:"children"`

	CanEdit           bool `json:"can_edit"`
	CanEditParents    bool `json:"can_edit_parents"`
	CanMakeLoginLink  bool `json:"can_make_login_link"`
	CanManageSessions bool `json:"can_manage_sessions"`
	CanConfirmProfile bool `json:"can_confirm_profile"`
	CanDeactivate     bool `json:"can_deactivate"`
	CanReactivate     bool `json:"can_reactivate"`
	CanAddOccasion    bool `json:"can_add_occasion"`
}

// summaryView is a user summary: what a resource holds of another user.
type summaryView struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Avatar *imageView `json:"avatar"`
}

type userList struct {
	Data       []userView `json:"data"`
	NextCursor *string    `json:"next_cursor"`
	TotalCount int        `json:"total_count"`
}

func (e *env) listUsers(token, query string) *http.Response {
	e.t.Helper()
	path := "/users"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

// addUsers adds users straight to the database, oldest first, and returns
// their IDs.
func (e *env) addUsers(names ...string) []string {
	e.t.Helper()
	ids := make([]string, len(names))
	for i, name := range names {
		e.clock.advance(1_000_000_000) // one second, so created_at differs
		id, err := user.Create(e.t.Context(), e.db, e.clock.now(), user.NewUser{Name: name, Birthday: user.PartialDate{Month: 1, Day: 1}})
		require.NoError(e.t, err)
		ids[i] = strconv.FormatInt(id, 10)
	}
	return ids
}

func names(list userList) []string {
	out := make([]string, len(list.Data))
	for i, u := range list.Data {
		out[i] = u.Name
	}
	return out
}

func TestTheAdminListsEveryUserByNameInTheCollectionEnvelope(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.addUsers("grace", "Zed", "Barbara")

	res := e.listUsers(admin, "")

	require.Equal(t, http.StatusOK, res.StatusCode)
	list := decode[userList](t, res)
	require.Equal(t, []string{"Ada", "Barbara", "grace", "Zed"}, names(list), "by name, ignoring case")
	require.Equal(t, 4, list.TotalCount)
	require.Nil(t, list.NextCursor)
}

func TestAUserListsOnlyThemselves(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, token := e.otherUser("Grace")

	list := decode[userList](t, e.listUsers(token, ""))

	require.Equal(t, []string{"Grace"}, names(list))
	require.Equal(t, 1, list.TotalCount)
}

func TestTheUsersCollectionNeedsASession(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	res := e.do(request{path: "/users"})

	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}

func TestAPageEndsWithACursorThatLeadsToTheNextPage(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.addUsers("Bea", "Cy", "Di", "Ed") // five users with Ada

	first := decode[userList](t, e.listUsers(admin, "limit=2"))
	require.Equal(t, []string{"Ada", "Bea"}, names(first))
	require.Equal(t, 5, first.TotalCount, "the whole collection, not the page")
	require.NotNil(t, first.NextCursor)

	second := decode[userList](t, e.listUsers(admin, "limit=2&cursor="+url.QueryEscape(*first.NextCursor)))
	require.Equal(t, []string{"Cy", "Di"}, names(second))
	require.NotNil(t, second.NextCursor)

	last := decode[userList](t, e.listUsers(admin, "limit=2&cursor="+url.QueryEscape(*second.NextCursor)))
	require.Equal(t, []string{"Ed"}, names(last))
	require.Nil(t, last.NextCursor)
	require.Equal(t, 5, last.TotalCount)
}

func TestTheSortIsReversedWithAMinusAndPagesFollowIt(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.addUsers("Bea", "Cy")

	list := decode[userList](t, e.listUsers(admin, "sort=-name&limit=2"))
	require.Equal(t, []string{"Cy", "Bea"}, names(list))
	next := decode[userList](t, e.listUsers(admin, "sort=-name&limit=2&cursor="+url.QueryEscape(*list.NextCursor)))
	require.Equal(t, []string{"Ada"}, names(next))
}

func TestUsersSortByWhenTheyWereCreated(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.addUsers("Zed", "Bea")

	oldest := decode[userList](t, e.listUsers(admin, "sort=created_at"))
	require.Equal(t, []string{"Ada", "Zed", "Bea"}, names(oldest))
	newest := decode[userList](t, e.listUsers(admin, "sort=-created_at&limit=2"))
	require.Equal(t, []string{"Bea", "Zed"}, names(newest))
	rest := decode[userList](t, e.listUsers(admin, "sort=-created_at&limit=2&cursor="+url.QueryEscape(*newest.NextCursor)))
	require.Equal(t, []string{"Ada"}, names(rest))
}

func TestUsersWithTheSameNameKeepTheirIDOrderAcrossPages(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	ids := e.addUsers("Sam", "Sam", "Sam")

	first := decode[userList](t, e.listUsers(admin, "limit=2"))
	second := decode[userList](t, e.listUsers(admin, "limit=2&cursor="+url.QueryEscape(*first.NextCursor)))

	var got []string
	for _, u := range append(first.Data, second.Data...) {
		got = append(got, u.ID)
	}
	require.Equal(t, append([]string{adminID}, ids...), got, "ties break by ID")
}

func TestAnUnknownFilterOrSortKeyIsA422WithACode(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for _, tc := range []struct{ query, code string }{
		{"shade=red", "unknown_filter"},
		{"name=Ada", "unknown_filter"},
		{"sort=shade", "unknown_sort"},
		{"sort=-shade", "unknown_sort"},
		{"sort=", "unknown_sort"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			res := e.listUsers(admin, tc.query)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, tc.code, readProblem(t, res).Code)
		})
	}
}

func TestACursorFromAnotherSortIsACursorMismatch(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	e.addUsers("Bea", "Cy")
	list := decode[userList](t, e.listUsers(admin, "limit=1"))
	cursor := url.QueryEscape(*list.NextCursor)

	for _, sort := range []string{"sort=created_at", "sort=-name"} {
		res := e.listUsers(admin, sort+"&cursor="+cursor)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, sort)
		require.Equal(t, "cursor_mismatch", readProblem(t, res).Code, sort)
	}
	// The default sort and the sort spelled out are the same sort.
	require.Equal(t, http.StatusOK, e.listUsers(admin, "sort=name&cursor="+cursor).StatusCode)
}

func TestACursorTheServerDidNotMakeAndABadLimitAreInvalidInput(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for _, query := range []string{"cursor=nonsense", "cursor=e30", "limit=0", "limit=201", "limit=-1", "limit=x"} {
		res := e.listUsers(admin, query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, query)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, query)
	}
	require.Equal(t, http.StatusOK, e.listUsers(admin, "limit=200").StatusCode)
}

func TestAUserHasAnETagAndEachRowOfTheCollectionHasTheSameVersion(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	graceID, _ := e.otherUser("Grace")

	own := e.do(request{path: "/users/" + adminID, header: cookie(admin)})
	etag := own.Header.Get("ETag")
	require.Regexp(t, `^"[0-9a-f]{32}"$`, etag)

	list := decode[userList](t, e.listUsers(admin, ""))
	versions := map[string]string{}
	for _, u := range list.Data {
		versions[u.ID] = u.Version
	}
	require.Equal(t, etag, versions[adminID])
	byAdmin := e.do(request{path: "/users/" + graceID, header: cookie(admin)})
	require.Equal(t, byAdmin.Header.Get("ETag"), versions[graceID])

	// The ETag hashes the body the caller gets: me is the same body as the ID.
	byMe := e.do(request{path: "/users/me", header: cookie(admin)})
	require.Equal(t, etag, byMe.Header.Get("ETag"))
}

func TestANonVersionedBodyHasNoVersionField(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	body := decode[map[string]any](t, e.do(request{path: "/users/me", header: cookie(admin)}))

	require.NotContains(t, body, "version", "the ETag header carries it; the body would hash itself")
}

func TestTheFullUserHasTheFieldsAndCanFieldsThatExist(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")

	body := decode[map[string]any](t, e.do(request{path: "/users/" + graceID, header: cookie(admin)}))

	require.Equal(t, graceID, body["id"])
	for _, field := range []string{
		"id", "name", "last_name", "birthday", "good_to_know", "timezone", "is_admin", "profile_confirmed_at", "created_at",
		"can_edit", "can_make_login_link", "can_manage_sessions", "can_confirm_profile",
	} {
		require.Contains(t, body, field)
	}
	require.Nil(t, body["last_name"], "a field with no value is null")
	require.Nil(t, body["good_to_know"])
	require.Nil(t, body["timezone"])
	require.Nil(t, body["profile_confirmed_at"])
	require.Equal(t, true, body["can_edit"])
	require.Equal(t, false, body["can_confirm_profile"], "only the user confirms their profile")

	own := decode[userView](t, e.do(request{path: "/users/me", header: cookie(grace)}))
	require.True(t, own.CanEdit)
	require.True(t, own.CanConfirmProfile, "self, and not confirmed yet")
}

// The contract says what the server enforces: the schema of every body that
// takes a name has the server's limit of 100 characters.
func TestTheOpenAPINameLimitsAreTheServersLimit(t *testing.T) {
	ts := newTestServer(t, fakePinger{})
	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					MaxLength int `json:"maxLength"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.NewDecoder(get(t, ts.URL+"/api/v2/openapi.json").Body).Decode(&doc))

	for _, route := range []struct{ path, method string }{
		{"/api/v2/setup", "post"},
		{"/api/v2/users", "post"},
		{"/api/v2/users/{id}", "patch"},
	} {
		content := doc.Paths[route.path][route.method].RequestBody.Content["application/json"]
		schema := doc.Components.Schemas[strings.TrimPrefix(content.Schema.Ref, "#/components/schemas/")]
		for _, field := range []string{"name", "last_name"} {
			require.Equal(t, user.MaxNameLength, schema.Properties[field].MaxLength, "%s %s %s", route.method, route.path, field)
		}
	}
}
