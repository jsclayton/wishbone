package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/backup"
	"jsclayton.dev/wishbone/internal/store"
)

// newDB creates a migrated database in a temp data dir and returns the dir.
func newDB(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	return dir, st
}

// newPendingDB creates a data dir whose database is new: it has no table yet,
// so the baseline migration is pending, and only serve applies it.
func newPendingDB(t *testing.T) (string, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return dir, st
}

func TestTheCLIHasTheCommandsThatExistSoFar(t *testing.T) {
	var cli CLI
	k, err := kong.New(&cli)
	require.NoError(t, err)

	var names []string
	for _, c := range k.Model.Children {
		names = append(names, c.Name)
	}
	require.ElementsMatch(t, []string{"serve", "users", "set-admin", "delete-user", "login-link", "backup", "healthcheck", "version"}, names)
}

func TestBackupCmd(t *testing.T) {
	dir, _ := newDB(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "images", "ab"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "images", "ab", "abcd.jpg"), []byte("x"), 0o600))
	target := filepath.Join(t.TempDir(), "snap")

	var out bytes.Buffer
	require.NoError(t, (&BackupCmd{DataDir: dir, Path: target, out: &out}).Run())
	require.Contains(t, out.String(), "Copied 1 images")
	require.FileExists(t, filepath.Join(target, "wishbone.db"))

	err := (&BackupCmd{DataDir: dir, Path: target, out: &out}).Run()
	require.ErrorIs(t, err, backup.ErrTargetExists)

	err = (&BackupCmd{DataDir: t.TempDir(), Path: filepath.Join(t.TempDir(), "x")}).Run()
	require.ErrorContains(t, err, "no database")
}

func TestBackupCmdCopiesEveryImageOfTheDatabase(t *testing.T) {
	dir, st := newDB(t)
	const key = "ab0123456789abcdef0123456789abcd"
	_, err := st.DB().Exec(`INSERT INTO images (key, width, height, created_at) VALUES (?, 1, 1, CURRENT_TIMESTAMP)`, key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "images", key[:2]), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "images", key[:2], key+".jpg"), []byte("x"), 0o600))
	target := filepath.Join(t.TempDir(), "snap")

	var out bytes.Buffer
	require.NoError(t, (&BackupCmd{DataDir: dir, Path: target, out: &out}).Run())

	require.Contains(t, out.String(), "Copied 1 images")
	require.FileExists(t, filepath.Join(target, "images", key[:2], key+".jpg"))
	snap, err := store.Open(t.Context(), filepath.Join(target, "wishbone.db"))
	require.NoError(t, err)
	defer snap.Close()
	var got string
	require.NoError(t, snap.DB().Get(&got, `SELECT key FROM images`))
	require.Equal(t, key, got)
}

func TestBackupCmdReportsMissingImages(t *testing.T) {
	dir, st := newDB(t)
	const key = "ab0123456789abcdef0123456789abcd"
	_, err := st.DB().Exec(`INSERT INTO images (key, width, height, created_at) VALUES (?, 1, 1, CURRENT_TIMESTAMP)`, key)
	require.NoError(t, err)
	target := filepath.Join(t.TempDir(), "snap")

	var out bytes.Buffer
	err = (&BackupCmd{DataDir: dir, Path: target, out: &out}).Run()
	require.ErrorIs(t, err, backup.ErrMissingImages)
	require.ErrorContains(t, err, key)
	require.Contains(t, out.String(), "Wrote database to")
	require.FileExists(t, filepath.Join(target, "wishbone.db"))
}

func TestHealthcheckProbesTheV2Route(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/health", r.URL.Path)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(ts.Close)
	require.NoError(t, (&HealthcheckCmd{URL: ts.URL}).Run())
}

func TestHealthcheckFailsOnUnhealthyOrUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(ts.Close)
	require.ErrorContains(t, (&HealthcheckCmd{URL: ts.URL}).Run(), "503")

	url := ts.URL
	ts.Close()
	require.Error(t, (&HealthcheckCmd{URL: url}).Run())
}
