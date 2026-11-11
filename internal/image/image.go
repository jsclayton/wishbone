// Package image owns the images table: an image is a resource of its own that a
// client uploads, then attaches to another resource by ID (docs/spec.md, API
// principles, Images). The bytes live in [imagestore.Store]. This package keeps
// the row that names the file, who uploaded it, and what it is attached to.
//
// # How an attachment works
//
// A resource that holds an image has a column that refers to images, such as
// users.avatar_image_id. That column is the attachment, and an image is attached
// to one resource at most. To add a resource that holds images, a change does
// three things:
//
//   - gives the column a foreign key to images(id) and a unique index, so the
//     database refuses a second holder and refuses to delete a held image;
//   - adds the column to [heldSQL] and to [Attachment], so the sweep and the
//     visibility rules know it;
//   - calls [Detach] when a change removes an image from the resource.
//
// # Deleting images
//
// The sweep ([Service.Sweep]) deletes an image that is no longer attached, or
// that was not attached in 24 hours. It deletes the row first and the file only
// after the row is gone, and it deletes only rows that nothing holds, so a file
// that a row refers to is never deleted. It never lists the files on disk: a
// file with no row is not its business. Do not add a sweep of files that have no
// row. A data dir can hold files that a migration has not given a row yet.
package image

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/imagestore"
)

const (
	// UnattachedFor is how long an image may stay without being attached. The
	// sweep deletes it after that.
	UnattachedFor = 24 * time.Hour
	// SweepEvery is how often the server runs the sweep.
	SweepEvery = time.Hour
)

var (
	// ErrNotFound means no image has the ID.
	ErrNotFound = errors.New("no such image")
	// ErrFetchFailed means a download of an image address failed: the address is
	// on the instance's own network, the download fails or is too large, or the
	// file is not an image.
	ErrFetchFailed = errors.New("the image could not be downloaded")
	// ErrInvalid means the bytes are not an image that Wishbone accepts. The
	// message is safe to show. It matches [imagestore.ErrInvalid].
	ErrInvalid = imagestore.ErrInvalid
)

// Image is an image as the database holds it.
type Image struct {
	ID     int64
	Key    string
	Width  int
	Height int
	// UploadedBy is the user who uploaded the image, or nil once that user is
	// deleted: the image can outlive them when another resource holds it.
	UploadedBy *int64
	CreatedAt  time.Time
	// Detached is true for an image that a resource let go of. The sweep
	// deletes it, and no resource can take it again.
	Detached bool
}

type row struct {
	ID         int64         `db:"id"`
	Key        string        `db:"key"`
	Width      int           `db:"width"`
	Height     int           `db:"height"`
	UploadedBy sql.NullInt64 `db:"uploaded_by"`
	CreatedAt  time.Time     `db:"created_at"`
	DetachedAt sql.NullTime  `db:"detached_at"`
}

const columns = `id, key, width, height, uploaded_by, created_at, detached_at`

func (r row) image() Image {
	img := Image{
		ID: r.ID, Key: r.Key, Width: r.Width, Height: r.Height,
		CreatedAt: r.CreatedAt, Detached: r.DetachedAt.Valid,
	}
	if r.UploadedBy.Valid {
		img.UploadedBy = &r.UploadedBy.Int64
	}
	return img
}

// Summary is an image as another resource contains it: what a client needs to
// load and lay out the image. The address of its bytes is /images/{id}.
type Summary struct {
	ID     int64
	Width  int
	Height int
}

// Kind says what kind of resource holds an image.
type Kind string

// The kinds of resource that hold an image.
const (
	// KindAvatar is a user's avatar. The holder's ID is the user's.
	KindAvatar Kind = "avatar"
	// KindItem is one of an item's images. The holder's ID is the item's.
	KindItem Kind = "item"
	// KindGift is the copy of an item's image that a gift keeps from its reveal.
	// The holder's ID is the gift's.
	KindGift Kind = "gift"
)

// Attachment says which resource holds an image. The zero value means none.
type Attachment struct {
	Kind Kind
	// ID is the ID of the resource that holds the image, such as a user.
	ID int64
}

// Attached reports whether a resource holds the image.
func (a Attachment) Attached() bool { return a.Kind != "" }

// heldSQL selects the ID of every image that a resource holds. Add a SELECT for
// each column that refers to images.
const heldSQL = `SELECT avatar_image_id FROM users WHERE avatar_image_id IS NOT NULL
	UNION ALL SELECT image_id FROM item_images
	UNION ALL SELECT image_id FROM gifts WHERE image_id IS NOT NULL`

