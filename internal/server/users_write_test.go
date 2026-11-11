package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (e *env) createUser(token string, body map[string]any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/users", body: body, header: cookie(token)})
}

func (e *env) patchUser(token, id string, body any, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodPatch, path: "/users/" + id, body: body, header: h})
}

func birthday(month, day int, year any) map[string]any {
	return map[string]any{"month": month, "day": day, "year": year}
}

func TestTheAdminCreatesAUser(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.createUser(admin, map[string]any{
		"name": " Grace ", "last_name": "Hopper", "birthday": birthday(12, 9, 1906), "timezone": "America/New_York",
	})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	u := decode[userView](t, res)
	require.Equal(t, "/api/v2/users/"+u.ID, res.Header.Get("Location"))
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	require.Equal(t, "Grace", u.Name, "the name is trimmed")
	require.Equal(t, "Hopper", *u.LastName)
	require.Equal(t, map[string]any{"month": float64(12), "day": float64(9), "year": float64(1906)}, *u.Birthday)
	require.Equal(t, "America/New_York", *u.Timezone)
	require.False(t, u.IsAdmin)
	require.Nil(t, u.ProfileConfirmedAt)
	require.True(t, u.CanEdit)
	require.False(t, u.CanConfirmProfile, "only the user confirms")

	again := decode[userView](t, e.do(request{path: "/users/" + u.ID, header: cookie(admin)}))
	require.Equal(t, u, again)
	require.Equal(t, 2, decode[userList](t, e.listUsers(admin, "")).TotalCount)
}

func TestAUserNeedsOnlyANameAndABirthdayWithoutAYear(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.createUser(admin, map[string]any{"name": "Grace", "birthday": birthday(2, 29, nil)})

	require.Equal(t, http.StatusCreated, res.StatusCode)
	u := decode[userView](t, res)
	require.Nil(t, u.LastName, "a field with no value is null")
	require.Nil(t, u.Timezone)
	require.Nil(t, (*u.Birthday)["year"])
	require.Equal(t, float64(29), (*u.Birthday)["day"])
}

func TestOnlyTheAdminCreatesUsers(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, grace := e.otherUser("Grace")
	body := map[string]any{"name": "Zed", "birthday": birthday(1, 1, nil)}

	res := e.createUser(grace, body)
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)

	res = e.do(request{method: http.MethodPost, path: "/users", body: body})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
	require.Equal(t, 2, decode[userList](t, e.listUsers(grace, "")).TotalCount+1, "nothing was made")
}

func TestAUserWithInputThatFailsARuleIsA422(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	long := strings.Repeat("é", 101)

	for name, tc := range map[string]struct {
		body     map[string]any
		location string
	}{
		"no name":            {map[string]any{"birthday": birthday(1, 1, nil)}, "body"},
		"a blank name":       {map[string]any{"name": "   ", "birthday": birthday(1, 1, nil)}, "body.name"},
		"a long name":        {map[string]any{"name": long, "birthday": birthday(1, 1, nil)}, "body.name"},
		"a long last name":   {map[string]any{"name": "G", "last_name": long, "birthday": birthday(1, 1, nil)}, "body.last_name"},
		"no birthday":        {map[string]any{"name": "G"}, "body"},
		"no month":           {map[string]any{"name": "G", "birthday": map[string]any{"day": 1}}, "body.birthday"},
		"February 30":        {map[string]any{"name": "G", "birthday": birthday(2, 30, nil)}, "body.birthday"},
		"February 29, 2001":  {map[string]any{"name": "G", "birthday": birthday(2, 29, 2001)}, "body.birthday"},
		"a year before 1900": {map[string]any{"name": "G", "birthday": birthday(1, 1, 1899)}, "body.birthday"},
		"a year in the future": {
			map[string]any{"name": "G", "birthday": birthday(1, 1, 2027)}, "body.birthday",
		},
		"a day in the future": {map[string]any{"name": "G", "birthday": birthday(11, 9, 2026)}, "body.birthday"},
		"an unknown timezone": {map[string]any{"name": "G", "birthday": birthday(1, 1, nil), "timezone": "Mars/Base"}, "body.timezone"},
		"the zone Local":      {map[string]any{"name": "G", "birthday": birthday(1, 1, nil), "timezone": "Local"}, "body.timezone"},
	} {
		t.Run(name, func(t *testing.T) {
			res := e.createUser(admin, tc.body)
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			p := decode[struct {
				Code   string `json:"code"`
				Errors []struct {
					Location string `json:"location"`
				} `json:"errors"`
			}](t, res)
			require.Equal(t, "invalid_input", p.Code)
			require.NotEmpty(t, p.Errors)
			require.Contains(t, p.Errors[0].Location, tc.location)
		})
	}
	require.Equal(t, 1, decode[userList](t, e.listUsers(admin, "")).TotalCount, "no refused input made a user")
}

