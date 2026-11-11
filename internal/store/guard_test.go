package store

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// setGooseVersions makes a database at path with goose's table, and exactly
// these versions in it. The schema is the baseline's, so only the versions
// differ from a real database of that release.
func setGooseVersions(t *testing.T, path string, versions ...int64) {
	t.Helper()
	st, err := Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, st.Migrate(t.Context()))
	_, err = st.db.ExecContext(t.Context(), `DELETE FROM goose_db_version WHERE version_id > 0`)
	require.NoError(t, err)
	for _, v := range versions {
		_, err = st.db.ExecContext(t.Context(),
			`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, v)
		require.NoError(t, err)
	}
	require.NoError(t, st.Close())
}

func TestOpenRefusesADatabaseNewerThanTheBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	newer := newestMigration(t) + 1
	setGooseVersions(t, path, BaselineVersion, newer)

	st, err := Open(t.Context(), path)
	require.Nil(t, st)
	require.ErrorIs(t, err, ErrDatabaseNewer)
	require.ErrorContains(t, err, "run a newer image")
	require.ErrorContains(t, err, "restore a snapshot")
	var nd *DatabaseNewerError
	require.True(t, errors.As(err, &nd))
	require.Equal(t, newer, nd.Database)
	require.Equal(t, newestMigration(t), nd.Binary)
}

func TestOpenAcceptsADatabaseAtTheBinarysVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, st.Migrate(t.Context()))
	require.NoError(t, st.Close())

	st, err = Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, st.Close())
}

func TestOpenAcceptsAnEmptyDatabase(t *testing.T) {
	st, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
}

// A database that goose set up but that has applied nothing is a new install too.
func TestOpenAcceptsADatabaseWithOnlyGoosesTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	setGooseVersions(t, path)

	st, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
}

// A database that has migrations but not the baseline is older than this
// release supports. The guard refuses it before anything is written
// (docs/spec.md, Migration, Version guard).
func TestOpenRefusesADatabaseOlderThanTheBaselineAndSaysSo(t *testing.T) {
	for name, versions := range map[string][]int64{
		"the first migration":          {1},
		"a few migrations":             {1, 20261027160910, 20261104041033},
		"the last before the baseline": {1, 20261027160910, 20261104041033, 20261105204058},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test.db")
			setGooseVersions(t, path, versions...)

			st, err := Open(t.Context(), path)

			require.Nil(t, st)
			require.ErrorIs(t, err, ErrDatabaseTooOld)
			require.NotErrorIs(t, err, ErrDatabaseNewer)
			require.ErrorContains(t, err, "older than this release supports")
			require.NotContains(t, err.Error(), "image")
			var tooOld *DatabaseTooOldError
			require.True(t, errors.As(err, &tooOld))
			require.Equal(t, versions[len(versions)-1], tooOld.Database)
			require.ErrorContains(t, err, "20261110030443", "the message names the baseline")
		})
	}
}
