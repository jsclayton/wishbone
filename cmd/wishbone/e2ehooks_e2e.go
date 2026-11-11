//go:build e2e

package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"jsclayton.dev/wishbone/internal/gift"
)

// e2eRevealPath is the e2e build's clock jump. POST {"recipient": <user id>,
// "advance": "<Go duration>"} gives the gifts on the lists that user is a
// recipient of, once the clock is that far ahead of now, and answers
// {"revealed": n}.
//
// It does not move the server's clock. All e2e specs share one server and one
// database, and a global clock jump (or a reveal for every list) would change
// what other specs see: upcoming dates, "today", their open gifts. A spec makes
// its own recipient and jumps for that recipient only.
const e2eRevealPath = "/api/e2e/reveal"

// withE2EHooks adds the e2e endpoints when WISHBONE_E2E_HOOKS=true. This file
// is compiled only with -tags e2e: `just build-e2e`, never `just build` or the
// Dockerfile.
func withE2EHooks(h http.Handler, rev *gift.Revealer, logger *slog.Logger) http.Handler {
	if os.Getenv("WISHBONE_E2E_HOOKS") != "true" {
		return h
	}
	logger.Warn("e2e build: test endpoints are on (" + e2eRevealPath + "). Never ship this binary")
	mux := http.NewServeMux()
	mux.Handle("POST "+e2eRevealPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Recipient int64  `json:"recipient"`
			Advance   string `json:"advance"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil || in.Recipient <= 0 {
			http.Error(w, `{"error":"want {\"recipient\": id, \"advance\": \"720h\"}"}`, http.StatusBadRequest)
			return
		}
		d, err := time.ParseDuration(in.Advance)
		if err != nil {
			http.Error(w, `{"error":"advance is not a duration"}`, http.StatusBadRequest)
			return
		}
		n, err := rev.RunFor(r.Context(), in.Recipient, time.Now().Add(d))
		if err != nil {
			logger.Error("e2e reveal failed", slog.Any("error", err))
			http.Error(w, `{"error":"reveal failed"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"revealed": n})
	}))
	mux.Handle("/", h)
	return mux
}
