package server

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type blockedStoreView struct {
	ID        string `json:"id"`
	Store     string `json:"store"`
	CreatedAt string `json:"created_at"`
	CanDelete bool   `json:"can_delete"`
	Version   string `json:"version"`
}

type blockedStoreList struct {
	Data       []blockedStoreView `json:"data"`
	NextCursor *string            `json:"next_cursor"`
	TotalCount int                `json:"total_count"`
}

func (e *env) postBlockedStore(token, store string) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/blocked-stores", body: map[string]any{"store": store}, header: cookie(token)})
}

// blockStore blocks a store as the admin and returns it.
func (e *env) blockStore(admin, store string) blockedStoreView {
	e.t.Helper()
	res := e.postBlockedStore(admin, store)
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "block %q", store)
	return decode[blockedStoreView](e.t, res)
}

func (e *env) listBlockedStores(token, query string) *http.Response {
	e.t.Helper()
	path := "/blocked-stores"
	if query != "" {
		path += "?" + query
	}
	return e.do(request{path: path, header: cookie(token)})
}

func (e *env) deleteBlockedStore(token, id string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{method: http.MethodDelete, path: "/blocked-stores/" + id, header: h})
}

func blockedStoreNames(list blockedStoreList) []string {
	out := make([]string, len(list.Data))
	for i, b := range list.Data {
		out[i] = b.Store
	}
	return out
}

func TestTheAdminBlocksAStore(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.postBlockedStore(admin, "https://www.Temu.com/p/phone-case?utm_source=x")

	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Regexp(t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	fields := decode[map[string]any](t, res)
	delete(fields, "$schema") // huma adds it to the bytes it sends
	require.ElementsMatch(t, []string{"id", "store", "created_at", "can_delete"}, keysOfMap(fields),
		"a blocked store has no more fields: only the admin blocks, and the route shows no items")
	require.Equal(t, "temu.com", fields["store"])
	require.Equal(t, true, fields["can_delete"])
	id, _ := fields["id"].(string)
	require.Equal(t, "/api/v2/blocked-stores/"+id, res.Header.Get("Location"))
	require.Equal(t, "2026-11-08T12:00:00Z", fields["created_at"])
}

func keysOfMap(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestABlockedStoreIsNamedByItsHost(t *testing.T) {
	for in, want := range map[string]string{
		"TEMU.com":           "temu.com",
		"www.temu.com":       "temu.com",
		"temu.com.":          "temu.com",
		"https://temu.com./": "temu.com",
		"http://temu.com:81": "temu.com",
	} {
		e := newEnv(t)
		_, admin := e.signUp()
		require.Equal(t, want, e.blockStore(admin, in).Store, in)
	}
}

func TestAStoreThatIsNotAHostIsA422(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	for _, in := range []string{"", "   ", "temu", "ftp://temu.com", "not a store", "https://"} {
		res := e.postBlockedStore(admin, in)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, in)
		p := decode[struct {
			Code   string `json:"code"`
			Errors []struct {
				Location string `json:"location"`
			} `json:"errors"`
		}](t, res)
		require.Equal(t, "invalid_input", p.Code, in)
		require.Len(t, p.Errors, 1, in)
		require.Equal(t, "body.store", p.Errors[0].Location, in)
	}
	res := e.do(request{method: http.MethodPost, path: "/blocked-stores", body: map[string]any{}, header: cookie(admin)})
	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, "no store")
	require.Equal(t, 0, decode[blockedStoreList](t, e.listBlockedStores(admin, "")).TotalCount, "nothing was blocked")
}

func TestBlockingABlockedStoreIsAlreadyBlockedAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	first := e.blockStore(admin, "temu.com")

	for _, in := range []string{"temu.com", "https://www.TEMU.com/x"} {
		res := e.postBlockedStore(admin, in)
		require.Equal(t, http.StatusConflict, res.StatusCode, in)
		require.Equal(t, "already_blocked", readProblem(t, res).Code, in)
	}

	list := decode[blockedStoreList](t, e.listBlockedStores(admin, ""))
	require.Equal(t, 1, list.TotalCount)
	require.Equal(t, first.ID, list.Data[0].ID)
}

