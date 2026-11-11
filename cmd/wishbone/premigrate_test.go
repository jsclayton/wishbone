package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/store"
)

func newDataDirStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(dir, "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func automaticSnapshots(t *testing.T, dir string) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, "backups", "pre-migrate-*"))
	require.NoError(t, err)
	return got
}

// A new install gets a snapshot of its empty database, then the baseline.
func TestMigrateWritesASnapshotFirst(t *testing.T) {
	dir := t.TempDir()
	st := newDataDirStore(t, dir)
	now := func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }

	require.NoError(t, migrate(t.Context(), st, dir, now, slog.New(slog.DiscardHandler)))

	snaps := automaticSnapshots(t, dir)
	require.Len(t, snaps, 1)
	require.Regexp(t, `pre-migrate-0-to-20261110030443-20261108T120000Z$`, snaps[0])
	require.FileExists(t, filepath.Join(snaps[0], "wishbone.db"))
	require.DirExists(t, filepath.Join(snaps[0], "images"))

	v, err := store.Version(t.Context(), st.DB())
	require.NoError(t, err)
	require.Equal(t, store.BaselineVersion, v, "the baseline ran after the snapshot")

	// Nothing is pending now, so a second start writes no snapshot.
	require.NoError(t, migrate(t.Context(), st, dir, func() time.Time { return now().Add(time.Hour) }, slog.New(slog.DiscardHandler)))
	require.Len(t, automaticSnapshots(t, dir), 1)
}

func TestMigrateAppliesNothingWhenTheSnapshotFails(t *testing.T) {
	dir := t.TempDir()
	st := newDataDirStore(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "backups"), []byte("not a dir"), 0o600))

	err := migrate(t.Context(), st, dir, time.Now, slog.New(slog.DiscardHandler))
	require.ErrorContains(t, err, "snapshot before migrations")

	v, err := store.Version(t.Context(), st.DB())
	require.NoError(t, err)
	require.Zero(t, v)
}

// A database at the baseline's version has nothing to migrate. The server
// writes no snapshot, and keeps a pre-baseline snapshot that is in the data dir.
func TestMigrateOnADatabaseAtTheBaselineWritesNothingAndKeepsAPreBaselineSnapshot(t *testing.T) {
	dir, st := newDB(t)
	preBaseline := filepath.Join(dir, "backups", "pre-migrate-20261105204058-to-20261110030443-20261110T164723Z")
	require.NoError(t, os.MkdirAll(preBaseline, 0o750))

	require.NoError(t, migrate(t.Context(), st, dir, time.Now, slog.New(slog.DiscardHandler)))

	require.Equal(t, []string{preBaseline}, automaticSnapshots(t, dir), "no new snapshot, and the old one stays")
}

// Only serve applies migrations. The baseline is pending on a new database, so
// every other command that opens it says to start the server first.
func TestACommandRefusesADatabaseWhoseMigrationIsPending(t *testing.T) {
	dir, _ := newPendingDB(t)

	err := (&UsersCmd{DataDir: dir}).Run()

	require.ErrorIs(t, err, store.ErrMigrationPending)
}
