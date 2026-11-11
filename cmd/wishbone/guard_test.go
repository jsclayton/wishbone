package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/config"
	"jsclayton.dev/wishbone/internal/store"
)

// newerDB makes a data dir whose database a newer wishbone has migrated.
func newerDB(t *testing.T) string {
	t.Helper()
	dir, st := newDB(t)
	v, err := store.Version(t.Context(), st.DB())
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(),
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, v+1)
	require.NoError(t, err)
	require.NoError(t, st.Close())
	return dir
}

func TestServeRefusesANewerDatabaseBeforeAnySnapshot(t *testing.T) {
	dir := newerDB(t)

	err := (&ServeCmd{Config: config.Config{PublicURL: "https://wish.example", DataDir: dir, RateLimitRedeem: 10}}).Run()

	require.ErrorIs(t, err, store.ErrDatabaseNewer)
	require.NoDirExists(t, filepath.Join(dir, "backups"))
}

func TestBackupRefusesANewerDatabase(t *testing.T) {
	dir := newerDB(t)
	dest := filepath.Join(t.TempDir(), "snap")

	err := (&BackupCmd{DataDir: dir, Path: dest}).Run()

	require.ErrorIs(t, err, store.ErrDatabaseNewer)
	require.NoDirExists(t, dest)
}

// tooOldDB makes a data dir whose database has migrations but not the
// baseline, so it is older than this release supports.
func tooOldDB(t *testing.T) string {
	t.Helper()
	dir, st := newDB(t)
	_, err := st.DB().ExecContext(t.Context(), `DELETE FROM goose_db_version WHERE version_id > 0`)
	require.NoError(t, err)
	for _, v := range []int64{1, 20261027160910, 20261105204058} {
		_, err = st.DB().ExecContext(t.Context(), `INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, v)
		require.NoError(t, err)
	}
	require.NoError(t, st.Close())
	return dir
}

func TestServeRefusesADatabaseOlderThanTheBaselineBeforeAnySnapshot(t *testing.T) {
	dir := tooOldDB(t)

	err := (&ServeCmd{Config: config.Config{PublicURL: "https://wish.example", DataDir: dir, RateLimitRedeem: 10}}).Run()

	require.ErrorIs(t, err, store.ErrDatabaseTooOld)
	require.ErrorContains(t, err, "older than this release supports")
	require.NoDirExists(t, filepath.Join(dir, "backups"))
}

func TestBackupAndUsersRefuseADatabaseOlderThanTheBaseline(t *testing.T) {
	dir := tooOldDB(t)
	dest := filepath.Join(t.TempDir(), "snap")

	require.ErrorIs(t, (&BackupCmd{DataDir: dir, Path: dest}).Run(), store.ErrDatabaseTooOld)
	require.NoDirExists(t, dest)
	require.ErrorIs(t, (&UsersCmd{DataDir: dir}).Run(), store.ErrDatabaseTooOld)
}
