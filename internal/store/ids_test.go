package store

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The migration that makes an ID never come back (docs/spec.md, API principles,
// Values), and the version of the schema just before it.
const (
	idsVersion       int64 = 20261121013109
	beforeIDsVersion int64 = 20261120145721
)

// idTables are the tables whose rows have an ID, in the order the baseline
// makes them. insert adds a row with no ID of its own to a database that has
// [seed] in it. It refers only to rows that the test doesn't delete.
var idTables = []struct{ table, insert string }{
	{"users", `INSERT INTO users (name, birthday_month, birthday_day, created_at) VALUES ('New', 1, 1, ` + at + `)`},
	{"setup_codes", `INSERT INTO setup_codes (code_hash, created_at) VALUES ('new', ` + at + `)`},
	{"groups", `INSERT INTO groups (name, created_at) VALUES ('New', ` + at + `)`},
	{"blocked_stores", `INSERT INTO blocked_stores (store, created_at) VALUES ('new.example', ` + at + `)`},
	{"lists", `INSERT INTO lists (name, created_at) VALUES ('New', ` + at + `)`},
	{"store_links", `INSERT INTO store_links (item_id, url, store, created_at) VALUES (1, 'https://new.example/x', 'new.example', ` + at + `)`},
	{"list_invitations", `INSERT INTO list_invitations (list_id, user_id, invited_by, created_at) VALUES (1, 2, 1, ` + at + `)`},
	{"occasions", `INSERT INTO occasions (kind, name, rule_kind, rule_month, rule_day, created_at) VALUES ('shared', 'New', 'annual', 1, 1, ` + at + `)`},
	{"gifts", `INSERT INTO gifts (item_id, giver_id, quantity, occasion_id, occasion_date, created_at) VALUES (1, 2, 1, 2, '2026-12-25', ` + at + `)`},
	{"gift_flags", `INSERT INTO gift_flags (gift_id, code, created_at) VALUES (1, 'new', ` + at + `)`},
	{"gift_changes", `INSERT INTO gift_changes (gift_id, field, old_value, new_value) VALUES (1, 'new', 'a', 'b')`},
	{"items", `INSERT INTO items (list_id, title, position, created_at) VALUES (1, 'New', 9, ` + at + `)`},
	{"sessions", `INSERT INTO sessions (user_id, token_hash, created_at, last_used_at, expires_at, cookie_sent_at) VALUES (1, 'new', ` + at + `, ` + at + `, ` + at + `, ` + at + `)`},
	{"login_links", `INSERT INTO login_links (user_id, token_hash, created_at, expires_at) VALUES (1, 'new', ` + at + `, ` + at + `)`},
	{"passkeys", `INSERT INTO passkeys (user_id, credential_id, public_key, name, created_at) VALUES (1, x'aa', x'bb', 'New', ` + at + `)`},
	{"images", `INSERT INTO images (key, width, height, created_at) VALUES ('new', 1, 1, ` + at + `)`},
}

// laterIDTables are the tables with an ID that a migration after the IDs
// migration adds. The tests of the IDs migration don't know them, because they
// aren't in its schema. insert adds a row to a database that has [seed] in it.
var laterIDTables = []struct{ table, insert string }{
	{"lasting_links", `INSERT INTO lasting_links (user_id, token_hash, created_at) VALUES (1, 'new' || random(), ` + at + `)`},
}

const at = "'2026-11-09 12:00:00'"

// seed puts rows in every table of the schema, with gaps in the IDs. A test
// can delete the row with the highest ID from each table of idTables, in that
// order: a row that points at it goes with it, and the table still has a row
// when its turn comes.
var seed = []string{
	`INSERT INTO instance_meta (key, value, updated_at) VALUES ('id', 'abc', ` + at + `)`,
	`INSERT INTO extraction_cache (url, result, created_at) VALUES ('https://a.example/x', '{}', ` + at + `)`,
	`INSERT INTO users (id, name, last_name, birthday_month, birthday_day, birthday_year, is_admin, profile_confirmed_at, created_at, good_to_know, timezone, webauthn_handle) VALUES
		(1, 'Ada', 'Lovelace', 12, 10, 1815, 1, ` + at + `, ` + at + `, 'Tea', 'Europe/London', x'0a'),
		(2, 'Grace', NULL, 12, 9, NULL, 0, NULL, ` + at + `, NULL, NULL, NULL),
		(7, 'Linus', NULL, 12, 28, NULL, 0, NULL, ` + at + `, NULL, NULL, NULL)`,
	`UPDATE users SET deactivated_at = ` + at + ` WHERE id = 7`,
	`INSERT INTO images (id, key, width, height, uploaded_by, created_at, detached_at) VALUES
		(3, 'k3', 10, 20, 1, ` + at + `, NULL), (4, 'k4', 10, 20, 2, ` + at + `, NULL),
		(5, 'k5', 10, 20, NULL, ` + at + `, NULL), (9, 'k9', 10, 20, 2, ` + at + `, ` + at + `)`,
	`UPDATE users SET avatar_image_id = 3 WHERE id = 1`,
	`INSERT INTO setup_codes (id, code_hash, created_at) VALUES (2, 'h2', ` + at + `), (5, 'h5', ` + at + `)`,
	`INSERT INTO groups (id, name, created_at) VALUES (1, 'Family', ` + at + `), (4, 'Cousins', ` + at + `)`,
	`INSERT INTO group_users (group_id, user_id) VALUES (1, 1), (1, 2), (4, 7)`,
	`INSERT INTO group_groups (group_id, child_id) VALUES (1, 4)`,
	`INSERT INTO user_parents (child_id, parent_id) VALUES (7, 1)`,
	`INSERT INTO blocked_stores (id, store, created_at) VALUES (3, 'bad.example', ` + at + `)`,
	`INSERT INTO lists (id, name, description, primary_user_id, name_follows_user, created_at, deleted_at) VALUES
		(1, 'Ada', 'Mine', 1, 1, ` + at + `, NULL), (2, 'Grace', NULL, 2, 0, ` + at + `, NULL),
		(6, 'Camping', NULL, NULL, 0, ` + at + `, ` + at + `)`,
	`INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (1, 1, ` + at + `), (2, 2, ` + at + `), (6, 1, ` + at + `)`,
	`INSERT INTO items (id, list_id, title, notes, price_minor, top_price_minor, currency, quantity, is_unlimited, position, added_by, created_at, price_cleared, deleted_at) VALUES
		(1, 1, 'Kettle', 'Red', 1000, 2000, 'USD', 2, 0, 0, 1, ` + at + `, 0, NULL),
		(2, 1, 'Book', NULL, NULL, NULL, NULL, 1, 1, 1, 2, ` + at + `, 1, NULL),
		(8, 2, 'Bike', NULL, NULL, NULL, NULL, 0, 0, 0, NULL, ` + at + `, 0, ` + at + `)`,
	`INSERT INTO store_links (id, item_id, url, store, price_minor, currency, captured_at, image_url, created_at) VALUES
		(1, 1, 'https://a.example/kettle', 'a.example', 500, 'USD', ` + at + `, 'https://a.example/k.jpg', ` + at + `),
		(5, 8, 'https://b.example/bike', 'b.example', NULL, NULL, NULL, NULL, ` + at + `)`,
	`INSERT INTO item_images (item_id, image_id, position, is_primary) VALUES (1, 4, 0, 1)`,
	`INSERT INTO list_invitations (id, list_id, user_id, invited_by, created_at) VALUES (2, 2, 1, NULL, ` + at + `), (3, 6, 2, 1, ` + at + `)`,
	`INSERT INTO occasions (id, kind, name, user_id, rule_kind, rule_year, rule_month, rule_day, rule_weekday, rule_nth, created_at, is_everyone) VALUES
		(1, 'birthday', 'Birthday', 1, NULL, NULL, NULL, NULL, NULL, NULL, ` + at + `, 0),
		(2, 'shared', 'Christmas', NULL, 'annual', NULL, 12, 25, NULL, NULL, ` + at + `, 1),
		(3, 'shared', 'Thanksgiving', NULL, 'nth_weekday', NULL, 11, NULL, 4, 4, ` + at + `, 0),
		(6, 'custom', 'Graduation', 2, 'once', 2027, 6, 5, NULL, NULL, ` + at + `, 0)`,
	`INSERT INTO occasion_groups (occasion_id, group_id) VALUES (3, 1)`,
	`INSERT INTO gifts (id, item_id, giver_id, quantity, occasion_id, occasion_date, from_label, note, is_bought, created_at, given_at, unshared, title, price_minor, top_price_minor, currency, image_id, occasion_name, giver_saw_list, giver_saw_occasion, giver_name) VALUES
		(1, 8, 1, 1, 2, '2026-12-25', 'Us', 'Wrap it', 1, ` + at + `, NULL, 0, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL),
		(4, 8, NULL, 2, NULL, '2026-06-05', NULL, NULL, 0, ` + at + `, ` + at + `, 1, 'Bike', 100, 200, 'USD', 5, 'Graduation', 1, 0, 'Linus')`,
	`INSERT INTO gift_recipients (gift_id, user_id, position) VALUES (4, 2, 0)`,
	`INSERT INTO gift_flags (id, gift_id, code, data, created_at) VALUES (2, 1, 'changed', '{}', ` + at + `), (3, 4, 'moved', NULL, ` + at + `)`,
	`INSERT INTO gift_changes (id, gift_id, field, label, old_value, new_value) VALUES (5, 1, 'title', '', 'Bik', 'Bike'), (6, 4, 'link', 'a.example', 'x', 'y')`,
	`INSERT INTO kept_list_recipients (list_id, user_id) VALUES (2, 7)`,
	`INSERT INTO sessions (id, user_id, token_hash, user_agent, created_at, last_used_at, expires_at, revoked_at, cookie_sent_at) VALUES
		(3, 1, 'h3', 'Safari', ` + at + `, ` + at + `, ` + at + `, NULL, ` + at + `),
		(10, 2, 'h10', '', ` + at + `, ` + at + `, ` + at + `, NULL, ` + at + `)`,
	`INSERT INTO idempotency_keys (session_id, key, request_hash, created_at, status, headers, body) VALUES
		(3, 'k1', 'r1', ` + at + `, 201, '{}', x'7b7d'), (10, 'k2', 'r2', ` + at + `, 204, '{}', x'')`,
	`INSERT INTO login_links (id, user_id, token_hash, created_at, expires_at, redeemed_at) VALUES
		(2, 2, 't2', ` + at + `, ` + at + `, ` + at + `), (4, 7, 't4', ` + at + `, ` + at + `, NULL)`,
	`INSERT INTO passkeys (id, user_id, credential_id, public_key, attestation_type, aaguid, sign_count, transports, backup_eligible, backup_state, name, created_at, last_used_at) VALUES
		(4, 1, x'01', x'02', 'none', x'03', 7, '["internal"]', 1, 1, 'Phone', ` + at + `, ` + at + `),
		(6, 2, x'04', x'05', '', x'', 0, '[]', 0, 0, 'Key', ` + at + `, NULL)`,
}

// beforeIDs opens a new database at the schema just before the migration.
func beforeIDs(t *testing.T) *Store {
	t.Helper()
	st := newDB(t)
	require.NoError(t, st.MigrateTo(t.Context(), beforeIDsVersion))
	return st
}

// insertID runs an INSERT and returns the ID that the new row got.
func insertID(t *testing.T, st *Store, query string) int64 {
	t.Helper()
	res, err := st.db.ExecContext(t.Context(), query)
	require.NoError(t, err, query)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func maxID(t *testing.T, st *Store, table string) (id int64) {
	t.Helper()
	require.NoError(t, st.db.GetContext(t.Context(), &id, `SELECT max(id) FROM `+table))
	return id
}

// sequences is sqlite_sequence: the highest ID that each table ever had.
func sequences(t *testing.T, st *Store) map[string]int64 {
	t.Helper()
	var rows []struct {
		Name string `db:"name"`
		Seq  int64  `db:"seq"`
	}
	require.NoError(t, st.db.SelectContext(t.Context(), &rows, `SELECT name, seq FROM sqlite_sequence`))
	seqs := map[string]int64{}
	for _, r := range rows {
		seqs[r.Name] = r.Seq
	}
	return seqs
}

// allRows is every row of every table but goose's and SQLite's own, as text.
// The first entry of a table is the names of its columns, in their order. A
// row is each of its values as SQLite's quote() writes it, so a NULL, a number,
// a text and a blob stay apart.
func allRows(t *testing.T, st *Store) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range schemaTables(t, st) {
		var cols []string
		require.NoError(t, st.db.SelectContext(t.Context(), &cols, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table))
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `quote("` + c + `")`
		}
		var rows []string
		require.NoError(t, st.db.SelectContext(t.Context(), &rows,
			`SELECT `+strings.Join(quoted, ` || ', ' || `)+` FROM `+table+` ORDER BY 1`))
		out[table] = append([]string{strings.Join(cols, ",")}, rows...)
	}
	return out
}

