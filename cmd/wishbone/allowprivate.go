//go:build !e2e

package main

import "log/slog"

// allowPrivateAddrs reports whether link extraction and image downloads may
// connect to loopback and private addresses. Release builds never allow it,
// so a pasted URL can't reach the host's network. Only the e2e build can turn
// it on (see allowprivate_e2e.go).
func allowPrivateAddrs(*slog.Logger) bool { return false }
