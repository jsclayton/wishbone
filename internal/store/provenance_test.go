package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The migration that makes what a lasting login link's sessions make record
// the link (docs/spec.md, Access, Lasting login links), and the version of the
// schema just before it.
const (
	provenanceVersion       int64 = 20261123091640
	beforeProvenanceVersion int64 = 20261122043018
)

// provenanceSeed adds lasting links to [seed], a session that one started,
// and a session with the highest ID that is then deleted, so the counter of
// sessions is above its highest row.
var provenanceSeed = []string{
	`INSERT INTO lasting_links (id, user_id, token_hash, created_at, last_used_at, revoked_at) VALUES
		(2, 2, 'l2', ` + at + `, ` + at + `, NULL), (5, 1, 'l5', ` + at + `, NULL, ` + at + `)`,
	`UPDATE sessions SET lasting_link_id = 2 WHERE id = 10`,
	`INSERT INTO sessions (id, user_id, token_hash, created_at, last_used_at, expires_at, cookie_sent_at) VALUES
		(20, 1, 'h20', ` + at + `, ` + at + `, ` + at + `, ` + at + `)`,
	`DELETE FROM sessions WHERE id = 20`,
}

// beforeProvenance takes a new database to the schema just before the
// migration, with rows in every table.
func beforeProvenance(t *testing.T, st *Store) {
	t.Helper()
	require.NoError(t, st.MigrateTo(t.Context(), beforeProvenanceVersion))
	execAll(t, st, seed...)
	execAll(t, st, provenanceSeed...)
}

// The migration keeps every row with its ID. Passkeys and login links from
// before it record no lasting link, and every lasting link from before it is
// marked as older than the record.
func TestTheProvenanceMigrationKeepsEveryRow(t *testing.T) {
	st := newDB(t)
	beforeProvenance(t, st)
	before := allRows(t, st)

	require.NoError(t, st.MigrateTo(t.Context(), provenanceVersion))

	after := allRows(t, st)
	for table, added := range map[string]struct{ column, value string }{
		"passkeys":      {"lasting_link_id", "NULL"},
		"login_links":   {"lasting_link_id", "NULL"},
		"lasting_links": {"predates_provenance", "1"},
	} {
		want := []string{before[table][0] + "," + added.column}
		for _, r := range before[table][1:] {
			want = append(want, r+", "+added.value)
		}
		require.Greater(t, len(want), 1, "%s has rows", table)
		require.Equal(t, want, after[table], table)
		delete(before, table)
		delete(after, table)
	}
	require.Equal(t, before, after, "every other table, sessions too, is as it was")
	requireSound(t, st)
	var temp int
	require.NoError(t, st.db.GetContext(t.Context(), &temp, `SELECT count(*) FROM sqlite_temp_master`))
	require.Zero(t, temp, "the migration leaves no temporary table")
}

// A delete of a lasting link clears it from what records it, and deletes
// nothing. Only sessions.lasting_link_id changes from what it was, and the
// rebuilt sessions table keeps its indexes, its trigger and its counter.
func TestTheProvenanceMigrationClearsALinkFromWhatRecordsItOnADelete(t *testing.T) {
	st := newDB(t)
	beforeProvenance(t, st)
	keys := foreignKeys(t, st)
	objects := schemaObjects(t, st)

	require.NoError(t, st.MigrateTo(t.Context(), provenanceVersion))

	var want []string
	for _, k := range keys {
		if k != "sessions.lasting_link_id -> lasting_links.id on delete CASCADE" {
			want = append(want, k)
		}
	}
	want = append(want,
		"login_links.lasting_link_id -> lasting_links.id on delete SET NULL",
		"passkeys.lasting_link_id -> lasting_links.id on delete SET NULL",
		"sessions.lasting_link_id -> lasting_links.id on delete SET NULL")
	require.ElementsMatch(t, want, foreignKeys(t, st))

	got := schemaObjects(t, st)
	for _, name := range []string{"index sessions_user_id", "index sessions_lasting_link_id", "trigger idempotency_keys_session_revoked"} {
		require.Equal(t, objects[name], got[name], name)
	}
	for _, name := range []string{"index passkeys_lasting_link_id", "index login_links_lasting_link_id"} {
		require.Contains(t, got, name)
	}

	require.Greater(t, insertID(t, st, `INSERT INTO sessions (user_id, token_hash, created_at, last_used_at, expires_at, cookie_sent_at) VALUES (1, 'new', `+at+`, `+at+`, `+at+`, `+at+`)`), int64(20), "the counter of sessions came over")
	execAll(t, st, `UPDATE sessions SET revoked_at = `+at+` WHERE id = 3`)
	var keysLeft []string
	require.NoError(t, st.db.SelectContext(t.Context(), &keysLeft, `SELECT key FROM idempotency_keys`))
	require.Equal(t, []string{"k2"}, keysLeft, "the trigger on sessions still acts")

	execAll(t, st,
		`UPDATE passkeys SET lasting_link_id = 2 WHERE id = 6`,
		`UPDATE login_links SET lasting_link_id = 2 WHERE id = 2`,
		`DELETE FROM lasting_links WHERE id = 2`)
	var left struct {
		Sessions   int `db:"sessions"`
		Passkeys   int `db:"passkeys"`
		LoginLinks int `db:"login_links"`
		Recording  int `db:"recording"`
	}
	require.NoError(t, st.db.GetContext(t.Context(), &left, `SELECT
		(SELECT count(*) FROM sessions WHERE id = 10) AS sessions,
		(SELECT count(*) FROM passkeys WHERE id = 6) AS passkeys,
		(SELECT count(*) FROM login_links WHERE id = 2) AS login_links,
		(SELECT count(*) FROM sessions WHERE lasting_link_id IS NOT NULL)
		 + (SELECT count(*) FROM passkeys WHERE lasting_link_id IS NOT NULL)
		 + (SELECT count(*) FROM login_links WHERE lasting_link_id IS NOT NULL) AS recording`))
	require.Equal(t, 1, left.Sessions, "the session stays")
	require.Equal(t, 1, left.Passkeys, "the passkey stays")
	require.Equal(t, 1, left.LoginLinks, "the login link stays")
	require.Zero(t, left.Recording, "and none of them records the link")
	requireSound(t, st)
}

// A migration that fails changes nothing, and leaves the connection with no
// open transaction and foreign keys on.
func TestAFailedProvenanceMigrationChangesNothing(t *testing.T) {
	st, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	beforeProvenance(t, st)
	// The migration can't make its own table of this name.
	execAll(t, st, `CREATE TABLE new_sessions (id INTEGER PRIMARY KEY)`)
	rows, schema := allRows(t, st), schemaDump(t, st)

	require.Error(t, st.MigrateTo(t.Context(), provenanceVersion))

	require.Equal(t, schema, schemaDump(t, st))
	require.Equal(t, rows, allRows(t, st))
	requireSound(t, st)
	v, err := Version(t.Context(), st.db)
	require.NoError(t, err)
	require.Equal(t, beforeProvenanceVersion, v)

	execAll(t, st, `DROP TABLE new_sessions`)
	require.NoError(t, st.MigrateTo(t.Context(), provenanceVersion))
	require.True(t, strings.HasSuffix(allRows(t, st)["passkeys"][0], ",lasting_link_id"))
	requireSound(t, st)
}