var (
	quotedTableName = regexp.MustCompile(`^CREATE TABLE "(\w+)"`)
	autoincrementID = regexp.MustCompile(`\(\s*id\s+INTEGER PRIMARY KEY AUTOINCREMENT,`)
)

// schemaObjects is every table, index and trigger of the schema, by name, but
// goose's table and SQLite's own. It leaves out what the migration changes on
// purpose: AUTOINCREMENT, and the quotes that SQLite puts around the name of a
// table it renamed.
func schemaObjects(t *testing.T, st *Store) map[string]string {
	t.Helper()
	var rows []struct {
		Type  string `db:"type"`
		Name  string `db:"name"`
		Table string `db:"tbl_name"`
		SQL   string `db:"sql"`
	}
	require.NoError(t, st.db.SelectContext(t.Context(), &rows,
		`SELECT type, name, tbl_name, COALESCE(sql, '') AS sql FROM sqlite_master
		 WHERE tbl_name NOT IN ('goose_db_version', 'sqlite_sequence')`))
	out := map[string]string{}
	for _, r := range rows {
		sql := quotedTableName.ReplaceAllString(r.SQL, "CREATE TABLE $1")
		sql = strings.ReplaceAll(sql, "INTEGER PRIMARY KEY AUTOINCREMENT", "INTEGER PRIMARY KEY")
		out[r.Type+" "+r.Name] = "on " + r.Table + ": " + sql
	}
	return out
}