func TestAPatchChangesOnlyTheFieldsItHas(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")
	before := decode[userView](t, e.do(request{path: "/users/" + id, header: cookie(token)}))

	res := e.patchUser(token, id, map[string]any{"name": "Gracie", "good_to_know": "Likes tea"}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	after := decode[userView](t, res)
	require.Equal(t, "Gracie", after.Name)
	require.Equal(t, "Likes tea", *after.GoodToKnow)
	require.Equal(t, before.Birthday, after.Birthday, "a field that is absent doesn't change")
	require.Equal(t, before.CreatedAt, after.CreatedAt)
	require.NotEqual(t, before.Name, after.Name)
	again := e.do(request{path: "/users/" + id, header: cookie(token)})
	require.Equal(t, again.Header.Get("ETag"), res.Header.Get("ETag"), "the answer is the full resource after the change")
}

func TestAPatchTakesMergePatchJSONAndPlainJSON(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")

	for _, ct := range []string{"application/merge-patch+json", "application/json", "application/merge-patch+json; charset=utf-8"} {
		res := e.do(request{method: http.MethodPatch, path: "/users/" + id, body: map[string]any{"name": "Via " + ct}, header: cookie(token), contentType: ct})
		require.Equal(t, http.StatusOK, res.StatusCode, ct)
		require.Equal(t, "Via "+ct, decode[userView](t, res).Name)
	}
	res := e.do(request{method: http.MethodPatch, path: "/users/" + id, body: map[string]any{"name": "X"}, header: cookie(token), contentType: "text/plain"})
	require.Equal(t, http.StatusUnsupportedMediaType, res.StatusCode)
}

func TestNullClearsAFieldThatCanBeClearedAndIsA422Elsewhere(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")
	require.Equal(t, http.StatusOK, e.patchUser(token, id, map[string]any{"last_name": "Hopper", "good_to_know": "Tea", "timezone": "Europe/Paris"}, nil).StatusCode)

	res := e.patchUser(token, id, map[string]any{"last_name": nil, "good_to_know": nil, "timezone": nil}, nil)

	require.Equal(t, http.StatusOK, res.StatusCode)
	u := decode[userView](t, res)
	require.Nil(t, u.LastName)
	require.Nil(t, u.GoodToKnow)
	require.Nil(t, u.Timezone)

	res = e.patchUser(token, id, map[string]any{"name": nil}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}

func TestAnEmptyLastNameOrNoteIsCleared(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")
	require.Equal(t, http.StatusOK, e.patchUser(token, id, map[string]any{"last_name": "Hopper", "good_to_know": "Tea"}, nil).StatusCode)

	u := decode[userView](t, e.patchUser(token, id, map[string]any{"last_name": "  ", "good_to_know": ""}, nil))

	require.Nil(t, u.LastName)
	require.Nil(t, u.GoodToKnow)
}

func TestABirthdayCannotBeCleared(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")

	res := e.patchUser(token, id, map[string]any{"birthday": nil}, nil)

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "birthday_required", readProblem(t, res).Code)
	after := decode[userView](t, e.do(request{path: "/users/" + id, header: cookie(token)}))
	require.NotNil(t, after.Birthday)
}

func TestAPatchedBirthdayFollowsTheBirthdayRules(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")

	res := e.patchUser(token, id, map[string]any{"birthday": birthday(3, 4, 1999)}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, float64(1999), (*decode[userView](t, res).Birthday)["year"])

	for _, b := range []map[string]any{birthday(2, 30, nil), birthday(1, 1, 1899), birthday(1, 1, 2999)} {
		res = e.patchUser(token, id, map[string]any{"birthday": b}, nil)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "%v", b)
		require.Equal(t, "invalid_input", readProblem(t, res).Code)
	}
}

