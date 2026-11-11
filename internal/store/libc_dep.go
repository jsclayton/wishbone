package store

import (
	// Keep modernc.org/libc a direct dependency so `go mod tidy` does not
	// demote it. Its version MUST match the one modernc.org/sqlite requires:
	// https://pkg.go.dev/modernc.org/sqlite#hdr-Fragile_modernc_org_libc_dependency
	_ "modernc.org/libc"
)