// foreignKeys is every foreign key of the schema as SQLite reads it: the
// table and column it is on, the table and column it points at, and what a
// delete does.
func foreignKeys(t *testing.T, st *Store) []string {
	t.Helper()
	var keys []string
	require.NoError(t, st.db.SelectContext(t.Context(), &keys,
		`SELECT m.name || '.' || fk."from" || ' -> ' || fk."table" || '.' || fk."to" || ' on delete ' || fk.on_delete
		 FROM sqlite_master m, pragma_foreign_key_list(m.name) fk WHERE m.type = 'table' ORDER BY 1`))
	return keys
}

// brokenReferences is what PRAGMA foreign_key_check finds: the table and the
// row that point at a row that isn't there, and the table they point at.
func brokenReferences(t *testing.T, st *Store) []string {
	t.Helper()
	var out []string
	require.NoError(t, st.db.SelectContext(t.Context(), &out,
		`SELECT "table" || ' ' || COALESCE("rowid", 'no rowid') || ' -> ' || "parent" || ' (' || fkid || ')'
		 FROM pragma_foreign_key_check ORDER BY 1`))
	return out
}

func requireSound(t *testing.T, st *Store) {
	t.Helper()
	require.Empty(t, brokenReferences(t, st), "PRAGMA foreign_key_check")
	var integrity []string
	require.NoError(t, st.db.SelectContext(t.Context(), &integrity, `PRAGMA integrity_check`))
	require.Equal(t, []string{"ok"}, integrity)
	var fk int
	require.NoError(t, st.db.GetContext(t.Context(), &fk, `PRAGMA foreign_keys`))
	require.Equal(t, 1, fk, "foreign keys are on again")
}

