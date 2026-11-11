package blocklist_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/blocklist"
	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/pipeline"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/store"
)

var blockedAt = time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) (*blocklist.Service, *sqlx.DB) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	return blocklist.NewService(st.DB(), func() time.Time { return blockedAt }), st.DB()
}

func block(t *testing.T, svc *blocklist.Service, input string) blocklist.Store {
	t.Helper()
	s, err := svc.Add(t.Context(), input)
	require.NoError(t, err, input)
	return s
}

func TestAddNamesTheStoreAsTheLowercaseHost(t *testing.T) {
	for _, in := range []string{
		"temu.com", "TEMU.com", "www.temu.com", " temu.com ", "temu.com.", "temu.com..",
		"https://www.temu.com/p/phone-case?utm_source=x", "http://temu.com:8080/x", "https://temu.com./p/1",
		"https://ｔｅｍｕ.com/p",
	} {
		svc, _ := newService(t)
		s := block(t, svc, in)
		require.Equal(t, "temu.com", s.Store, in)
		require.Equal(t, blockedAt, s.CreatedAt.UTC(), in)
	}
}

func TestAddKeepsTheUnicodeFormOfAHost(t *testing.T) {
	svc, _ := newService(t)
	require.Equal(t, "bücher.de", block(t, svc, "xn--bcher-kva.de").Store)
}

func TestAddRefusesWhatIsNotAStore(t *testing.T) {
	svc, _ := newService(t)
	for _, in := range []string{"", "   ", "temu", "ftp://temu.com", "not a store", "https://", ".com", "a..com"} {
		_, err := svc.Add(t.Context(), in)
		require.ErrorIs(t, err, blocklist.ErrInvalid, in)
	}
}

func TestAddARepeatIsAlreadyBlocked(t *testing.T) {
	svc, _ := newService(t)
	first := block(t, svc, "temu.com")
	for _, in := range []string{"temu.com", "https://www.TEMU.com/x", "temu.com."} {
		_, err := svc.Add(t.Context(), in)
		require.ErrorIs(t, err, blocklist.ErrAlreadyBlocked, in)
	}
	got, _, _, err := svc.List(t.Context(), listAll)
	require.NoError(t, err)
	require.Equal(t, []blocklist.Store{first}, got, "the same store is listed once")
}

var listAll = page.Request{Key: "store", Limit: 100}

func storeNames(all []blocklist.Store) []string {
	out := make([]string, len(all))
	for i, s := range all {
		out[i] = s.Store
	}
	return out
}

func TestListIsSortedByStore(t *testing.T) {
	svc, _ := newService(t)
	for _, s := range []string{"temu.com", "aliexpress.com", "shein.com"} {
		block(t, svc, s)
	}
	got, total, next, err := svc.List(t.Context(), listAll)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, 3, total)
	require.Equal(t, []string{"aliexpress.com", "shein.com", "temu.com"}, storeNames(got))

	desc := listAll
	desc.Desc = true
	got, _, _, err = svc.List(t.Context(), desc)
	require.NoError(t, err)
	require.Equal(t, []string{"temu.com", "shein.com", "aliexpress.com"}, storeNames(got))
}

