//go:build !e2e

package main

import (
	"log/slog"
	"net/http"

	"jsclayton.dev/wishbone/internal/gift"
)

// withE2EHooks returns h unchanged. Release builds have no test endpoints,
// whatever the environment says. The e2e build adds them (see e2ehooks_e2e.go).
func withE2EHooks(h http.Handler, _ *gift.Revealer, _ *slog.Logger) http.Handler { return h }
