package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// baselineTables are the tables of the schema, in the order the baseline makes them.
var baselineTables = []string{
	"instance_meta", "extraction_cache", "users", "setup_codes", "groups", "group_users", "group_groups",
	"user_parents", "blocked_stores", "lists", "list_recipients", "store_links", "item_images",
	"list_invitations", "occasions", "occasion_groups", "gifts", "gift_flags", "gift_changes",
	"kept_list_recipients", "gift_recipients", "items", "sessions", "login_links", "passkeys", "images",
}

func gooseVersions(t *testing.T, st *Store) []int64 {
	t.Helper()
	var vs []int64
	require.NoError(t, st.db.SelectContext(t.Context(), &vs, `SELECT version_id FROM goose_db_version ORDER BY id`))
	return vs
}

func schemaTables(t *testing.T, st *Store) []string {
	t.Helper()
	var names []string
	require.NoError(t, st.db.SelectContext(t.Context(), &names,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT IN ('goose_db_version', 'sqlite_sequence') ORDER BY rowid`))
	return names
}

// A new install creates the schema from the baseline, in one migration.
func TestANewInstallCreatesTheSchemaFromTheBaseline(t *testing.T) {
	st := newDB(t)

	require.NoError(t, st.Migrate(t.Context()))

	require.Equal(t, baselineTables, schemaTables(t, st))
	require.Equal(t, []int64{0, BaselineVersion}, gooseVersions(t, st), "goose's own row, and the baseline")
	require.Equal(t, int64(20261110030443), BaselineVersion, "the version that existing databases already have")
	var objects int
	require.NoError(t, st.db.GetContext(t.Context(), &objects,
		`SELECT count(*) FROM sqlite_master WHERE type IN ('view', 'trigger')`))
	require.Zero(t, objects)
}

// olderRowsVersions are the goose versions that an existing database lists: a
// row for each migration that the baseline replaced, and the baseline's own
// version (20261110030443).
var olderRowsVersions = []int64{
	0, 1, 20261027160910, 20261027174740, 20261027180812, 20261027205730, 20261028001626,
	20261028003627, 20261028004951, 20261028011431, 20261028013231, 20261028014304, 20261028023341,
	20261028023342, 20261028030048, 20261028034024, 20261028035011, 20261028041748, 20261028041910,
	20261028043643, 20261030145844, 20261030145846, 20261031210728, 20261101212652, 20261102162112,
	20261103003804, 20261103023030, 20261104041033, 20261105204058, 20261110030443,
}

// A database that lists the rows of the migrations that the baseline replaced,
// and the baseline's version, is at the baseline. Goose ignores the versions
// that have no file, so nothing runs on it.
func TestADatabaseAtTheBaselineVersionRunsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	setGooseVersions(t, path, olderRowsVersions[1:]...)
	st, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	_, err = st.db.ExecContext(t.Context(), `INSERT INTO instance_meta (key, value) VALUES ('id', 'kept')`)
	require.NoError(t, err)
	before := schemaDump(t, st)

	called := false
	require.NoError(t, st.MigrateAfter(t.Context(), func(context.Context, int64, int64) error {
		called = true
		return nil
	}))

	require.False(t, called, "nothing is pending, so there is no snapshot to take")
	require.NoError(t, st.EnsureMigrated(t.Context()))
	require.Equal(t, olderRowsVersions, gooseVersions(t, st), "no version was added")
	require.Equal(t, before, schemaDump(t, st))
	var v string
	require.NoError(t, st.db.GetContext(t.Context(), &v, `SELECT value FROM instance_meta WHERE key = 'id'`))
	require.Equal(t, "kept", v)
}

func schemaDump(t *testing.T, st *Store) []string {
	t.Helper()
	var sqls []string
	require.NoError(t, st.db.SelectContext(t.Context(), &sqls,
		`SELECT type || ' ' || name || ' ' || COALESCE(sql, '') FROM sqlite_master ORDER BY rowid`))
	return sqls
}