// Service stores, loads, and deletes images.
type Service struct {
	db    *sqlx.DB
	files *imagestore.Store
	now   func() time.Time
}

// NewService returns a Service. files holds the bytes, and now is the clock, so
// tests control time.
func NewService(db *sqlx.DB, files *imagestore.Store, now func() time.Time) *Service {
	return &Service{db: db, files: files, now: func() time.Time { return now().UTC() }}
}

// Create reads an image from r, stores it as a JPEG of at most
// [imagestore.MaxDimension] pixels with none of the original's metadata, and
// returns it. It returns [ErrInvalid] for bytes that are not a JPEG, PNG, GIF,
// or WebP image within the limits. The new image is attached to nothing: only
// the uploader sees it, and the sweep deletes it after [UnattachedFor].
func (s *Service) Create(ctx context.Context, uploader int64, r io.Reader) (Image, error) {
	saved, err := s.files.Put(r)
	if err != nil {
		return Image{}, err
	}
	return s.insert(ctx, uploader, saved)
}

// CreateFromURL downloads an http or https image address and stores it as
// [Service.Create] does. It refuses the instance's own network. Every failure of
// the download matches [ErrFetchFailed].
func (s *Service) CreateFromURL(ctx context.Context, uploader int64, rawURL string) (Image, error) {
	saved, err := s.files.DownloadPut(ctx, rawURL)
	if err != nil {
		return Image{}, fmt.Errorf("%w: %w", ErrFetchFailed, err)
	}
	return s.insert(ctx, uploader, saved)
}

// insert records a stored file as an image that nothing holds.
func (s *Service) insert(ctx context.Context, uploader int64, saved imagestore.Saved) (Image, error) {
	// The file is written before the row, so a row always has its file.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO images (key, width, height, uploaded_by, created_at) VALUES (?, ?, ?, ?, ?)`,
		saved.Key, saved.Width, saved.Height, uploader, s.now())
	if err != nil {
		// Nothing refers to the file yet, so it is ours to delete.
		_ = s.files.Delete(saved.Key)
		return Image{}, fmt.Errorf("insert image: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Image{}, err
	}
	return Get(ctx, s.db, id)
}

// Copy stores a second copy of an image under a new key, and records it in q (a
// transaction) as an image of its own, uploaded by the user, that nothing holds
// yet. The caller attaches it in the same transaction. Deleting either image
// leaves the other. It returns [ErrNotFound] when the source has no file.
//
// The file is written before the row is committed. If the transaction then fails,
// the caller calls [Service.Discard] with the new image, so no file is left that
// no row names.
func (s *Service) Copy(ctx context.Context, q sqlx.ExtContext, uploader, id int64) (Image, error) {
	src, err := Get(ctx, q, id)
	if err != nil {
		return Image{}, err
	}
	key, err := s.files.Copy(src.Key)
	if errors.Is(err, imagestore.ErrNotFound) {
		return Image{}, ErrNotFound
	}
	if err != nil {
		return Image{}, fmt.Errorf("copy image file: %w", err)
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO images (key, width, height, uploaded_by, created_at) VALUES (?, ?, ?, ?, ?)`,
		key, src.Width, src.Height, uploader, s.now())
	if err != nil {
		_ = s.files.Delete(key)
		return Image{}, fmt.Errorf("insert image copy: %w", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		_ = s.files.Delete(key)
		return Image{}, fmt.Errorf("insert image copy: %w", err)
	}
	return Get(ctx, q, newID)
}

// Discard deletes the file of an image from [Service.Copy] whose transaction did
// not commit. Its row is gone with the rollback, so nothing refers to the file.
func (s *Service) Discard(img Image) error { return s.files.Delete(img.Key) }

// Get loads an image. It returns [ErrNotFound] when there is none.
func (s *Service) Get(ctx context.Context, id int64) (Image, error) {
	return Get(ctx, s.db, id)
}

// Get loads an image from a database or a transaction.
func Get(ctx context.Context, q sqlx.QueryerContext, id int64) (Image, error) {
	var r row
	err := sqlx.GetContext(ctx, q, &r, `SELECT `+columns+` FROM images WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Image{}, ErrNotFound
	}
	if err != nil {
		return Image{}, fmt.Errorf("get image: %w", err)
	}
	return r.image(), nil
}

