package main

import (
	"fmt"
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

// A new install gets a snapshot of its empty database, then the migrations.
func TestMigrateWritesASnapshotFirst(t *testing.T) {
	dir := t.TempDir()
	st := newDataDirStore(t, dir)
	now := func() time.Time { return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC) }

	require.NoError(t, migrate(t.Context(), st, dir, now, slog.New(slog.DiscardHandler)))

	v, err := store.Version(t.Context(), st.DB())
	require.NoError(t, err)
	require.GreaterOrEqual(t, v, store.BaselineVersion, "the migrations ran after the snapshot")

	snaps := automaticSnapshots(t, dir)
	require.Len(t, snaps, 1)
	require.Regexp(t, fmt.Sprintf(`pre-migrate-0-to-%d-20261108T120000Z$`, v), snaps[0])
	require.FileExists(t, filepath.Join(snaps[0], "wishbone.db"))
	require.DirExists(t, filepath.Join(snaps[0], "images"))

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

// A database that has every migration has nothing to migrate. The server
// writes no snapshot, and keeps a pre-baseline snapshot that is in the data dir.
func TestMigrateOnAMigratedDatabaseWritesNothingAndKeepsAPreBaselineSnapshot(t *testing.T) {
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

// The IDs migration (20261121013109) rebuilds every table that has an ID. The
// snapshot before it holds the database as it was, with its images. A server
// that starts on the restored snapshot migrates it, and every row keeps its ID.
func TestTheSnapshotBeforeTheIDsMigrationRestoresAndMigrates(t *testing.T) {
	const before, ids = 20261120145721, 20261121013109
	const imageKey = "ab0123456789abcdef0123456789abcd"
	dir := t.TempDir()
	st := newDataDirStore(t, dir)
	require.NoError(t, st.MigrateTo(t.Context(), before))
	exec(t, st, `INSERT INTO users (id, name, birthday_month, birthday_day, created_at) VALUES
		(1, 'Ada', 1, 1, CURRENT_TIMESTAMP), (4, 'Grace', 1, 1, CURRENT_TIMESTAMP)`)
	exec(t, st, `INSERT INTO images (id, key, width, height, uploaded_by, created_at) VALUES (6, ?, 1, 1, 4, CURRENT_TIMESTAMP)`, imageKey)
	exec(t, st, `UPDATE users SET avatar_image_id = 6 WHERE id = 4`)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "images", "ab"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "images", "ab", imageKey+".jpg"), []byte("jpeg"), 0o600))
	now := func() time.Time { return time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC) }

	require.NoError(t, migrate(t.Context(), st, dir, now, slog.New(slog.DiscardHandler)))

	v, err := store.Version(t.Context(), st.DB())
	require.NoError(t, err)
	require.GreaterOrEqual(t, v, int64(ids))
	snaps := automaticSnapshots(t, dir)
	require.Len(t, snaps, 1)
	require.Regexp(t, fmt.Sprintf(`pre-migrate-%d-to-%d-20261121T120000Z$`, before, v), snaps[0])

	// A restore puts the snapshot's files in place of the data (docs/operations.md, Restore).
	restored := t.TempDir()
	require.NoError(t, os.CopyFS(restored, os.DirFS(snaps[0])))
	require.FileExists(t, filepath.Join(restored, "images", "ab", imageKey+".jpg"))
	rst := newDataDirStore(t, restored)
	v, err = store.Version(t.Context(), rst.DB())
	require.NoError(t, err)
	require.Equal(t, int64(before), v, "the snapshot is of the database before the migration")

	require.NoError(t, migrate(t.Context(), rst, restored, now, slog.New(slog.DiscardHandler)))

	v, err = store.Version(t.Context(), rst.DB())
	require.NoError(t, err)
	require.GreaterOrEqual(t, v, int64(ids))
	require.Equal(t, 1, count(t, rst, `SELECT count(*) FROM users WHERE id = 4 AND name = 'Grace' AND avatar_image_id = 6`))
	require.Equal(t, 1, count(t, rst, `SELECT count(*) FROM images WHERE id = 6 AND key = ? AND uploaded_by = 4`, imageKey))
	require.Len(t, automaticSnapshots(t, restored), 1, "the restored database got its own snapshot first")
	exec(t, rst, `UPDATE users SET avatar_image_id = NULL WHERE id = 4`)
	exec(t, rst, `DELETE FROM images WHERE id = 6`)
	exec(t, rst, `INSERT INTO images (key, width, height, created_at) VALUES ('cd', 1, 1, CURRENT_TIMESTAMP)`)
	require.Equal(t, 7, count(t, rst, `SELECT max(id) FROM images`), "the ID of the deleted image doesn't come back")
}
