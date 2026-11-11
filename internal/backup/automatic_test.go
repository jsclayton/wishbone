package backup_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/backup"
	"jsclayton.dev/wishbone/internal/store"
)

var at = time.Date(2026, 11, 8, 17, 4, 5, 0, time.FixedZone("EST", -5*3600))

func backupsIn(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "backups"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAutomaticWritesASnapshotNamedForTheMigration(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	addImage(t, st.DB(), oldKey)
	writeImage(t, dir, oldKey)

	res, err := backup.Automatic(t.Context(), st.DB(), dir, 20261110030443, 20261201000000, at)
	require.NoError(t, err)

	snap := filepath.Join(dir, "backups", "pre-migrate-20261110030443-to-20261201000000-20261108T220405Z")
	require.Equal(t, filepath.Join(snap, "wishbone.db"), res.Database)
	require.FileExists(t, filepath.Join(snap, "wishbone.db"))
	require.FileExists(t, filepath.Join(snap, "images", oldKey[:2], oldKey+".jpg"))
	require.Equal(t, 1, res.Images)
}

// The server never migrates without a whole snapshot, so a snapshot with a
// missing image counts as failed and is removed. wishbone backup keeps it.
func TestAutomaticRemovesASnapshotWithAMissingImage(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	addImage(t, st.DB(), oldKey) // no file on disk

	_, err := backup.Automatic(t.Context(), st.DB(), dir, 1, 2, at)
	require.ErrorIs(t, err, backup.ErrMissingImages)
	require.Empty(t, backupsIn(t, dir))
}

func TestAutomaticFailsWhenItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "backups"), []byte("not a dir"), 0o600))

	_, err := backup.Automatic(t.Context(), st.DB(), dir, 1, 2, at)
	require.Error(t, err)
}

func TestAutomaticKeepsTheThreeNewestAutomaticSnapshots(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	// A snapshot from wishbone backup, and other files the operator keeps.
	for _, p := range []string{"2025-01-01", "pre-migrate-notes", "pre-migrate-1-to-2-yesterday"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "backups", p), 0o750))
	}

	// The from version doesn't order snapshots: the time does.
	froms := []int64{50, 40, 30, 20, 10}
	for i, from := range froms {
		_, err := backup.Automatic(t.Context(), st.DB(), dir, from, from+1, at.Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
	}

	require.ElementsMatch(t, []string{
		"2025-01-01", "pre-migrate-notes", "pre-migrate-1-to-2-yesterday",
		"pre-migrate-30-to-31-20261109T000405Z",
		"pre-migrate-20-to-21-20261109T010405Z",
		"pre-migrate-10-to-11-20261109T020405Z",
	}, backupsIn(t, dir))
}