func TestListPagesByKeyset(t *testing.T) {
	svc, _ := newService(t)
	for _, s := range []string{"c.com", "a.com", "b.com"} {
		block(t, svc, s)
	}
	req := page.Request{Key: "store", Limit: 2}
	first, total, next, err := svc.List(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.NotNil(t, next)
	require.Equal(t, []string{"a.com", "b.com"}, storeNames(first))

	req.After = &page.Position{Value: "b.com", ID: first[1].ID}
	rest, total, next, err := svc.List(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Nil(t, next)
	require.Equal(t, []string{"c.com"}, storeNames(rest))
}

func TestMatchTakesTheStoreAndItsSubdomains(t *testing.T) {
	svc, _ := newService(t)
	block(t, svc, "temu.com")
	for _, u := range []string{
		"https://temu.com/x", "https://www.temu.com/x", "https://m.temu.com/x", "https://a.b.temu.com", "temu.com/p?q=1",
		"HTTPS://WWW.TEMU.COM/X", "https://temu.com:8443/x", "http://user:pw@temu.com/x", "https://temu.com#top",
		// A trailing dot names the same host.
		"https://temu.com./p/1", "https://m.temu.com../p", "https://www.temu.com./p", "https://temu.com.:8080/p",
		// The same host in full-width letters, after IDNA mapping.
		"https://ｔｅｍｕ.com/p",
	} {
		store, blocked, err := svc.Match(t.Context(), u)
		require.NoError(t, err)
		require.True(t, blocked, u)
		require.Equal(t, "temu.com", store, u)
	}
}

func TestMatchLeavesOtherHostsAlone(t *testing.T) {
	svc, _ := newService(t)
	block(t, svc, "temu.com")
	for _, u := range []string{
		"https://notemu.com", "https://temu.com.evil.example", "https://example.com/temu.com", "https://example.com/?u=temu.com",
		"https://com", "https://com.", "mailto:x", "", "   ", "not a url at all", "ftp://temu.com", "https://eu-temu.com",
	} {
		_, blocked, err := svc.Match(t.Context(), u)
		require.NoError(t, err)
		require.False(t, blocked, u)
	}
}

func TestMatchTakesAnInternationalizedHostInEitherForm(t *testing.T) {
	svc, _ := newService(t)
	block(t, svc, "bücher.de")
	for _, u := range []string{"https://xn--bcher-kva.de/x", "https://BÜCHER.de/x", "https://www.xn--bcher-kva.de./x"} {
		store, blocked, err := svc.Match(t.Context(), u)
		require.NoError(t, err)
		require.True(t, blocked, u)
		require.Equal(t, "bücher.de", store)
	}
}

func TestMatchStopsAtTheBlockedSubdomain(t *testing.T) {
	svc, _ := newService(t)
	block(t, svc, "shop.example.com")
	_, blocked, err := svc.Match(t.Context(), "https://example.com")
	require.NoError(t, err)
	require.False(t, blocked)
	_, blocked, err = svc.Match(t.Context(), "https://other.example.com")
	require.NoError(t, err)
	require.False(t, blocked)
	store, blocked, err := svc.Match(t.Context(), "https://x.shop.example.com")
	require.NoError(t, err)
	require.True(t, blocked)
	require.Equal(t, "shop.example.com", store)
}

func TestMatchNamesTheMostSpecificBlockedStore(t *testing.T) {
	svc, _ := newService(t)
	block(t, svc, "example.com")
	block(t, svc, "shop.example.com")
	store, blocked, err := svc.Match(t.Context(), "https://a.shop.example.com")
	require.NoError(t, err)
	require.True(t, blocked)
	require.Equal(t, "shop.example.com", store)
}

func TestMatchWithNoBlockedStoreBlocksNothing(t *testing.T) {
	svc, _ := newService(t)
	_, blocked, err := svc.Match(t.Context(), "https://temu.com")
	require.NoError(t, err)
	require.False(t, blocked)
}

func TestRemoveAllowsTheStoreAgain(t *testing.T) {
	svc, _ := newService(t)
	s := block(t, svc, "temu.com")
	require.NoError(t, svc.Remove(t.Context(), s.ID, nil))
	_, blocked, err := svc.Match(t.Context(), "https://temu.com")
	require.NoError(t, err)
	require.False(t, blocked)
	require.ErrorIs(t, svc.Remove(t.Context(), s.ID, nil), blocklist.ErrNotFound, "it is gone")
	require.ErrorIs(t, svc.Remove(t.Context(), 999, nil), blocklist.ErrNotFound)
}

func TestRemoveRunsTheGuardBeforeTheDelete(t *testing.T) {
	svc, _ := newService(t)
	s := block(t, svc, "temu.com")
	stop := errors.New("stop")
	err := svc.Remove(t.Context(), s.ID, func(_ context.Context, got blocklist.Store) error {
		require.Equal(t, s, got)
		return stop
	})
	require.ErrorIs(t, err, stop)
	_, blocked, err := svc.Match(t.Context(), "https://temu.com")
	require.NoError(t, err)
	require.True(t, blocked, "the guard stopped the delete")
}

func TestGetFindsAStoreByID(t *testing.T) {
	svc, _ := newService(t)
	s := block(t, svc, "temu.com")
	got, err := svc.Get(t.Context(), s.ID)
	require.NoError(t, err)
	require.Equal(t, s, got)
	_, err = svc.Get(t.Context(), s.ID+1)
	require.ErrorIs(t, err, blocklist.ErrNotFound)
}

type endsAt struct{ res extract.Result }

func (f endsAt) Fetch(context.Context, string) (extract.Result, error) { return f.res, nil }

// The cache row below is written by the real pipeline, so this test breaks if
// the cached JSON stops carrying the final address at "URL", which the cache
// check in blocklist.go reads.
func TestMatchTakesTheAddressOfAReadingTheExtractionPipelineCached(t *testing.T) {
	svc, db := newService(t)
	block(t, svc, "temu.com")
	pipe := pipeline.New(pipeline.Options{
		DB: db,
		Direct: endsAt{extract.Result{
			Product: extract.Product{Title: "Case", Price: "10.00", Currency: "USD", Images: []string{"https://s.test/i.jpg"}},
			URL:     "https://www.temu.com/p/1", Store: "temu.com", Status: extract.StatusOK,
		}},
		Registerer: prometheus.NewRegistry(),
	})
	_, err := pipe.Extract(t.Context(), 1, "https://short.example/abc")
	require.NoError(t, err)

	store, blocked, err := svc.Match(t.Context(), "https://short.example/abc")
	require.NoError(t, err)
	require.True(t, blocked)
	require.Equal(t, "temu.com", store)
}

func TestMatchTakesTheAddressACachedReadingEndedAt(t *testing.T) {
	svc, db := newService(t)
	block(t, svc, "temu.com")
	_, err := db.ExecContext(t.Context(), `INSERT INTO extraction_cache (url, result, created_at) VALUES (?, ?, '2020-01-01 00:00:00')`,
		"https://short.example/abc", `{"Title":"Case","URL":"https://www.temu.com/p/1","Store":"temu.com"}`)
	require.NoError(t, err)
	store, blocked, err := svc.Match(t.Context(), "short.example/abc")
	require.NoError(t, err)
	require.True(t, blocked, "a short link that ended at a blocked store")
	require.Equal(t, "temu.com", store)

	_, blocked, err = svc.Match(t.Context(), "https://short.example/other")
	require.NoError(t, err)
	require.False(t, blocked, "no reading: the typed host is all there is")
}

func TestMatchIgnoresACachedReadingThatIsNotJSON(t *testing.T) {
	svc, db := newService(t)
	block(t, svc, "temu.com")
	_, err := db.ExecContext(t.Context(), `INSERT INTO extraction_cache (url, result, created_at) VALUES (?, ?, '2020-01-01 00:00:00')`,
		"https://short.example/abc", `not json`)
	require.NoError(t, err)
	_, blocked, err := svc.Match(t.Context(), "https://short.example/abc")
	require.NoError(t, err)
	require.False(t, blocked)
}
