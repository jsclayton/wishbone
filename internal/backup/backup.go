// Package backup writes a consistent snapshot of the database and images.
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/imagerefs"
	"jsclayton.dev/wishbone/internal/store"
)

// ErrTargetExists means the target path is already taken. Run never
// overwrites or merges into an existing path.
var ErrTargetExists = errors.New("backup target already exists")

// ErrMissingImages means the snapshot database references image files that
// are in neither the data dir nor the snapshot. The snapshot is kept.
var ErrMissingImages = errors.New("snapshot references missing images")

// Result describes what Run wrote.
type Result struct {
	Database string   // path of the database snapshot
	Images   int      // number of image files copied
	ImageDir string   // path of the copied images directory
	Missing  []string // image keys the snapshot references but could not be found
}

// Run writes a snapshot into the new directory target: wishbone.db (SQLite
// VACUUM INTO) and images/ (a copy of dataDir/images). It is safe to call
// while the server is running. On failure it removes the partial target.
//
// The server deletes a replaced image right after it updates the database, so
// the copy runs in two passes around the snapshot. Pass one copies every image
// that exists. Pass two, after the snapshot, copies files created since. The
// server writes a file before the database points at it, so every key the
// snapshot references is then in the backup. If one is still missing, Run
// keeps the snapshot, lists the keys in Result.Missing, and returns
// ErrMissingImages.
func Run(ctx context.Context, db *sqlx.DB, dataDir, target string) (Result, error) {
	return run(ctx, db, dataDir, target, nil)
}

// run is Run with a hook that runs between the image pass and the snapshot.
func run(ctx context.Context, db *sqlx.DB, dataDir, target string, afterImages func()) (res Result, err error) {
	target = filepath.Clean(target)
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return res, fmt.Errorf("create parent of target: %w", err)
	}
	// Mkdir is atomic, so two runs can't both claim the same target.
	if err := os.Mkdir(target, 0o750); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return res, fmt.Errorf("%w: %s", ErrTargetExists, target)
		}
		return res, fmt.Errorf("create target: %w", err)
	}
	defer func() {
		if err != nil && !errors.Is(err, ErrMissingImages) {
			_ = os.RemoveAll(target)
		}
	}()

	srcImages := filepath.Join(dataDir, "images")
	res.ImageDir = filepath.Join(target, "images")
	if err = copyTree(ctx, srcImages, res.ImageDir); err != nil {
		return res, fmt.Errorf("copy images: %w", err)
	}
	if afterImages != nil {
		afterImages()
	}

	res.Database = filepath.Join(target, "wishbone.db")
	// VACUUM INTO takes no bound parameters, so quote the path as a literal.
	lit := "'" + strings.ReplaceAll(res.Database, "'", "''") + "'"
	if _, err = db.ExecContext(ctx, "VACUUM INTO "+lit); err != nil {
		return res, fmt.Errorf("snapshot database: %w", err)
	}

	if err = os.Chmod(res.Database, 0o600); err != nil {
		return res, fmt.Errorf("restrict database snapshot: %w", err)
	}

	if err = copyTree(ctx, srcImages, res.ImageDir); err != nil {
		return res, fmt.Errorf("copy images: %w", err)
	}
	res.Images = countFiles(res.ImageDir)

	// Check every image the snapshot references. The columns are listed in
	// internal/imagerefs. The path layout (key[:2]/key.jpg) is owned by internal/imagestore.
	// The "sqlite" driver is registered by the store package's import. Open
	// read-only so the snapshot gets no -wal or -shm files.
	snap, err := sqlx.Open("sqlite", "file:"+res.Database+"?mode=ro")
	if err != nil {
		return res, fmt.Errorf("open snapshot: %w", err)
	}
	defer snap.Close()
	// The snapshot can be of a database that isn't migrated yet, so read it
	// with the query for its own schema.
	version, err := store.Version(ctx, snap)
	if err != nil {
		return res, fmt.Errorf("read snapshot version: %w", err)
	}
	keys, err := imagerefs.KeysAt(ctx, snap, version)
	if err != nil {
		return res, fmt.Errorf("list referenced images: %w", err)
	}
	for _, k := range keys {
		if len(k) < 2 {
			res.Missing = append(res.Missing, k)
			continue
		}
		if _, statErr := os.Stat(filepath.Join(res.ImageDir, k[:2], k+".jpg")); statErr != nil {
			res.Missing = append(res.Missing, k)
		}
	}
	if len(res.Missing) > 0 {
		// Keep the snapshot: it is still the best copy available.
		return res, fmt.Errorf("%w: %s", ErrMissingImages, strings.Join(res.Missing, ", "))
	}
	return res, nil
}

func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

// copyTree copies regular files from src to dst that dst doesn't have yet.
// A missing src gives an empty dst. A file deleted during the walk is skipped.
func copyTree(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o750)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if _, err := os.Stat(out); err == nil {
			return nil // copied by an earlier pass
		}
		if err := copyFile(p, out); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				_ = os.Remove(out)
				return nil
			}
			return err
		}
		return nil
	})
}

func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
