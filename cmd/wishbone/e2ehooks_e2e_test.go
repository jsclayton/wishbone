//go:build e2e

package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestE2EBuildRevealEndpointNeedsTheSwitch(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Setenv("WISHBONE_E2E_HOOKS", "")
	rec := httptest.NewRecorder()
	withE2EHooks(next, nil, logger).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, e2eRevealPath, nil))
	require.Equal(t, http.StatusTeapot, rec.Code)

	t.Setenv("WISHBONE_E2E_HOOKS", "true")
	rec = httptest.NewRecorder()
	withE2EHooks(next, nil, logger).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, e2eRevealPath, strings.NewReader(`{"recipient":0}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code, "the hook answers, and checks its input")
}