// idTables and laterIDTables are every table with an ID. A table that a later
// migration adds with an ID must never give one again either.
func TestEveryTableWithAnIDNeverGivesItAgain(t *testing.T) {
	st := migrated(t)

	var withID []string
	require.NoError(t, st.db.SelectContext(t.Context(), &withID,
		`SELECT m.name FROM sqlite_master m, pragma_table_info(m.name) c
		 WHERE m.type = 'table' AND m.name <> 'goose_db_version' AND c.name = 'id' ORDER BY m.name`))
	var listed []string
	for _, tb := range slices.Concat(idTables, laterIDTables) {
		listed = append(listed, tb.table)
	}
	slices.Sort(listed)
	require.Equal(t, listed, withID, "add a new table with an ID to laterIDTables")

	execAll(t, st, seed...)
	for _, tb := range laterIDTables {
		first := insertID(t, st, tb.insert)
		execAll(t, st, fmt.Sprintf(`DELETE FROM %s WHERE id = %d`, tb.table, first))
		require.Greater(t, insertID(t, st, tb.insert), first, "%s: the ID of a deleted row", tb.table)
	}

	for _, table := range withID {
		var sql string
		require.NoError(t, st.db.GetContext(t.Context(), &sql, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table))
		require.Regexp(t, autoincrementID, sql,
			"%s: a table with an ID declares it INTEGER PRIMARY KEY AUTOINCREMENT, so that SQLite never gives the ID of a deleted row again", table)
	}
}

