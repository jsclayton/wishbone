package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// KeepAutomatic is how many automatic snapshots the server keeps.
const KeepAutomatic = 3

// ErrPrune means the automatic snapshot was written, but an older one could
// not be removed. The new snapshot is whole, so a caller can go on.
var ErrPrune = errors.New("remove old automatic snapshots")

// automaticTime is the UTC timestamp at the end of an automatic snapshot's
// name. It sorts in time order.
const automaticTime = "20060102T150405Z"

// automaticName matches the name of an automatic snapshot and nothing else,
// so pruning never touches a snapshot from wishbone backup. It captures the
// version before the migrations, the version after, and the time.
var automaticName = regexp.MustCompile(`^pre-migrate-(\d+)-to-(\d+)-(\d{8}T\d{6}Z)$`)

// Automatic writes the snapshot that serve takes before it applies
// migrations: dataDir/backups/pre-migrate-<from>-to-<to>-<UTC timestamp>/,
// with the same contents as [Run]. A snapshot that references a missing image
// counts as failed: Automatic removes it and returns ErrMissingImages. On any
// failure, no snapshot is left behind.
//
// After it writes the snapshot, Automatic removes the older automatic
// snapshots, so only the [KeepAutomatic] newest are left. If that fails, it
// returns an error that wraps ErrPrune, and the new snapshot is still whole.
//
// Automatic knows no baseline migration. [AutomaticWithBaseline] does.
func Automatic(ctx context.Context, db *sqlx.DB, dataDir string, from, to int64, now time.Time) (Result, error) {
	return AutomaticWithBaseline(ctx, db, dataDir, from, to, 0, now)
}

// AutomaticWithBaseline is like [Automatic], and knows the version of the
// baseline migration (docs/spec.md, Migration, Automatic backup). A snapshot is
// a pre-baseline snapshot when the database was older than the baseline and the
// migrations to apply include it: from is not 0, from is older than baseline,
// and to is baseline or newer. A new install (from 0) has nothing older, so its
// snapshot is like any other.
//
// The server never deletes a pre-baseline snapshot, and the 3 newest do not
// count it. When the new snapshot is a pre-baseline snapshot, the older ones go
// and only the newest stays. A failed snapshot prunes nothing.
//
// The version guard refuses a database below the baseline, so this binary never
// writes a pre-baseline snapshot. One that is in the data dir is still
// recognized by its name, so retention never deletes it.
func AutomaticWithBaseline(ctx context.Context, db *sqlx.DB, dataDir string, from, to, baseline int64, now time.Time) (Result, error) {
	dir := filepath.Join(dataDir, "backups")
	target := filepath.Join(dir, fmt.Sprintf("pre-migrate-%d-to-%d-%s", from, to, now.UTC().Format(automaticTime)))
	res, err := Run(ctx, db, dataDir, target)
	if errors.Is(err, ErrMissingImages) {
		_ = os.RemoveAll(target)
	}
	if err != nil {
		return res, err
	}
	if err := pruneAutomatic(dir, KeepAutomatic, baseline, isPreBaseline(from, to, baseline)); err != nil {
		return res, fmt.Errorf("%w: %w", ErrPrune, err)
	}
	return res, nil
}

// isPreBaseline reports whether a snapshot from the version from, before the
// migrations up to to, is a pre-baseline snapshot. A baseline of 0 means there
// is none.
func isPreBaseline(from, to, baseline int64) bool {
	return baseline > 0 && from > 0 && from < baseline && to >= baseline
}

// pruneAutomatic removes the automatic snapshots in dir that the server no
// longer keeps. Of the snapshots that are not pre-baseline snapshots, it keeps
// the newest keep, by the time in their names. It never removes a pre-baseline
// snapshot, unless newIsPreBaseline: then it keeps only the newest of them.
func pruneAutomatic(dir string, keep int, baseline int64, newIsPreBaseline bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type snap struct{ name, at string }
	var others, preBaselines []snap
	for _, e := range entries {
		m := automaticName.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		from, errFrom := strconv.ParseInt(m[1], 10, 64)
		to, errTo := strconv.ParseInt(m[2], 10, 64)
		s := snap{e.Name(), m[3]}
		if errFrom == nil && errTo == nil && isPreBaseline(from, to, baseline) {
			preBaselines = append(preBaselines, s)
		} else {
			others = append(others, s)
		}
	}
	// Newest first. The name breaks a tie, so the order is stable.
	newestFirst := func(a, b snap) int {
		if c := strings.Compare(b.at, a.at); c != 0 {
			return c
		}
		return strings.Compare(b.name, a.name)
	}
	slices.SortFunc(others, newestFirst)
	slices.SortFunc(preBaselines, newestFirst)

	remove := others[min(keep, len(others)):]
	if newIsPreBaseline {
		remove = append(remove, preBaselines[min(1, len(preBaselines)):]...)
	}
	var errs []error
	for _, s := range remove {
		if err := os.RemoveAll(filepath.Join(dir, s.name)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
