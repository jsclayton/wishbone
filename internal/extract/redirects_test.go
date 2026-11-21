package extract_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/netguard"
	"jsclayton.dev/wishbone/internal/netguard/netguardtest"
)

func TestRedirectsFollowsEveryHopAndReadsNoBody(t *testing.T) {
	var final atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", http.StatusFound) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/c", http.StatusMovedPermanently) })
	mux.HandleFunc("/c", func(w http.ResponseWriter, _ *http.Request) { final.Add(1); _, _ = w.Write([]byte("page")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r := extract.NewRedirects(extract.RedirectsOptions{AllowPrivateAddrs: true})

	chain, err := r.Follow(t.Context(), srv.URL+"/a")
	require.NoError(t, err)
	require.Equal(t, []string{srv.URL + "/b", srv.URL + "/c"}, chain)

	chain, err = r.Follow(t.Context(), srv.URL+"/c")
	require.NoError(t, err)
	require.Empty(t, chain)
}

func TestRedirectsStopsAtTheLimitAndKeepsTheChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, r.URL.Path+"x", http.StatusFound) }))
	defer srv.Close()
	r := extract.NewRedirects(extract.RedirectsOptions{AllowPrivateAddrs: true, MaxRedirects: 3})
	chain, err := r.Follow(t.Context(), srv.URL+"/")
	require.ErrorIs(t, err, extract.ErrTooManyRedirects)
	require.Len(t, chain, 4)
}

func TestRedirectsTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	r := extract.NewRedirects(extract.RedirectsOptions{AllowPrivateAddrs: true, Timeout: 50 * time.Millisecond})
	_, err := r.Follow(t.Context(), srv.URL)
	require.Error(t, err)
}

func TestRedirectsRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	r := extract.NewRedirects(extract.RedirectsOptions{})
	_, err := r.Follow(t.Context(), srv.URL)
	require.Error(t, err)
}

func TestRedirectsFollowsOnlyRedirectStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusOK)
		case "/created":
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusCreated)
		case "/not-modified":
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(http.StatusNotModified)
		}
	}))
	defer srv.Close()
	r := extract.NewRedirects(extract.RedirectsOptions{AllowPrivateAddrs: true})
	for _, p := range []string{"/ok", "/created", "/not-modified"} {
		chain, err := r.Follow(t.Context(), srv.URL+p)
		require.NoError(t, err, p)
		require.Empty(t, chain, p)
	}
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/a" {
				w.Header().Set("Location", "/b")
				w.WriteHeader(code)
			}
		}))
		chain, err := r.Follow(t.Context(), hop.URL+"/a")
		hop.Close()
		require.NoError(t, err, code)
		require.Equal(t, []string{hop.URL + "/b"}, chain, code)
	}
}

// short.test resolves only through the fake DNS, so Follow reaches the server
// only if it looks names up with RedirectsOptions.Resolver.
func TestRedirectsLooksUpHostsWithTheResolverOption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
		}
	}))
	defer srv.Close()
	resolver := netguardtest.Resolver(t, map[string][]netip.Addr{"short.test": {netip.MustParseAddr("127.0.0.1")}})
	target := strings.Replace(srv.URL, "127.0.0.1", "short.test", 1)

	chain, err := extract.NewRedirects(extract.RedirectsOptions{AllowPrivateAddrs: true, Resolver: resolver}).Follow(t.Context(), target+"/a")
	require.NoError(t, err)
	require.Equal(t, []string{target + "/b"}, chain)

	_, err = extract.NewRedirects(extract.RedirectsOptions{Resolver: resolver}).Follow(t.Context(), target+"/a")
	require.ErrorIs(t, err, netguard.ErrPrivateAddress, "the private address check runs on the resolver's answer")
}