// A failed snapshot doesn't count as one of the newest, so it removes none of
// the snapshots before it.
func TestAutomaticFailureRemovesNoOlderSnapshot(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	for i := range 3 {
		_, err := backup.Automatic(t.Context(), st.DB(), dir, int64(i), int64(i+1), at.Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
	}
	addImage(t, st.DB(), oldKey) // no file on disk
	_, err := backup.Automatic(t.Context(), st.DB(), dir, 3, 4, at.Add(3*time.Hour))
	require.ErrorIs(t, err, backup.ErrMissingImages)
	require.Len(t, backupsIn(t, dir), 3)
}

// A snapshot that can't be removed doesn't undo the new one: it is whole, so
// the caller can still migrate.
func TestAutomaticReportsAFailedPruneAndKeepsTheNewSnapshot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove files from a read-only folder")
	}
	dir := t.TempDir()
	st := newStore(t, dir)
	writeImage(t, dir, oldKey)
	for i := range 3 {
		_, err := backup.Automatic(t.Context(), st.DB(), dir, int64(i), int64(i+1), at.Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
	}
	// The oldest snapshot's image folder can't be emptied.
	locked := filepath.Join(dir, "backups", "pre-migrate-0-to-1-20261108T220405Z", "images", oldKey[:2])
	require.NoError(t, os.Chmod(locked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

	res, err := backup.Automatic(t.Context(), st.DB(), dir, 3, 4, at.Add(3*time.Hour))
	require.ErrorIs(t, err, backup.ErrPrune)
	require.FileExists(t, res.Database)
}

// baseline is the version of the baseline migration in these tests. A snapshot
// from before it, to it or later, is a pre-baseline snapshot.
const baseline = 100

func writeBaselineAware(t *testing.T, st *store.Store, dir string, from, to int64, at time.Time) string {
	t.Helper()
	res, err := backup.AutomaticWithBaseline(t.Context(), st.DB(), dir, from, to, baseline, at)
	require.NoError(t, err)
	return filepath.Base(filepath.Dir(res.Database))
}

// A pre-baseline snapshot is the only copy of a database that no release
// migrates any more, so the server never deletes it, however many snapshots
// come after.
func TestAutomaticNeverDeletesAPreBaselineSnapshot(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	kept := writeBaselineAware(t, st, dir, 50, 200, at)
	for i := range 6 {
		writeBaselineAware(t, st, dir, 200+int64(i), 201+int64(i), at.Add(time.Duration(i+1)*time.Hour))
	}

	names := backupsIn(t, dir)

	require.Contains(t, names, kept)
	require.Len(t, names, 4, "the pre-baseline snapshot, and the 3 newest of the others")
}

// A new install has nothing older than the baseline. Its snapshot, from version 0, is like any other.
func TestAutomaticTreatsTheSnapshotOfANewInstallAsAnyOther(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	first := writeBaselineAware(t, st, dir, 0, 200, at)
	for i := range 3 {
		writeBaselineAware(t, st, dir, 200+int64(i), 201+int64(i), at.Add(time.Duration(i+1)*time.Hour))
	}

	require.NotContains(t, backupsIn(t, dir), first)
}

// When a new pre-baseline snapshot is written, it replaces the older
// pre-baseline snapshot. A snapshot from wishbone backup and the snapshots of
// other migrations stay.
func TestAutomaticKeepsOnlyTheNewestPreBaselineSnapshot(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "backups", "2025-01-01"), 0o750))
	older := writeBaselineAware(t, st, dir, 50, 200, at)
	other := writeBaselineAware(t, st, dir, 200, 201, at.Add(time.Hour))
	newer := writeBaselineAware(t, st, dir, 50, 200, at.Add(2*time.Hour))

	names := backupsIn(t, dir)

	require.NotContains(t, names, older)
	require.ElementsMatch(t, []string{"2025-01-01", other, newer}, names)
}

// A pre-baseline snapshot that fails is removed and prunes nothing, so the
// older one stays: it is still the only copy of its database.
func TestAutomaticAFailedPreBaselineSnapshotKeepsTheOlderOne(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	older := writeBaselineAware(t, st, dir, 50, 200, at)
	addImage(t, st.DB(), oldKey) // no file on disk

	_, err := backup.AutomaticWithBaseline(t.Context(), st.DB(), dir, 50, 200, baseline, at.Add(time.Hour))

	require.ErrorIs(t, err, backup.ErrMissingImages)
	require.Equal(t, []string{older}, backupsIn(t, dir))
}

// A data dir can hold a pre-baseline snapshot. It is recognized by the
// baseline's version. No later snapshot deletes it, however many are taken.
func TestAutomaticNeverDeletesAPreBaselineSnapshotThatIsAlreadyInTheDataDir(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	preBaseline := "pre-migrate-20261105204058-to-20261110030443-20261110T164723Z"
	manual := "wishbone-2026-11-08"
	for _, p := range []string{preBaseline, manual, "pre-migrate-20261104041033-to-20261105204058-20261108T220405Z"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "backups", p), 0o750))
	}

	var written []string
	for i := range 5 {
		from := store.BaselineVersion + int64(i)
		res, err := backup.AutomaticWithBaseline(t.Context(), st.DB(), dir, from, from+1, store.BaselineVersion, at.Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
		written = append(written, filepath.Base(filepath.Dir(res.Database)))
	}

	names := backupsIn(t, dir)
	require.Contains(t, names, preBaseline)
	require.Contains(t, names, manual, "a snapshot from wishbone backup stays")
	require.NotContains(t, names, "pre-migrate-20261104041033-to-20261105204058-20261108T220405Z",
		"a snapshot that ends before the baseline is an ordinary automatic one, so it ages out")
	require.Subset(t, names, written[2:], "the 3 newest of the others")
	require.Len(t, names, 5)
}
