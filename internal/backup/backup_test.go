package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/backup"
	"jsclayton.dev/wishbone/internal/store"
)

func newStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(dir, "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(context.Background()))
	return st
}

const key = "ab0123456789abcdef0123456789abcd"

func TestRunSnapshotsWhileWriterActive(t *testing.T) {
	dataDir := t.TempDir()
	st := newStore(t, dataDir)
	ctx := context.Background()
	_, err := st.DB().ExecContext(ctx, `CREATE TABLE backup_probe (n INTEGER)`)
	require.NoError(t, err)
	_, err = st.DB().ExecContext(ctx, `INSERT INTO backup_probe VALUES (1)`)
	require.NoError(t, err)

	imgDir := filepath.Join(dataDir, "images", "ab")
	require.NoError(t, os.MkdirAll(imgDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(imgDir, key+".jpg"), []byte("jpeg"), 0o600))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = st.DB().ExecContext(ctx, `INSERT INTO backup_probe VALUES (2)`)
			}
		}
	}()

	target := filepath.Join(t.TempDir(), "snap")
	res, err := backup.Run(ctx, st.DB(), dataDir, target)
	close(stop)
	wg.Wait()
	require.NoError(t, err)
	require.Equal(t, 1, res.Images)

	snap, err := store.Open(t.Context(), filepath.Join(target, "wishbone.db"))
	require.NoError(t, err)
	defer snap.Close()
	var n int
	require.NoError(t, snap.DB().Get(&n, `SELECT COUNT(*) FROM backup_probe WHERE n = 1`))
	require.Equal(t, 1, n)
	var check string
	require.NoError(t, snap.DB().Get(&check, `PRAGMA integrity_check`))
	require.Equal(t, "ok", check)

	got, err := os.ReadFile(filepath.Join(target, "images", "ab", key+".jpg"))
	require.NoError(t, err)
	require.Equal(t, "jpeg", string(got))
}

func TestRunWithoutImagesDir(t *testing.T) {
	dataDir := t.TempDir()
	st := newStore(t, dataDir)
	target := filepath.Join(t.TempDir(), "snap")
	res, err := backup.Run(context.Background(), st.DB(), dataDir, target)
	require.NoError(t, err)
	require.Zero(t, res.Images)
	require.DirExists(t, filepath.Join(target, "images"))
}

func TestRunRefusesExistingTarget(t *testing.T) {
	dataDir := t.TempDir()
	st := newStore(t, dataDir)
	target := t.TempDir() // exists
	marker := filepath.Join(target, "keep")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))

	_, err := backup.Run(context.Background(), st.DB(), dataDir, target)
	require.ErrorIs(t, err, backup.ErrTargetExists)
	require.FileExists(t, marker)
	require.NoFileExists(t, filepath.Join(target, "wishbone.db"))
}

const (
	oldKey = "aa0123456789abcdef0123456789abcd"
	newKey = "bb0123456789abcdef0123456789abcd"
)

// addImage records an image in the images table. The file is the test's to write.
func addImage(t *testing.T, db *sqlx.DB, key string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO images (key, width, height, created_at) VALUES (?, 1, 1, CURRENT_TIMESTAMP)`, key)
	require.NoError(t, err)
}

func writeImage(t *testing.T, dir, key string) string {
	t.Helper()
	d := filepath.Join(dir, "images", key[:2])
	require.NoError(t, os.MkdirAll(d, 0o750))
	p := filepath.Join(d, key+".jpg")
	require.NoError(t, os.WriteFile(p, []byte(key), 0o600))
	return p
}

// The server replaces an image (write the new file, add the row, delete the
// old file) after the first image pass. The second pass picks up the new file.
func TestRunSecondPassCopiesNewImage(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	addImage(t, st.DB(), oldKey)
	oldPath := writeImage(t, dir, oldKey)
	target := filepath.Join(t.TempDir(), "snap")

	res, err := backup.RunWithHook(context.Background(), st.DB(), dir, target, func() {
		writeImage(t, dir, newKey)
		addImage(t, st.DB(), newKey)
		require.NoError(t, os.Remove(oldPath))
	})
	require.NoError(t, err)
	require.Empty(t, res.Missing)
	require.Equal(t, 2, res.Images)
	require.FileExists(t, filepath.Join(target, "images", newKey[:2], newKey+".jpg"))
	require.FileExists(t, filepath.Join(target, "images", oldKey[:2], oldKey+".jpg"))
}

// A referenced file that disappears before the first pass copied it can't be
// recovered. Run reports it and keeps the snapshot.
func TestRunReportsMissingImageAndKeepsSnapshot(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	addImage(t, st.DB(), oldKey) // no file on disk
	target := filepath.Join(t.TempDir(), "snap")

	res, err := backup.Run(context.Background(), st.DB(), dir, target)
	require.ErrorIs(t, err, backup.ErrMissingImages)
	require.Equal(t, []string{oldKey}, res.Missing)
	require.FileExists(t, filepath.Join(target, "wishbone.db"))
}

func TestRunMissingImageDeletedBetweenPasses(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	// The row points at newKey, whose file is created and deleted inside the
	// window, so no pass can copy it.
	target := filepath.Join(t.TempDir(), "snap")
	res, err := backup.RunWithHook(context.Background(), st.DB(), dir, target, func() {
		p := writeImage(t, dir, newKey)
		addImage(t, st.DB(), newKey)
		require.NoError(t, os.Remove(p))
	})
	require.ErrorIs(t, err, backup.ErrMissingImages)
	require.Equal(t, []string{newKey}, res.Missing)
	require.DirExists(t, target)
}

func TestRunDatabaseMode(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	target := filepath.Join(t.TempDir(), "snap")
	_, err := backup.Run(context.Background(), st.DB(), dir, target)
	require.NoError(t, err)
	fi, err := os.Stat(filepath.Join(target, "wishbone.db"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// The snapshot before the first migrations is of a new database with no
// tables. The image check reads it with the query for that schema.
func TestRunOnADatabaseThatIsNotMigrated(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	res, err := backup.Run(context.Background(), st.DB(), dir, filepath.Join(t.TempDir(), "snap"))
	require.NoError(t, err)
	require.FileExists(t, res.Database)
}

// A database keeps its image keys in images. The backup reports a row whose
// file is gone, held or not, and passes when every file is there.
func TestRunChecksEveryImageRow(t *testing.T) {
	dir := t.TempDir()
	st := newStore(t, dir)
	_, err := st.DB().Exec(`INSERT INTO users (id, name, birthday_month, birthday_day, created_at) VALUES (1, 'Ann', 1, 1, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = st.DB().Exec(`INSERT INTO images (id, key, width, height, uploaded_by, created_at) VALUES
		(1, ?, 1, 1, 1, CURRENT_TIMESTAMP), (2, ?, 1, 1, 1, CURRENT_TIMESTAMP)`, oldKey, newKey)
	require.NoError(t, err)
	_, err = st.DB().Exec(`UPDATE users SET avatar_image_id = 1`)
	require.NoError(t, err)
	writeImage(t, dir, oldKey)

	res, err := backup.Run(t.Context(), st.DB(), dir, filepath.Join(t.TempDir(), "snap"))
	require.ErrorIs(t, err, backup.ErrMissingImages, "the unattached image's file is missing")
	require.Equal(t, []string{newKey}, res.Missing)

	writeImage(t, dir, newKey)
	res, err = backup.Run(t.Context(), st.DB(), dir, filepath.Join(t.TempDir(), "snap"))
	require.NoError(t, err)
	require.Empty(t, res.Missing)
	require.Equal(t, 2, res.Images)
}