// Before the migration, the ID of a deleted row came back when it was the
// highest in its table. This is the fault that the migration ends.
func TestBeforeTheMigrationADeletedIDCameBack(t *testing.T) {
	st := beforeIDs(t)
	execAll(t, st, seed...)

	for _, tb := range idTables {
		first := insertID(t, st, tb.insert)
		execAll(t, st, fmt.Sprintf(`DELETE FROM %s WHERE id = %d`, tb.table, first))
		require.Equal(t, first, insertID(t, st, tb.insert), tb.table)
	}
}

// An ID is never used again (docs/spec.md, API principles, Values). In
// each table with an ID, the row after a deleted row gets a higher ID: after
// the delete of a row that was there before the migration, and after the
// delete of a row that was added since.
func TestAnIDNeverComesBack(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *Store{
		"in a database from before the migration": func(t *testing.T) *Store {
			st := beforeIDs(t)
			execAll(t, st, seed...)
			require.NoError(t, st.Migrate(t.Context()))
			return st
		},
		"in a new install": func(t *testing.T) *Store {
			st := migrated(t)
			execAll(t, st, seed...)
			return st
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := open(t)
			for _, tb := range idTables {
				highest := maxID(t, st, tb.table)
				execAll(t, st, fmt.Sprintf(`DELETE FROM %s WHERE id = %d`, tb.table, highest))
				first := insertID(t, st, tb.insert)
				require.Greater(t, first, highest, "%s: the ID of a deleted row from before", tb.table)

				execAll(t, st, fmt.Sprintf(`DELETE FROM %s WHERE id = %d`, tb.table, first))
				second := insertID(t, st, tb.insert)
				require.Greater(t, second, first, "%s: the ID of a deleted new row", tb.table)
			}
			requireSound(t, st)
		})
	}
}

