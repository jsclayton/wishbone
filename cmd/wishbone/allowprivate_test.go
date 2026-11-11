//go:build !e2e

package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// The default build has no switch for private addresses, even if the
// environment variable the e2e build reads is set.
func TestDefaultBuildNeverAllowsPrivateAddresses(t *testing.T) {
	t.Setenv("WISHBONE_EXTRACT_ALLOW_PRIVATE", "true")
	require.False(t, allowPrivateAddrs(slog.New(slog.NewTextHandler(io.Discard, nil))))
}
