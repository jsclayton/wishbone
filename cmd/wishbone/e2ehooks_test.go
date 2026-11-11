//go:build !e2e

package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// The default build has no e2e endpoints, even if the environment variable the
// e2e build reads is set.
func TestDefaultBuildHasNoE2EEndpoints(t *testing.T) {
	t.Setenv("WISHBONE_E2E_HOOKS", "true")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := withE2EHooks(next, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/e2e/reveal", nil))
	require.Equal(t, http.StatusTeapot, rec.Code, "the request reaches the app, not a hook")
}