// The migration keeps every row with its ID, and every index, trigger and
// foreign key. Only AUTOINCREMENT is new.
func TestTheIDsMigrationKeepsEveryRowAndTheSchema(t *testing.T) {
	st := beforeIDs(t)
	execAll(t, st, seed...)
	rows, objects, keys := allRows(t, st), schemaObjects(t, st), foreignKeys(t, st)
	require.Len(t, rows, len(baselineTables)+1, "every table of the schema has rows to keep")
	for table, r := range rows {
		require.Greater(t, len(r), 1, "%s has no row in the seed", table)
	}

	require.NoError(t, st.MigrateTo(t.Context(), idsVersion))

	require.Equal(t, rows, allRows(t, st))
	require.Equal(t, objects, schemaObjects(t, st))
	require.Equal(t, keys, foreignKeys(t, st))
	requireSound(t, st)
	var temp int
	require.NoError(t, st.db.GetContext(t.Context(), &temp, `SELECT count(*) FROM sqlite_temp_master`))
	require.Zero(t, temp, "the migration leaves no temporary table")

	// The foreign keys that point at a rebuilt table still act, and so does the
	// trigger on sessions.
	execAll(t, st, `UPDATE sessions SET revoked_at = `+at+` WHERE id = 3`)
	var keysLeft []string
	require.NoError(t, st.db.SelectContext(t.Context(), &keysLeft, `SELECT key FROM idempotency_keys`))
	require.Equal(t, []string{"k2"}, keysLeft, "a revoked session's keys go with it")
	execAll(t, st, `DELETE FROM users WHERE id = 2`)
	var left struct {
		Sessions int `db:"sessions"`
		Keys     int `db:"keys"`
		Lists    int `db:"lists"`
		Passkeys int `db:"passkeys"`
	}
	require.NoError(t, st.db.GetContext(t.Context(), &left, `SELECT
		(SELECT count(*) FROM sessions WHERE user_id = 2) AS sessions, (SELECT count(*) FROM idempotency_keys) AS keys,
		(SELECT count(*) FROM lists WHERE id = 2) AS lists, (SELECT count(*) FROM passkeys WHERE user_id = 2) AS passkeys`))
	require.Zero(t, left, "what belongs to a deleted user goes with them")
	_, err := st.db.ExecContext(t.Context(), `INSERT INTO items (list_id, title, position, created_at) VALUES (99, 'Lost', 0, `+at+`)`)
	require.Error(t, err, "a row still needs the row it points at")
	requireSound(t, st)
}

// After the migration, each table's counter is at its highest ID, so the next
// row gets a higher one. A table with no rows starts at 1.
func TestTheIDsMigrationStartsEachTableAtItsHighestID(t *testing.T) {
	st := beforeIDs(t)
	execAll(t, st, seed...)
	execAll(t, st, `DELETE FROM blocked_stores`)
	want := map[string]int64{}
	for _, tb := range idTables {
		if tb.table != "blocked_stores" {
			want[tb.table] = maxID(t, st, tb.table)
		}
	}
	want["blocked_stores"] = 0

	require.NoError(t, st.MigrateTo(t.Context(), idsVersion))

	seqs := sequences(t, st)
	delete(seqs, "goose_db_version")
	require.Equal(t, want, seqs)
	require.Equal(t, int64(1), insertID(t, st, `INSERT INTO blocked_stores (store, created_at) VALUES ('new.example', `+at+`)`))
}