// AttachedTo says which resource holds the image, from a database or a
// transaction.
func AttachedTo(ctx context.Context, q sqlx.QueryerContext, id int64) (Attachment, error) {
	var holder int64
	err := sqlx.GetContext(ctx, q, &holder, `SELECT id FROM users WHERE avatar_image_id = ?`, id)
	switch {
	case err == nil:
		return Attachment{Kind: KindAvatar, ID: holder}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Attachment{}, fmt.Errorf("find what holds image: %w", err)
	}
	err = sqlx.GetContext(ctx, q, &holder, `SELECT item_id FROM item_images WHERE image_id = ?`, id)
	switch {
	case err == nil:
		return Attachment{Kind: KindItem, ID: holder}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Attachment{}, fmt.Errorf("find what holds image: %w", err)
	}
	err = sqlx.GetContext(ctx, q, &holder, `SELECT id FROM gifts WHERE image_id = ?`, id)
	switch {
	case err == nil:
		return Attachment{Kind: KindGift, ID: holder}, nil
	case errors.Is(err, sql.ErrNoRows):
		return Attachment{}, nil
	}
	return Attachment{}, fmt.Errorf("find what holds image: %w", err)
}

// Attachment says which resource holds the image.
func (s *Service) Attachment(ctx context.Context, id int64) (Attachment, error) {
	return AttachedTo(ctx, s.db, id)
}

// Detach records that a resource let go of an image, in the same transaction as
// the change. The sweep then deletes the image. An image can't be attached
// again after that: a client uploads a new one.
func Detach(ctx context.Context, q sqlx.ExecerContext, now time.Time, id int64) error {
	if _, err := q.ExecContext(ctx, `UPDATE images SET detached_at = ? WHERE id = ?`, now.UTC(), id); err != nil {
		return fmt.Errorf("detach image: %w", err)
	}
	return nil
}

// DetachUnheldBy lets go of every image that the user uploaded and that no
// resource holds, in the transaction that deletes the user: only the user could
// see such an image, so nobody can after the delete, and the sweep deletes them at
// once and not 24 hours later. An image that a resource holds stays, with no
// uploader once the user is gone. The user's avatar is detached first (see
// [Detach]), because the user row, which holds it, is about to go.
func DetachUnheldBy(ctx context.Context, q sqlx.ExecerContext, now time.Time, userID int64) error {
	if _, err := q.ExecContext(ctx, `UPDATE images SET detached_at = ?
		WHERE uploaded_by = ? AND detached_at IS NULL AND id NOT IN (`+heldSQL+`)`, now.UTC(), userID); err != nil {
		return fmt.Errorf("detach the images of a user: %w", err)
	}
	return nil
}

// Open returns the bytes of the image. The caller closes the file.
func (s *Service) Open(img Image) (*os.File, error) {
	f, err := s.files.Open(img.Key)
	if errors.Is(err, imagestore.ErrNotFound) {
		return nil, ErrNotFound
	}
	return f, err
}

// Sweep deletes every image that nothing holds and that was detached, or that
// was uploaded more than [UnattachedFor] ago. It returns how many it deleted.
//
// It deletes rows first, in one statement that checks [heldSQL], and then the
// files of the rows it deleted. A row that something holds is not deleted, and
// the foreign keys refuse it anyway, so no file of a held image is deleted. If
// a file can't be deleted, the error says so and the file stays, with no row.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	cutoff := s.now().Add(-UnattachedFor)
	var keys []string
	err := s.db.SelectContext(ctx, &keys,
		`DELETE FROM images
		 WHERE id NOT IN (`+heldSQL+`) AND (detached_at IS NOT NULL OR created_at < ?)
		 RETURNING key`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep images: %w", err)
	}
	var failed error
	for _, key := range keys {
		if err := s.files.Delete(key); err != nil {
			failed = errors.Join(failed, fmt.Errorf("delete image file %s: %w", key, err))
		}
	}
	return len(keys), failed
}

// Schedule runs [Service.Sweep] now and then every [SweepEvery] until ctx ends.
// A failed run is logged and tried again on the next tick. logger may be nil.
func (s *Service) Schedule(ctx context.Context, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	tick := time.NewTicker(SweepEvery)
	defer tick.Stop()
	for {
		switch n, err := s.Sweep(ctx); {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Error("image sweep failed", slog.Any("error", err))
		case n > 0:
			logger.Info("deleted images", slog.Int("count", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
