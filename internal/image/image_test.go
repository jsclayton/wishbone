package image_test

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	wbimage "jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

var start = time.Date(2026, time.November, 9, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t      *testing.T
	images *wbimage.Service
	files  *imagestore.Store
	st     *store.Store
	now    time.Time
	user   int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	files, err := imagestore.New(filepath.Join(dir, "images"), imagestore.Options{})
	require.NoError(t, err)
	f := &fixture{t: t, files: files, st: st, now: start}
	f.images = wbimage.NewService(st.DB(), files, func() time.Time { return f.now })
	f.user, err = user.Create(t.Context(), st.DB(), start, user.NewUser{Name: "Ann", Birthday: user.PartialDate{Month: 1, Day: 1}})
	require.NoError(t, err)
	return f
}

func smallPNG(t *testing.T) *bytes.Reader {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4, 3))))
	return bytes.NewReader(b.Bytes())
}

func (f *fixture) hasFile(key string) bool {
	f.t.Helper()
	file, err := f.files.Open(key)
	if err != nil {
		require.ErrorIs(f.t, err, imagestore.ErrNotFound)
		return false
	}
	_ = file.Close()
	return true
}

// A crash between writing a file and inserting its row leaves a file that no
// row names. So can a data dir that holds files a migration has not given rows
// yet. The sweep reads rows, so it never deletes such a file, however old.
func TestSweepNeverDeletesAFileThatNoRowNames(t *testing.T) {
	f := newFixture(t)
	orphan, err := f.files.Save(smallPNG(t)) // the write of a request that crashed before its insert
	require.NoError(t, err)
	img, err := f.images.Create(t.Context(), f.user, smallPNG(t))
	require.NoError(t, err)
	f.now = f.now.Add(30 * 24 * time.Hour)

	n, err := f.images.Sweep(t.Context())

	require.NoError(t, err)
	require.Equal(t, 1, n, "the unattached image with a row went")
	require.False(t, f.hasFile(img.Key))
	require.True(t, f.hasFile(orphan), "the file with no row stays")
}

func TestSweepDeletesAFileOnlyAfterItsRowIsGone(t *testing.T) {
	f := newFixture(t)
	img, err := f.images.Create(t.Context(), f.user, smallPNG(t))
	require.NoError(t, err)
	_, err = f.st.DB().ExecContext(t.Context(), `UPDATE users SET avatar_image_id = ? WHERE id = ?`, img.ID, f.user)
	require.NoError(t, err)
	f.now = f.now.Add(30 * 24 * time.Hour)

	n, err := f.images.Sweep(t.Context())

	require.NoError(t, err)
	require.Zero(t, n, "a held image is never too old")
	require.True(t, f.hasFile(img.Key))
	_, err = f.images.Get(t.Context(), img.ID)
	require.NoError(t, err)

	_, err = f.st.DB().ExecContext(t.Context(), `UPDATE users SET avatar_image_id = NULL WHERE id = ?`, f.user)
	require.NoError(t, err)
	n, err = f.images.Sweep(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.False(t, f.hasFile(img.Key))
	_, err = f.images.Get(t.Context(), img.ID)
	require.ErrorIs(t, err, wbimage.ErrNotFound)
}