// A migration that fails changes nothing, and leaves the connection as it
// was: no open transaction, and foreign keys on. The migration turns foreign
// keys off and opens its own transaction, so goose can't undo it.
func TestAFailedIDsMigrationChangesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	require.NoError(t, st.MigrateTo(t.Context(), beforeIDsVersion))
	execAll(t, st, seed...)
	// The migration can't make its own table of this name, so it fails after its
	// first rebuilds.
	execAll(t, st, `CREATE TABLE new_sessions (id INTEGER PRIMARY KEY)`)
	rows, schema := allRows(t, st), schemaDump(t, st)

	require.Error(t, st.MigrateTo(t.Context(), idsVersion))

	require.Equal(t, schema, schemaDump(t, st))
	require.Equal(t, rows, allRows(t, st))
	requireSound(t, st)
	v, err := Version(t.Context(), st.db)
	require.NoError(t, err)
	require.Equal(t, beforeIDsVersion, v)
	// A write after the failure is kept: it is in no transaction that a close
	// would undo.
	execAll(t, st, `INSERT INTO instance_meta (key, value) VALUES ('after', 'kept')`)
	other, err := Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { other.Close() })
	var kept string
	require.NoError(t, other.db.GetContext(t.Context(), &kept, `SELECT value FROM instance_meta WHERE key = 'after'`))
	require.Equal(t, "kept", kept)
	execAll(t, st, `DELETE FROM instance_meta WHERE key = 'after'`)

	// With the cause gone, the same database migrates.
	execAll(t, st, `DROP TABLE new_sessions`)
	delete(rows, "new_sessions")
	require.NoError(t, st.MigrateTo(t.Context(), idsVersion))
	require.Equal(t, rows, allRows(t, st))
	requireSound(t, st)
}

// The app keeps foreign keys on, so a database has no broken reference. If
// one has, from a write that some other tool made, the migration leaves it as it
// is and doesn't stop on it: the rebuild didn't break it.
func TestTheIDsMigrationLeavesAReferenceThatWasAlreadyBroken(t *testing.T) {
	st := beforeIDs(t)
	execAll(t, st, seed...)
	execAll(t, st,
		`PRAGMA foreign_keys = OFF`,
		`INSERT INTO sessions (id, user_id, token_hash, created_at, last_used_at, expires_at, cookie_sent_at) VALUES (12, 99, 'h12', `+at+`, `+at+`, `+at+`, `+at+`)`,
		`INSERT INTO group_users (group_id, user_id) VALUES (1, 98)`,
		`PRAGMA foreign_keys = ON`,
	)
	broken := brokenReferences(t, st)
	require.Equal(t, []string{"group_users 4 -> users (0)", "sessions 12 -> users (0)"}, broken)
	rows := allRows(t, st)

	require.NoError(t, st.MigrateTo(t.Context(), idsVersion))

	require.Equal(t, broken, brokenReferences(t, st))
	require.Equal(t, rows, allRows(t, st))
}

// Goose records a migration that runs with NO TRANSACTION after the
// migration's own commit. If the server stops between the two, the next start
// runs the migration again, on a database that already has it.
func TestTheIDsMigrationRunsAgainOnADatabaseThatAlreadyHasIt(t *testing.T) {
	st := beforeIDs(t)
	execAll(t, st, seed...)
	require.NoError(t, st.MigrateTo(t.Context(), idsVersion))
	rows, schema, seqs := allRows(t, st), schemaObjects(t, st), sequences(t, st)
	execAll(t, st, fmt.Sprintf(`DELETE FROM goose_db_version WHERE version_id = %d`, idsVersion))

	require.NoError(t, st.MigrateTo(t.Context(), idsVersion))

	require.Equal(t, rows, allRows(t, st))
	require.Equal(t, schema, schemaObjects(t, st))
	got := sequences(t, st)
	delete(got, "goose_db_version")
	delete(seqs, "goose_db_version")
	require.Equal(t, seqs, got)
	requireSound(t, st)
}

// carryCounter is the step that a rebuild of a table with an ID needs, after
// the copy and before the drop (docs/development.md, Migrations). %[1]s is the
// table. The copy sets the new table's counter to the highest ID that is in the
// table, and DROP TABLE deletes the old counter with the old table. This gives
// the new table the old counter when that is higher.
const carryCounter = `UPDATE sqlite_sequence
SET seq = (SELECT max(seq) FROM sqlite_sequence WHERE name IN ('%[1]s', 'new_%[1]s'))
WHERE name = 'new_%[1]s'`

var createTableName = regexp.MustCompile(`^CREATE TABLE "?\w+"?`)

