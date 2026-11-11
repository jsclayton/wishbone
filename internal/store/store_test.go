package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAndMigrate(t *testing.T) {
	ctx := t.Context()
	st, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	require.NoError(t, st.Migrate(ctx))
	// Migrating twice is a no-op.
	require.NoError(t, st.Migrate(ctx))

	var fk int
	require.NoError(t, st.db.GetContext(ctx, &fk, "PRAGMA foreign_keys"))
	require.Equal(t, 1, fk, "foreign keys must be enabled")

	var mode string
	require.NoError(t, st.db.GetContext(ctx, &mode, "PRAGMA journal_mode"))
	require.Equal(t, "wal", mode)

	var n int
	require.NoError(t, st.db.GetContext(ctx, &n, "SELECT count(*) FROM instance_meta"))
	require.Zero(t, n)
}

// A read-then-write transaction must wait out busy_timeout when another
// connection holds the write lock, not fail at once with SQLITE_BUSY.
func TestTransactionsWaitForOtherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wishbone.db")
	a, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer a.Close()
	b, err := Open(t.Context(), path)
	require.NoError(t, err)
	defer b.Close()
	_, err = a.DB().Exec(`CREATE TABLE t (n INTEGER)`)
	require.NoError(t, err)

	// b holds the write lock briefly, then commits.
	tx, err := b.DB().Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO t (n) VALUES (1)`)
	require.NoError(t, err)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = tx.Commit()
	}()

	// a reads, then writes. Without an immediate lock this fails fast.
	atx, err := a.DB().Begin()
	require.NoError(t, err)
	var n int
	require.NoError(t, atx.QueryRow(`SELECT count(*) FROM t`).Scan(&n))
	_, err = atx.Exec(`INSERT INTO t (n) VALUES (2)`)
	require.NoError(t, err)
	require.NoError(t, atx.Commit())
}

func execAll(t *testing.T, st *Store, qs ...string) {
	t.Helper()
	for _, q := range qs {
		_, err := st.db.ExecContext(t.Context(), q)
		require.NoError(t, err, q)
	}
}

// migrated opens a new database and applies every migration.
func migrated(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	return st
}

type itemRefs struct {
	Items  int `db:"items"`
	Images int `db:"images"`
	Links  int `db:"links"`
	Gifts  int `db:"gifts"`
}

func countItemRefs(t *testing.T, st *Store) itemRefs {
	t.Helper()
	var n itemRefs
	require.NoError(t, st.db.GetContext(t.Context(), &n, `SELECT
		(SELECT count(*) FROM items) AS items, (SELECT count(*) FROM item_images) AS images,
		(SELECT count(*) FROM store_links) AS links, (SELECT count(*) FROM gifts) AS gifts`))
	return n
}

// The schema lets an item's quantity be 0, for an item whose whole quantity
// was given, and its foreign keys cascade.
func TestSchemaLetsAnItemHaveQuantityZeroAndCascades(t *testing.T) {
	st := migrated(t)
	execAll(t, st,
		`INSERT INTO users (id, name, birthday_month, birthday_day, created_at) VALUES (1, 'Ada', 1, 1, '2026-11-09 12:00:00')`,
		`INSERT INTO lists (id, name, created_at) VALUES (1, 'Ada', '2026-11-09 12:00:00')`,
		`INSERT INTO items (id, list_id, title, quantity, position, created_at) VALUES (1, 1, 'Kettle', 2, 0, '2026-11-09 12:00:00')`,
		`INSERT INTO images (id, key, width, height, uploaded_by, created_at) VALUES (1, 'k1', 10, 10, 1, '2026-11-09 12:00:00')`,
		`INSERT INTO item_images (item_id, image_id, position, is_primary) VALUES (1, 1, 0, 1)`,
		`INSERT INTO store_links (item_id, url, store, created_at) VALUES (1, 'https://a.example/x', 'a.example', '2026-11-09 12:00:00')`,
		`INSERT INTO gifts (item_id, giver_id, quantity, occasion_date, created_at) VALUES (1, 1, 1, '2026-12-25', '2026-11-09 12:00:00')`,
	)

	execAll(t, st, `UPDATE items SET quantity = 0 WHERE id = 1`)
	_, err := st.db.ExecContext(t.Context(), `UPDATE items SET quantity = -1 WHERE id = 1`)
	require.Error(t, err)

	require.Equal(t, itemRefs{Items: 1, Images: 1, Links: 1, Gifts: 1}, countItemRefs(t, st))
	execAll(t, st, `DELETE FROM items WHERE id = 1`)
	require.Equal(t, itemRefs{}, countItemRefs(t, st))
}

// A user's delete takes their sessions, login links and passkeys with it, and an
// image they uploaded outlives them with no uploader (docs/spec.md,
// Deactivation and deletion, Deletion).
func TestSchemaLetsAUserBeDeleted(t *testing.T) {
	const at = "'2026-11-09 12:00:00'"
	st := migrated(t)
	execAll(t, st,
		`INSERT INTO users (id, name, birthday_month, birthday_day, created_at) VALUES (1, 'Ada', 1, 1, `+at+`), (2, 'Grace', 1, 1, `+at+`)`,
		`INSERT INTO lists (id, name, created_at) VALUES (1, 'Grace', `+at+`)`,
		`INSERT INTO items (id, list_id, title, position, created_at) VALUES (1, 1, 'Kettle', 0, `+at+`)`,
		`INSERT INTO sessions (user_id, token_hash, created_at, last_used_at, expires_at, cookie_sent_at) VALUES (2, 'h', `+at+`, `+at+`, `+at+`, `+at+`)`,
		`INSERT INTO login_links (user_id, token_hash, created_at, expires_at) VALUES (2, 'l', `+at+`, `+at+`)`,
		`INSERT INTO passkeys (user_id, credential_id, public_key, name, created_at) VALUES (2, x'01', x'02', 'Key', `+at+`)`,
		`INSERT INTO images (id, key, width, height, uploaded_by, created_at) VALUES (1, 'k1', 10, 10, 2, `+at+`), (2, 'k2', 10, 10, 2, `+at+`)`,
		`INSERT INTO item_images (item_id, image_id, position, is_primary) VALUES (1, 1, 0, 1)`,
	)
	count := func(table string) (n int) {
		require.NoError(t, st.db.GetContext(t.Context(), &n, `SELECT count(*) FROM `+table))
		return n
	}
	require.Equal(t, []int{1, 1, 1, 2, 1}, []int{count("sessions"), count("login_links"), count("passkeys"), count("images"), count("item_images")})

	execAll(t, st, `DELETE FROM users WHERE id = 2`)

	require.Equal(t, []int{0, 0, 0, 2, 1}, []int{count("sessions"), count("login_links"), count("passkeys"), count("images"), count("item_images")})
	var withUploader int
	require.NoError(t, st.db.GetContext(t.Context(), &withUploader, `SELECT count(*) FROM images WHERE uploaded_by IS NOT NULL`))
	require.Zero(t, withUploader)
	// A held image is still refused a delete, and a user's session still needs the user.
	_, err := st.db.ExecContext(t.Context(), `DELETE FROM images WHERE id = 1`)
	require.Error(t, err)
	_, err = st.db.ExecContext(t.Context(), `INSERT INTO sessions (user_id, token_hash, created_at, last_used_at, expires_at, cookie_sent_at) VALUES (9, 'x', `+at+`, `+at+`, `+at+`, `+at+`)`)
	require.Error(t, err)
}
