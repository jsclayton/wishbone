package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
)

func TestTheAdminSeesAndDeletesAnotherUsersPasskeysButCannotAddOrRenameThem(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	made := decode[passkeyView](t, e.registerPasskey(graceID, cookie(grace), passkeytest.New(t), "Phone"))

	res := e.listPasskeys(graceID, cookie(admin), "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	list := decode[passkeyList](t, res)
	require.Len(t, list.Data, 1)
	require.Equal(t, "Phone", list.Data[0].Name)
	require.False(t, list.Data[0].CanEdit, "only the user renames a passkey")
	require.True(t, list.Data[0].CanDelete)

	for _, r := range []request{
		{method: http.MethodPost, path: "/users/" + graceID + "/passkeys/registration"},
		{method: http.MethodPost, path: "/users/" + graceID + "/passkeys", body: map[string]any{"credential": map[string]any{}}},
		{method: http.MethodPatch, path: "/users/" + graceID + "/passkeys/" + made.ID, body: map[string]any{"name": "Mine"}},
	} {
		r.header = cookie(admin)
		res := e.do(r)
		require.Equal(t, http.StatusForbidden, res.StatusCode, r.method+" "+r.path)
		require.Equal(t, "forbidden", readProblem(t, res).Code)
	}

	res = e.do(request{method: http.MethodDelete, path: "/users/" + graceID + "/passkeys/" + made.ID, header: cookie(admin)})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, decode[passkeyList](t, e.listPasskeys(graceID, cookie(grace), "")).Data)
}

func TestAUserWhoIsNotTheUserOrTheAdminFindsNoPasskeys(t *testing.T) {
	e := newEnv(t)
	adminID, admin := e.signUp()
	_, grace := e.otherUser("Grace")
	made := decode[passkeyView](t, e.registerPasskey(adminID, cookie(admin), passkeytest.New(t), "Laptop"))

	for _, r := range []request{
		{path: "/users/" + adminID + "/passkeys"},
		{method: http.MethodPost, path: "/users/" + adminID + "/passkeys/registration"},
		{method: http.MethodPost, path: "/users/" + adminID + "/passkeys", body: map[string]any{"credential": map[string]any{}}},
		{method: http.MethodPatch, path: "/users/" + adminID + "/passkeys/" + made.ID, body: map[string]any{"name": "Mine"}},
		{method: http.MethodDelete, path: "/users/" + adminID + "/passkeys/" + made.ID},
		{path: "/users/999/passkeys"},
	} {
		r.header = cookie(grace)
		res := e.do(r)
		require.Equal(t, http.StatusNotFound, res.StatusCode, r.method+" "+r.path)
		require.Equal(t, "not_found", readProblem(t, res).Code)
	}
	require.Equal(t, "Laptop", decode[passkeyList](t, e.listPasskeys(adminID, cookie(admin), "")).Data[0].Name)
}

func TestPasskeyRoutesNeedASignIn(t *testing.T) {
	e := newEnv(t)
	id, _ := e.signUp()
	for _, r := range []request{
		{path: "/users/" + id + "/passkeys"},
		{method: http.MethodPost, path: "/users/" + id + "/passkeys/registration"},
		{method: http.MethodPost, path: "/users/" + id + "/passkeys", body: map[string]any{"credential": map[string]any{}}},
		{method: http.MethodPatch, path: "/users/" + id + "/passkeys/1", body: map[string]any{"name": "x"}},
		{method: http.MethodDelete, path: "/users/" + id + "/passkeys/1"},
	} {
		res := e.do(r)
		require.Equal(t, http.StatusUnauthorized, res.StatusCode, r.method+" "+r.path)
	}
}

func TestABearerCallerAndACookieCallerGetTheSamePasskeys(t *testing.T) {
	e := newEnv(t)
	id, cookieToken := e.signUp()
	bearerToken := decode[redeemBody](t, e.redeem(e.makeLink(cookieToken, id), "bearer", nil)).Token
	require.Equal(t, http.StatusCreated, e.registerPasskey("me", bearer(bearerToken), passkeytest.New(t), "Laptop").StatusCode)

	viaCookie := decode[passkeyList](t, e.listPasskeys("me", cookie(cookieToken), ""))
	viaBearer := decode[passkeyList](t, e.listPasskeys(id, bearer(bearerToken), ""))

	require.Len(t, viaCookie.Data, 1)
	require.Equal(t, viaCookie.Data, viaBearer.Data)
}