// rebuild rebuilds a table as a later migration would, by the steps of the IDs
// migration: foreign keys off, a transaction of its own, the new table, the
// copy, the drop, the rename, and the table's indexes and triggers again. The
// new table has the definition of the old one. With carry, it runs
// [carryCounter] between the copy and the drop.
func rebuild(t *testing.T, st *Store, table string, carry bool) {
	t.Helper()
	var create string
	require.NoError(t, st.db.GetContext(t.Context(), &create, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table))
	var again []string
	require.NoError(t, st.db.SelectContext(t.Context(), &again,
		`SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY rowid`, table))

	steps := []string{
		`PRAGMA foreign_keys = OFF`,
		`BEGIN IMMEDIATE`,
		createTableName.ReplaceAllString(create, "CREATE TABLE new_"+table),
		`INSERT INTO new_` + table + ` SELECT * FROM ` + table,
	}
	if carry {
		steps = append(steps, fmt.Sprintf(carryCounter, table))
	}
	steps = append(steps, `DROP TABLE `+table, `ALTER TABLE new_`+table+` RENAME TO `+table)
	steps = append(steps, again...)
	steps = append(steps, `COMMIT`, `PRAGMA foreign_keys = ON`)
	execAll(t, st, steps...)
}

// A later migration that rebuilds a table with an ID must carry the table's
// counter over, or the ID of a deleted row comes back. In each table, the row
// with the highest ID is deleted, the table is rebuilt with the step that
// docs/development.md gives, and the next row still gets a higher ID. The
// deletes leave some tables with no row (blocked_stores is one), so a table
// that is empty at its rebuild keeps its counter too.
func TestARebuildThatCarriesTheCounterOverGivesNoIDAgain(t *testing.T) {
	st := migrated(t)
	execAll(t, st, seed...)
	schema, keys := schemaObjects(t, st), foreignKeys(t, st)
	// The highest ID that each table ever had, read before the deletes: a
	// delete takes rows of the tables after it too.
	highest := sequences(t, st)
	var empty []string

	for _, tb := range idTables {
		// An earlier delete can have taken the row with the highest ID already.
		execAll(t, st, fmt.Sprintf(`DELETE FROM %[1]s WHERE id = (SELECT max(id) FROM %[1]s)`, tb.table))
		var left int64
		require.NoError(t, st.db.GetContext(t.Context(), &left, `SELECT COALESCE(max(id), 0) FROM `+tb.table))
		require.Less(t, left, highest[tb.table], "%s: the row with the highest ID is gone", tb.table)
		rows := allRows(t, st)
		if len(rows[tb.table]) == 1 {
			empty = append(empty, tb.table)
		}

		rebuild(t, st, tb.table, true)

		require.Equal(t, rows, allRows(t, st), tb.table)
		require.Equal(t, highest[tb.table], sequences(t, st)[tb.table], "%s: the counter came over", tb.table)
		require.Greater(t, insertID(t, st, tb.insert), highest[tb.table], tb.table)
	}

	require.Contains(t, empty, "blocked_stores", "some tables had no row at their rebuild")
	require.NotContains(t, empty, "items", "and some had rows")
	require.Equal(t, schema, schemaObjects(t, st), "the rebuild in this test keeps the schema, as a migration's must")
	require.Equal(t, keys, foreignKeys(t, st))
	requireSound(t, st)
}

// Without that step, a rebuild loses the counter: the next row gets the ID of
// the deleted one. This is why the step is in the rule for a rebuild.
func TestARebuildThatLeavesTheCounterGivesADeletedIDAgain(t *testing.T) {
	st := migrated(t)
	execAll(t, st, seed...)
	highest := maxID(t, st, "items")
	execAll(t, st, fmt.Sprintf(`DELETE FROM items WHERE id = %d`, highest))
	left := maxID(t, st, "items")

	rebuild(t, st, "items", false)

	require.Equal(t, left+1, insertID(t, st, `INSERT INTO items (list_id, title, position, created_at) VALUES (1, 'New', 9, `+at+`)`))
	require.Less(t, left+1, highest, "an ID below the deleted one, which a row once had")
}