func TestAFieldTheCallerSeesButCannotChangeIsA403AndNothingChanges(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	id, token := e.otherUser("Grace")

	for _, key := range []string{"id", "is_admin", "created_at", "profile_confirmed_at", "can_edit", "can_confirm_profile", "can_manage_sessions"} {
		t.Run(key, func(t *testing.T) {
			res := e.patchUser(token, id, map[string]any{"name": "Changed", key: "x"}, nil)
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Equal(t, "field_not_writable", readProblem(t, res).Code)
			require.Equal(t, "Grace", decode[userView](t, e.do(request{path: "/users/" + id, header: cookie(token)})).Name)
		})
	}
	res := e.patchUser(admin, adminID, map[string]any{"is_admin": false}, nil)
	require.Equal(t, http.StatusForbidden, res.StatusCode, "the admin can't give up the role either: the CLI does")
	require.Equal(t, "field_not_writable", readProblem(t, res).Code)
}

func TestAFieldThatIsNotInTheUserIsA422(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")

	res := e.patchUser(token, id, map[string]any{"shade": "red"}, nil)

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
}

func TestAPatchNeedsAnObject(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")

	for _, body := range []any{[]string{"name"}, "name", 3} {
		res := e.patchUser(token, id, body, nil)
		require.GreaterOrEqual(t, res.StatusCode, 400)
		require.Less(t, res.StatusCode, 500, "%v", body)
	}
}