func TestRenamingAPasskeyReturnsItAndChangesItsVersion(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	made := decode[passkeyView](t, e.registerPasskey(id, cookie(token), passkeytest.New(t), "Laptop"))
	versionBefore := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data[0].Version

	res := e.do(request{method: http.MethodPatch, path: "/users/me/passkeys/" + made.ID, header: cookie(token),
		body: map[string]any{"name": "  Kitchen laptop "}, contentType: "application/merge-patch+json"})

	require.Equal(t, http.StatusOK, res.StatusCode)
	renamed := decode[passkeyView](t, res)
	require.Equal(t, "Kitchen laptop", renamed.Name)
	require.Equal(t, made.ID, renamed.ID)
	require.True(t, renamed.CanEdit)
	require.NotEqual(t, versionBefore, res.Header.Get("ETag"))
	row := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data[0]
	require.Equal(t, "Kitchen laptop", row.Name)
	require.Equal(t, res.Header.Get("ETag"), row.Version)
}

func TestARenameWithABadBodyIsRefused(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	made := decode[passkeyView](t, e.registerPasskey(id, cookie(token), passkeytest.New(t), "Laptop"))
	path := "/users/" + id + "/passkeys/" + made.ID

	for name, tc := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"blank name":       {map[string]any{"name": "   "}, http.StatusUnprocessableEntity, "invalid_input"},
		"empty name":       {map[string]any{"name": ""}, http.StatusUnprocessableEntity, "invalid_input"},
		"null name":        {map[string]any{"name": nil}, http.StatusUnprocessableEntity, "invalid_input"},
		"long name":        {map[string]any{"name": strings.Repeat("a", 101)}, http.StatusUnprocessableEntity, "invalid_input"},
		"read-only field":  {map[string]any{"created_at": "2020-01-01T00:00:00Z"}, http.StatusForbidden, "field_not_writable"},
		"can field":        {map[string]any{"can_edit": false}, http.StatusForbidden, "field_not_writable"},
		"field that isn't": {map[string]any{"shade": "red"}, http.StatusUnprocessableEntity, "invalid_input"},
	} {
		res := e.do(request{method: http.MethodPatch, path: path, header: cookie(token), body: tc.body})
		require.Equal(t, tc.status, res.StatusCode, name)
		require.Equal(t, tc.code, readProblem(t, res).Code, name)
	}
	require.Equal(t, "Laptop", decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data[0].Name)
}

func TestARenameOrADeleteWithAStaleVersionIs412AndChangesNothing(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	made := decode[passkeyView](t, e.registerPasskey(id, cookie(token), passkeytest.New(t), "Laptop"))
	path := "/users/" + id + "/passkeys/" + made.ID
	stale := http.Header{"If-Match": {`"stale"`}, "Cookie": {sessionCookieName + "=" + token}}

	res := e.do(request{method: http.MethodPatch, path: path, header: stale, body: map[string]any{"name": "Phone"}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)
	res = e.do(request{method: http.MethodDelete, path: path, header: stale})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)

	row := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data[0]
	require.Equal(t, "Laptop", row.Name)
	current := http.Header{"If-Match": {row.Version}, "Cookie": {sessionCookieName + "=" + token}}
	require.Equal(t, http.StatusNoContent, e.do(request{method: http.MethodDelete, path: path, header: current}).StatusCode)
}

