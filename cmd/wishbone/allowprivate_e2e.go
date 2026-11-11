//go:build e2e

package main

import (
	"log/slog"
	"os"
)

// allowPrivateAddrs is the e2e build's switch. With WISHBONE_EXTRACT_ALLOW_PRIVATE=true,
// link extraction and image downloads may connect to loopback and private
// addresses, so the browser tests can point them at a fixture page. This file
// is compiled only with -tags e2e: `just build-e2e`, never `just build` or
// the Dockerfile.
func allowPrivateAddrs(logger *slog.Logger) bool {
	if os.Getenv("WISHBONE_EXTRACT_ALLOW_PRIVATE") != "true" {
		return false
	}
	logger.Warn("e2e build: link extraction and image downloads may reach private addresses. Never ship this binary")
	return true
}
