package store

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newestMigration is the version of the newest migration file.
func newestMigration(t *testing.T) int64 {
	t.Helper()
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	require.NoError(t, err)
	slices.Sort(names)
	base := path.Base(names[len(names)-1])
	v, err := strconv.ParseInt(base[:strings.IndexByte(base, '_')], 10, 64)
	require.NoError(t, err)
	return v
}

func tableExists(t *testing.T, st *Store, name string) bool {
	t.Helper()
	var n int
	require.NoError(t, st.db.GetContext(t.Context(), &n,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name))
	return n == 1
}

func newDB(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrateAfterCallsBeforeOnTheOldSchema(t *testing.T) {
	st := newDB(t)
	var from, to int64
	var sawUsers bool
	err := st.MigrateAfter(t.Context(), func(_ context.Context, f, tv int64) error {
		from, to = f, tv
		sawUsers = tableExists(t, st, "users")
		return nil
	})
	require.NoError(t, err)
	require.Zero(t, from)
	require.Equal(t, newestMigration(t), to)
	require.False(t, sawUsers, "before must see the database as it was before the migrations")
	require.True(t, tableExists(t, st, "users"), "the migrations run after before")
}

func TestMigrateAfterAppliesNothingWhenBeforeFails(t *testing.T) {
	st := newDB(t)
	boom := errors.New("disk full")

	err := st.MigrateAfter(t.Context(), func(context.Context, int64, int64) error { return boom })

	require.ErrorIs(t, err, boom)
	require.False(t, tableExists(t, st, "users"))
	v, err := Version(t.Context(), st.db)
	require.NoError(t, err)
	require.Zero(t, v)
}

func TestMigrateAfterSkipsBeforeWhenNothingIsPending(t *testing.T) {
	st := newDB(t)
	require.NoError(t, st.Migrate(t.Context()))

	called := false
	require.NoError(t, st.MigrateAfter(t.Context(), func(context.Context, int64, int64) error {
		called = true
		return nil
	}))
	require.False(t, called)
}

func TestVersion(t *testing.T) {
	st := newDB(t)

	v, err := Version(t.Context(), st.db)
	require.NoError(t, err)
	require.Zero(t, v, "goose never touched this database")

	require.NoError(t, st.Migrate(t.Context()))
	v, err = Version(t.Context(), st.db)
	require.NoError(t, err)
	require.Equal(t, newestMigration(t), v)
}

func TestMigrateToAppliesNothingBelowTheBaseline(t *testing.T) {
	st := newDB(t)
	require.NoError(t, st.MigrateTo(t.Context(), BaselineVersion-1))
	require.False(t, tableExists(t, st, "users"))

	require.NoError(t, st.MigrateTo(t.Context(), BaselineVersion))
	require.True(t, tableExists(t, st, "users"))
}
