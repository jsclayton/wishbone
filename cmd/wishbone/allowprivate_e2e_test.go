//go:build e2e

package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestE2EBuildAllowsPrivateAddressesAndWarns(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))

	t.Setenv("WISHBONE_EXTRACT_ALLOW_PRIVATE", "")
	require.False(t, allowPrivateAddrs(logger))
	require.Empty(t, out.String())

	t.Setenv("WISHBONE_EXTRACT_ALLOW_PRIVATE", "true")
	require.True(t, allowPrivateAddrs(logger))
	require.Contains(t, out.String(), "level=WARN")
}