func TestOnlyTheUserAndTheAdminEditAUser(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")

	// Another user can't see Grace, so for them she is not there.
	res := e.patchUser(zed, graceID, map[string]any{"name": "X"}, nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	require.Equal(t, "not_found", readProblem(t, res).Code)
	require.Equal(t, http.StatusNotFound, e.patchUser(grace, adminID, map[string]any{"name": "X"}, nil).StatusCode)
	require.Equal(t, http.StatusNotFound, e.patchUser(grace, "999", map[string]any{"name": "X"}, nil).StatusCode)
	require.Equal(t, http.StatusUnauthorized, e.do(request{method: http.MethodPatch, path: "/users/" + graceID, body: map[string]any{"name": "X"}}).StatusCode)

	res = e.patchUser(admin, graceID, map[string]any{"name": "By admin"}, nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.True(t, decode[userView](t, res).CanEdit)
}

func TestAnEditByAnotherUserDoesNotConfirmTheProfile(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	id, token := e.otherUser("Grace")

	u := decode[userView](t, e.patchUser(admin, id, map[string]any{"name": "By admin"}, nil))
	require.Nil(t, u.ProfileConfirmedAt)
	u = decode[userView](t, e.patchUser(token, id, map[string]any{"name": "By Grace"}, nil))
	require.Nil(t, u.ProfileConfirmedAt, "even the user's edit doesn't: confirm-profile does")
}

func TestIfMatchWithTheCurrentVersionPasses(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")

	for i, tc := range []struct {
		name  string
		value func(etag string) string
	}{
		{"the ETag", func(etag string) string { return etag }},
		{"the ETag without quotes", func(etag string) string { return strings.Trim(etag, `"`) }},
		{"a list of tags", func(etag string) string { return `"nope", ` + etag }},
		{"any", func(string) string { return "*" }},
	} {
		etag := e.do(request{path: "/users/" + id, header: cookie(token)}).Header.Get("ETag")
		res := e.patchUser(token, id, map[string]any{"good_to_know": tc.name + strconv.Itoa(i)}, http.Header{"If-Match": {tc.value(etag)}})
		require.Equal(t, http.StatusOK, res.StatusCode, tc.name)
	}
}

func TestARowsVersionWorksInIfMatch(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	id, _ := e.otherUser("Grace")
	var version string
	for _, u := range decode[userList](t, e.listUsers(admin, "")).Data {
		if u.ID == id {
			version = u.Version
		}
	}

	res := e.patchUser(admin, id, map[string]any{"name": "Via version"}, http.Header{"If-Match": {version}})

	require.Equal(t, http.StatusOK, res.StatusCode)
	res = e.patchUser(admin, id, map[string]any{"name": "Again"}, http.Header{"If-Match": {version}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode, "the first edit changed the user")
}

func TestIfMatchWithAStaleVersionIsA412AndChangesNothing(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")
	stale := e.do(request{path: "/users/" + id, header: cookie(token)}).Header.Get("ETag")
	require.Equal(t, http.StatusOK, e.patchUser(token, id, map[string]any{"name": "Changed elsewhere"}, nil).StatusCode)

	for name, value := range map[string]string{"a stale tag": stale, "a weak tag": "W/" + stale, "a made-up tag": `"abc"`} {
		res := e.patchUser(token, id, map[string]any{"name": "Lost update"}, http.Header{"If-Match": {value}})
		require.Equal(t, http.StatusPreconditionFailed, res.StatusCode, name)
		require.Equal(t, "resource_changed", readProblem(t, res).Code, name)
	}
	require.Equal(t, "Changed elsewhere", decode[userView](t, e.do(request{path: "/users/" + id, header: cookie(token)})).Name)
}

func TestTheETagChangesOnlyWhenWhatTheCallerSeesChanges(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	id, token := e.otherUser("Grace")
	first := e.do(request{path: "/users/" + id, header: cookie(token)}).Header.Get("ETag")
	same := e.do(request{path: "/users/" + id, header: cookie(token)}).Header.Get("ETag")
	require.Equal(t, first, same)

	e.clock.advance(time.Hour) // time passing alone changes nothing the caller sees
	require.Equal(t, first, e.do(request{path: "/users/" + id, header: cookie(token)}).Header.Get("ETag"))

	e.patchUser(token, id, map[string]any{"name": "Gracie"}, nil)
	require.NotEqual(t, first, e.do(request{path: "/users/" + id, header: cookie(token)}).Header.Get("ETag"))
}

func TestOnlyTheUserConfirmsTheirProfile(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	id, token := e.otherUser("Grace")
	_, zed := e.otherUser("Zed")

	// The admin sees Grace and can edit her, but can't confirm for her.
	res := e.do(request{method: http.MethodPost, path: "/users/" + id + "/confirm-profile", header: cookie(admin)})
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "forbidden", readProblem(t, res).Code)
	// Zed can't see Grace.
	res = e.do(request{method: http.MethodPost, path: "/users/" + id + "/confirm-profile", header: cookie(zed)})
	require.Equal(t, http.StatusNotFound, res.StatusCode)
	res = e.do(request{method: http.MethodPost, path: "/users/" + id + "/confirm-profile"})
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	res = e.do(request{method: http.MethodPost, path: "/users/" + id + "/confirm-profile", header: cookie(token)})
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.NotEmpty(t, res.Header.Get("ETag"))
	u := decode[userView](t, res)
	require.NotNil(t, u.ProfileConfirmedAt)
	require.Equal(t, "2026-11-08T12:00:00Z", *u.ProfileConfirmedAt)
	require.False(t, u.CanConfirmProfile)
}

func TestConfirmingAgainKeepsTheFirstTime(t *testing.T) {
	e := newEnv(t)
	e.signUp()
	_, token := e.otherUser("Grace")
	confirm := func() userView {
		return decode[userView](t, e.do(request{method: http.MethodPost, path: "/users/me/confirm-profile", header: cookie(token)}))
	}
	first := confirm()
	e.clock.advance(time.Hour)

	second := confirm()

	require.Equal(t, *first.ProfileConfirmedAt, *second.ProfileConfirmedAt)
}
