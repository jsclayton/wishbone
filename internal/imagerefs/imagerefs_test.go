package imagerefs_test

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/imagerefs"
	"jsclayton.dev/wishbone/internal/store"
)

func exec(t *testing.T, st *store.Store, q string) {
	t.Helper()
	_, err := st.DB().ExecContext(t.Context(), q)
	require.NoError(t, err)
}

// A new database has no tables yet, so no row uses an image.
func TestKeysAtVersionZeroHasNoKeys(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "new.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	keys, err := imagerefs.KeysAt(t.Context(), st.DB(), 0)
	require.NoError(t, err)
	require.Empty(t, keys)
}

// The binary refuses a database older than the baseline, so no query is kept
// for a schema from before it.
func TestKeysAtRefusesASchemaOlderThanTheBaseline(t *testing.T) {
	for _, v := range []int64{1, 20261103003804, 20261105204058, store.BaselineVersion - 1} {
		_, err := imagerefs.KeysAt(t.Context(), nil, v)
		require.ErrorIs(t, err, imagerefs.ErrUnknownSchema, "version %d", v)
	}
}

// A snapshot must read every database version that the binary accepts: the
// baseline, and every version after it. The last entry of the schemas fits all
// the newer ones.
func TestKeysAtHasAQueryForEveryVersionTheBinaryAccepts(t *testing.T) {
	st := openBaseline(t)
	for _, v := range []int64{store.BaselineVersion, store.BaselineVersion + 1, math.MaxInt64} {
		_, err := imagerefs.KeysAt(t.Context(), st.DB(), v)
		require.NoError(t, err, "version %d", v)
	}
}

func openBaseline(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	return st
}

// Every row of images has a file, whether a resource holds the row or not. The
// sweep deletes the row before the file, so the backup checks them all.
func TestKeysAtADatabaseWithImagesListsEveryImage(t *testing.T) {
	st := openBaseline(t)
	exec(t, st, `INSERT INTO users (id, name, birthday_month, birthday_day, created_at) VALUES (1, 'Ann', 1, 1, CURRENT_TIMESTAMP)`)
	exec(t, st, `INSERT INTO images (id, key, width, height, uploaded_by, created_at) VALUES
		(1, 'held', 1, 1, 1, CURRENT_TIMESTAMP), (2, 'loose', 1, 1, 1, CURRENT_TIMESTAMP)`)
	exec(t, st, `UPDATE users SET avatar_image_id = 1 WHERE id = 1`)
	v, err := store.Version(t.Context(), st.DB())
	require.NoError(t, err)
	require.Equal(t, store.BaselineVersion, v)

	keys, err := imagerefs.KeysAt(t.Context(), st.DB(), v)

	require.NoError(t, err)
	require.ElementsMatch(t, []string{"held", "loose"}, keys)
}