func TestOnlyTheAdminReachesBlockedStores(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	_, grace := e.otherUser("Grace")
	temu := e.blockStore(admin, "temu.com")

	for name, call := range map[string]func(token string) *http.Response{
		"list":   func(token string) *http.Response { return e.listBlockedStores(token, "") },
		"block":  func(token string) *http.Response { return e.postBlockedStore(token, "shein.com") },
		"remove": func(token string) *http.Response { return e.deleteBlockedStore(token, temu.ID, nil) },
	} {
		res := call(grace)
		require.Equal(t, http.StatusForbidden, res.StatusCode, name)
		require.Equal(t, "forbidden", readProblem(t, res).Code, name)
	}
	for _, r := range []request{
		{path: "/blocked-stores"},
		{method: http.MethodPost, path: "/blocked-stores", body: map[string]any{"store": "shein.com"}},
		{method: http.MethodDelete, path: "/blocked-stores/" + temu.ID},
	} {
		require.Equal(t, http.StatusUnauthorized, e.do(r).StatusCode, r.method+" "+r.path)
	}
	require.Equal(t, []string{"temu.com"}, blockedStoreNames(decode[blockedStoreList](t, e.listBlockedStores(admin, ""))),
		"a user's attempts changed nothing")
}

func TestTheBlockedStoresAreSortedByStore(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	for _, s := range []string{"temu.com", "aliexpress.com", "shein.com"} {
		e.blockStore(admin, s)
	}

	list := decode[blockedStoreList](t, e.listBlockedStores(admin, ""))

	require.Equal(t, []string{"aliexpress.com", "shein.com", "temu.com"}, blockedStoreNames(list))
	require.Equal(t, 3, list.TotalCount)
	for _, b := range list.Data {
		require.True(t, b.CanDelete)
		require.Regexp(t, `^"[0-9a-f]{32}"$`, b.Version)
	}
	require.Equal(t, []string{"temu.com", "shein.com", "aliexpress.com"},
		blockedStoreNames(decode[blockedStoreList](t, e.listBlockedStores(admin, "sort=-store"))))
}

func TestTheBlockedStoresCollectionPagesAndRejectsWhatItDoesNotKnow(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	for _, s := range []string{"c.com", "a.com", "b.com"} {
		e.blockStore(admin, s)
	}

	first := decode[blockedStoreList](t, e.listBlockedStores(admin, "limit=2"))
	require.Equal(t, []string{"a.com", "b.com"}, blockedStoreNames(first))
	require.Equal(t, 3, first.TotalCount)
	require.NotNil(t, first.NextCursor)
	rest := decode[blockedStoreList](t, e.listBlockedStores(admin, "limit=2&cursor="+url.QueryEscape(*first.NextCursor)))
	require.Equal(t, []string{"c.com"}, blockedStoreNames(rest))
	require.Nil(t, rest.NextCursor)

	for query, code := range map[string]string{"shade=red": "unknown_filter", "sort=created_at": "unknown_sort"} {
		res := e.listBlockedStores(admin, query)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, query)
		require.Equal(t, code, readProblem(t, res).Code, query)
	}
}

func TestARowsVersionIsTheETagOfTheCreate(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	res := e.postBlockedStore(admin, "temu.com")
	etag := res.Header.Get("ETag")

	list := decode[blockedStoreList](t, e.listBlockedStores(admin, ""))

	require.Equal(t, etag, list.Data[0].Version)
}

func TestTheAdminAllowsAStoreAgain(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	temu := e.blockStore(admin, "temu.com")
	e.blockStore(admin, "shein.com")

	res := e.deleteBlockedStore(admin, temu.ID, nil)

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, []string{"shein.com"}, blockedStoreNames(decode[blockedStoreList](t, e.listBlockedStores(admin, ""))))
	res = e.deleteBlockedStore(admin, temu.ID, nil)
	require.Equal(t, http.StatusNotFound, res.StatusCode, "it is gone")
	require.Equal(t, "not_found", readProblem(t, res).Code)
	require.Equal(t, http.StatusNotFound, e.deleteBlockedStore(admin, "nonsense", nil).StatusCode)
	require.Equal(t, http.StatusCreated, e.postBlockedStore(admin, "temu.com").StatusCode, "it can be blocked again")
}

func TestDeletingWithAStaleIfMatchIsA412(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	created := e.postBlockedStore(admin, "temu.com")
	temu := decode[blockedStoreView](t, created)

	res := e.deleteBlockedStore(admin, temu.ID, http.Header{"If-Match": {`"0123456789abcdef0123456789abcdef"`}})
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)
	require.Equal(t, 1, decode[blockedStoreList](t, e.listBlockedStores(admin, "")).TotalCount, "nothing was removed")

	res = e.deleteBlockedStore(admin, temu.ID, http.Header{"If-Match": {created.Header.Get("ETag")}})
	require.Equal(t, http.StatusNoContent, res.StatusCode)
}