func TestDeletingAPasskeyIs204AndAGoneOrForeignPasskeyIs404(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	graceID, grace := e.otherUser("Grace")
	mine := decode[passkeyView](t, e.registerPasskey(id, cookie(token), passkeytest.New(t), "Laptop"))
	hers := decode[passkeyView](t, e.registerPasskey(graceID, cookie(grace), passkeytest.New(t), "Phone"))

	res := e.do(request{method: http.MethodDelete, path: "/users/me/passkeys/" + mine.ID, header: cookie(token)})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Empty(t, readBody(t, res))
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodDelete, path: "/users/me/passkeys/" + mine.ID, header: cookie(token)}).StatusCode)

	// Another user's passkey at my address is no passkey of mine.
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodDelete, path: "/users/me/passkeys/" + hers.ID, header: cookie(token)}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodPatch, path: "/users/me/passkeys/" + hers.ID, header: cookie(token), body: map[string]any{"name": "x"}}).StatusCode)
	require.Equal(t, http.StatusNotFound, e.do(request{method: http.MethodDelete, path: "/users/me/passkeys/nope", header: cookie(token)}).StatusCode)
	require.Len(t, decode[passkeyList](t, e.listPasskeys(graceID, cookie(grace), "")).Data, 1)
}

func TestRegistrationProblemsHaveACodeEach(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	a := passkeytest.New(t)

	// Finishing with no registration under way.
	res := e.do(request{method: http.MethodPost, path: "/users/me/passkeys", header: cookie(token), body: map[string]any{"credential": map[string]any{}}})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "passkey_registration_expired", readProblem(t, res).Code)

	// An answer that does not verify.
	e.do(request{method: http.MethodPost, path: "/users/me/passkeys/registration", header: cookie(token)})
	res = e.do(request{method: http.MethodPost, path: "/users/me/passkeys", header: cookie(token), body: map[string]any{"credential": map[string]any{}}})
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Equal(t, "passkey_not_verified", readProblem(t, res).Code)

	// An answer that comes after the registration ran out.
	begin := e.do(request{method: http.MethodPost, path: "/users/me/passkeys/registration", header: cookie(token)})
	var opts json.RawMessage
	require.NoError(t, json.NewDecoder(begin.Body).Decode(&opts))
	late := a.Create(t, opts, passkeyOrigin)
	e.clock.advance(6 * time.Minute)
	res = e.do(request{method: http.MethodPost, path: "/users/me/passkeys", header: cookie(token), body: map[string]any{"credential": json.RawMessage(late)}})
	require.Equal(t, "passkey_registration_expired", readProblem(t, res).Code)

	// The same authenticator twice.
	require.Equal(t, http.StatusCreated, e.registerPasskey(id, cookie(token), a, "One").StatusCode)
	res = e.registerPasskey(id, cookie(token), a, "Two")
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Equal(t, "passkey_already_added", readProblem(t, res).Code)
	require.Len(t, decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data, 1)
}

func TestAPasskeyAddedWithNoNameIsCalledPasskey(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()

	made := decode[passkeyView](t, e.registerPasskey(id, cookie(token), passkeytest.New(t), ""))

	require.Equal(t, "Passkey", made.Name)
}

func TestThePasskeysPageAndSortLikeAnyCollection(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	for _, name := range []string{"First", "Second", "Third"} {
		e.clock.advance(time.Minute)
		require.Equal(t, http.StatusCreated, e.registerPasskey(id, cookie(token), passkeytest.New(t), name).StatusCode)
	}

	first := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "?limit=2"))
	require.Equal(t, []string{"First", "Second"}, []string{first.Data[0].Name, first.Data[1].Name})
	require.Equal(t, 3, first.TotalCount)
	require.NotNil(t, first.NextCursor)
	second := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "?limit=2&cursor="+*first.NextCursor))
	require.Len(t, second.Data, 1)
	require.Equal(t, "Third", second.Data[0].Name)
	require.Nil(t, second.NextCursor)

	newest := decode[passkeyList](t, e.listPasskeys(id, cookie(token), "?sort=-created_at"))
	require.Equal(t, "Third", newest.Data[0].Name)

	res := e.listPasskeys(id, cookie(token), "?sort=name")
	require.Equal(t, "unknown_sort", readProblem(t, res).Code)
	res = e.listPasskeys(id, cookie(token), "?name=x")
	require.Equal(t, "unknown_filter", readProblem(t, res).Code)
	res = e.listPasskeys(id, cookie(token), "?sort=-created_at&cursor="+*first.NextCursor)
	require.Equal(t, "cursor_mismatch", readProblem(t, res).Code)
}
