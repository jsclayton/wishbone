package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/user"
)

const (
	// MaxStoreLinks is the most store links one item can have.
	MaxStoreLinks = 10
	// MaxImages is the most images one item can have.
	MaxImages = 5
)

var (
	// ErrTooManyStoreLinks means the input has more than [MaxStoreLinks] store links.
	ErrTooManyStoreLinks = errors.New("an item can have at most 10 store links")
	// ErrImageAttached means another item took an image first: the unique index
	// on item_images refused it. The server's guard answers the usual case with
	// a 409 before the write. This is the same answer for a write that slips past.
	ErrImageAttached = errors.New("that image is already attached to something else")
	// ErrTooManyImages means the input has more than [MaxImages] images.
	ErrTooManyImages = errors.New("an item can have at most 5 images")
)

// StoreLink is one place where an item can be bought: a product page at a store,
// with its own price, currency, and captured date. The store links of an item
// are interchangeable ways to buy the same thing.
type StoreLink struct {
	ID    int64
	URL   string
	Store string
	// PriceMinor is in the currency's minor unit. Currency is nil exactly when it is.
	PriceMinor *int64
	Currency   *string
	// CapturedAt is when the price was captured, or nil when there is no price.
	CapturedAt *time.Time
	// ImageURL is the store's own address of its picture, or nil. It is outside Wishbone.
	ImageURL  *string
	CreatedAt time.Time
}

// NewStoreLink is a store link to add with a new item. The caller has normalized
// the address, named the store, and checked the blocked stores.
type NewStoreLink struct {
	URL, Store string
	PriceMinor *int64
	Currency   string // empty for the default, with a price
	ImageURL   string // empty for none
}

// cleanStoreLink applies the rules that tie a store link's price fields together.
func cleanStoreLink(n NewStoreLink, i int) (NewStoreLink, error) {
	field := func(name string) string { return fmt.Sprintf("store_links.%d.%s", i, name) }
	switch {
	case n.PriceMinor != nil && *n.PriceMinor < 0:
		return n, invalid(field("price_minor"), "the price can't be negative")
	case n.PriceMinor == nil && n.Currency != "":
		return n, invalid(field("currency"), "a currency needs a price")
	case n.PriceMinor != nil && n.Currency == "":
		n.Currency = DefaultCurrency
	}
	if n.PriceMinor != nil && !validCurrency(n.Currency) {
		return n, invalid(field("currency"), "use a 3-letter currency code such as USD")
	}
	return n, nil
}

// sortForGivers puts the store links in the order everyone who can't edit the
// list sees: within one currency, the lowest price first and the unpriced last.
// With prices in several currencies, the order added is kept. links must be in
// the order added. The input is not changed.
func sortForGivers(links []StoreLink) []StoreLink {
	out := append([]StoreLink{}, links...)
	currency, mixed, priced := "", false, false
	for _, l := range out {
		if l.PriceMinor == nil {
			continue
		}
		priced = true
		if currency != "" && *l.Currency != currency {
			mixed = true
		}
		currency = *l.Currency
	}
	if mixed || !priced {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].PriceMinor, out[j].PriceMinor
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		}
		return *a < *b
	})
	return out
}

type storeLinkRow struct {
	ID         int64          `db:"id"`
	ItemID     int64          `db:"item_id"`
	URL        string         `db:"url"`
	Store      string         `db:"store"`
	PriceMinor sql.NullInt64  `db:"price_minor"`
	Currency   sql.NullString `db:"currency"`
	CapturedAt sql.NullTime   `db:"captured_at"`
	ImageURL   sql.NullString `db:"image_url"`
	CreatedAt  time.Time      `db:"created_at"`
}

func (r storeLinkRow) storeLink() StoreLink {
	l := StoreLink{ID: r.ID, URL: r.URL, Store: r.Store, CreatedAt: r.CreatedAt}
	if r.PriceMinor.Valid {
		l.PriceMinor = &r.PriceMinor.Int64
	}
	if r.Currency.Valid {
		l.Currency = &r.Currency.String
	}
	if r.CapturedAt.Valid {
		l.CapturedAt = &r.CapturedAt.Time
	}
	if r.ImageURL.Valid {
		l.ImageURL = &r.ImageURL.String
	}
	return l
}

type imageRow struct {
	ItemID    int64 `db:"item_id"`
	ImageID   int64 `db:"image_id"`
	Width     int   `db:"width"`
	Height    int   `db:"height"`
	IsPrimary bool  `db:"is_primary"`
}

// loadExtras fills in the store links and the images of the items, with one
// query for each. Every item gets non-nil slices, so a client sees `[]` and
// never `null`.
func loadExtras(ctx context.Context, q sqlx.QueryerContext, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]int64, len(items))
	at := make(map[int64]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
		at[it.ID] = i
		items[i].StoreLinks = []StoreLink{}
		items[i].Images = []image.Summary{}
	}

	query, args, err := sqlx.In(`SELECT id, item_id, url, store, price_minor, currency, captured_at, image_url, created_at
		FROM store_links WHERE item_id IN (?) ORDER BY id`, ids)
	if err != nil {
		return err
	}
	var links []storeLinkRow
	if err := sqlx.SelectContext(ctx, q, &links, query, args...); err != nil {
		return fmt.Errorf("load store links: %w", err)
	}
	for _, r := range links {
		i := at[r.ItemID]
		items[i].StoreLinks = append(items[i].StoreLinks, r.storeLink())
	}
	for i := range items {
		if !items[i].CanEdit {
			items[i].StoreLinks = sortForGivers(items[i].StoreLinks)
		}
	}

	query, args, err = sqlx.In(`SELECT item_images.item_id, item_images.image_id, images.width, images.height, item_images.is_primary
		FROM item_images JOIN images ON images.id = item_images.image_id
		WHERE item_images.item_id IN (?) ORDER BY item_images.item_id, item_images.position`, ids)
	if err != nil {
		return err
	}
	var imgs []imageRow
	if err := sqlx.SelectContext(ctx, q, &imgs, query, args...); err != nil {
		return fmt.Errorf("load item images: %w", err)
	}
	for _, r := range imgs {
		i := at[r.ItemID]
		s := image.Summary{ID: r.ImageID, Width: r.Width, Height: r.Height}
		items[i].Images = append(items[i].Images, s)
		if r.IsPrimary {
			primary := s
			items[i].PrimaryImage = &primary
		}
	}
	return nil
}

// cleanImages checks the image IDs and the primary image of a new item, and
// returns the index of the primary image. The first image is the default.
func cleanImages(ids []int64, primary *int64) (int, error) {
	if len(ids) > MaxImages {
		return 0, ErrTooManyImages
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return 0, invalid("image_ids", "an image can be listed only once")
		}
		seen[id] = true
	}
	if primary == nil {
		return 0, nil
	}
	for i, id := range ids {
		if id == *primary {
			return i, nil
		}
	}
	return 0, invalid("primary_image_id", "the primary image must be one of the item's images")
}

// AddGuard runs inside the transaction of [Service.Add], before the item is
// written, with the transaction as q. An error from it stops the add. The check
// that the viewer may use each image goes there.
type AddGuard func(ctx context.Context, q sqlx.ExtContext) error

// insertStoreLink writes a store link that [cleanStoreLink] has checked, at the
// end of the item's links, and returns its ID. A price is captured now.
func insertStoreLink(ctx context.Context, tx *sqlx.Tx, itemID int64, l NewStoreLink, now time.Time) (int64, error) {
	var captured *time.Time
	var currency *string
	if l.PriceMinor != nil {
		captured, currency = &now, &l.Currency
	}
	var imageURL *string
	if l.ImageURL != "" {
		imageURL = &l.ImageURL
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO store_links (item_id, url, store, price_minor, currency, captured_at, image_url, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		itemID, l.URL, l.Store, l.PriceMinor, currency, captured, imageURL, now)
	if err != nil {
		return 0, fmt.Errorf("insert store link: %w", err)
	}
	return res.LastInsertId()
}

// insertExtras writes the store links and the images of a new item.
func insertExtras(ctx context.Context, tx *sqlx.Tx, itemID int64, n New, primary int, now time.Time) error {
	for _, l := range n.StoreLinks {
		if _, err := insertStoreLink(ctx, tx, itemID, l, now); err != nil {
			return err
		}
	}
	for i, imageID := range n.ImageIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_images (item_id, image_id, position, is_primary) VALUES (?, ?, ?, ?)`,
			itemID, imageID, i, i == primary); err != nil {
			return attachError(err)
		}
	}
	return nil
}

// attachError turns the error of an insert into item_images into
// [ErrImageAttached] when the unique index on image_id refused it. It matches
// the error by its code, not by its message. Every other unique index of the
// table is on a column this package fills in itself and can't repeat, and the
// primary key has a code of its own, so a unique violation here is another
// item's image.
func attachError(err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return ErrImageAttached
	}
	return fmt.Errorf("attach image: %w", err)
}

// imagePlan is what an item's images are after a change: their IDs in order,
// and the index of the primary image (-1 when there are none).
type imagePlan struct {
	ids     []int64
	primary int
}

// planImages applies the changes to the item's current images and checks the
// result against the rules in docs/spec.md, Images on an item.
func planImages(current Item, c Changes) (imagePlan, error) {
	ids := make([]int64, len(current.Images))
	for i, img := range current.Images {
		ids[i] = img.ID
	}
	if c.ImageIDs.Set {
		if c.ImageIDs.Value == nil {
			return imagePlan{}, invalid("image_ids", "image_ids can't be null: send an empty array to remove every image")
		}
		ids = *c.ImageIDs.Value
	}
	if len(ids) > MaxImages {
		return imagePlan{}, ErrTooManyImages
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return imagePlan{}, invalid("image_ids", "an image can be listed only once")
		}
		seen[id] = true
	}

	indexOf := func(want int64) int {
		for i, id := range ids {
			if id == want {
				return i
			}
		}
		return -1
	}
	primary := -1
	switch {
	case c.PrimaryImageID.Set && c.PrimaryImageID.Value != nil:
		if primary = indexOf(*c.PrimaryImageID.Value); primary < 0 {
			return imagePlan{}, invalid("primary_image_id", "the primary image must be one of the item's images")
		}
	case c.PrimaryImageID.Set:
		// null: the first image.
	case current.PrimaryImage != nil:
		primary = indexOf(current.PrimaryImage.ID)
	}
	if primary < 0 && len(ids) > 0 {
		// The primary image was removed (or there was none): the first image takes its place.
		primary = 0
	}
	return imagePlan{ids: ids, primary: primary}, nil
}

// replaceImages sets the images of an item to what the changes make of them, in
// the order given, and detaches the ones left out. The caller has checked, in the
// same transaction, that the viewer may attach the new ones.
//
// It returns the ID of the primary image after the change, or nil when there is none.
func replaceImages(ctx context.Context, tx *sqlx.Tx, current Item, c Changes, now time.Time) (*int64, error) {
	plan, err := planImages(current, c)
	if err != nil {
		return nil, err
	}
	kept := map[int64]bool{}
	for _, id := range plan.ids {
		kept[id] = true
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_images WHERE item_id = ?`, current.ID); err != nil {
		return nil, fmt.Errorf("replace item images: %w", err)
	}
	for _, img := range current.Images {
		if kept[img.ID] {
			continue
		}
		if err := image.Detach(ctx, tx, now, img.ID); err != nil {
			return nil, err
		}
	}
	for i, id := range plan.ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO item_images (item_id, image_id, position, is_primary) VALUES (?, ?, ?, ?)`,
			current.ID, id, i, i == plan.primary); err != nil {
			return nil, attachError(err)
		}
	}
	if plan.primary < 0 {
		return nil, nil
	}
	return &plan.ids[plan.primary], nil
}

// ImageIDs returns the IDs of the item's images, in order.
func (it Item) ImageIDs() []int64 {
	ids := make([]int64, len(it.Images))
	for i, img := range it.Images {
		ids[i] = img.ID
	}
	return ids
}

// detachImages lets go of every image of an item, in the transaction that
// deletes the item. The sweep then deletes them.
func detachImages(ctx context.Context, tx *sqlx.Tx, itemID int64, now time.Time) error {
	var ids []int64
	if err := tx.SelectContext(ctx, &ids, `SELECT image_id FROM item_images WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("find item images: %w", err)
	}
	for _, id := range ids {
		if err := image.Detach(ctx, tx, now, id); err != nil {
			return err
		}
	}
	return nil
}

// CanSee says whether the viewer can see an item's images: whether they can see
// the item, or give a gift on it. A gift shows the item's primary image to its
// giver, also after the item was deleted softly or became fully claimed.
// q is a database or a transaction.
func CanSee(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (bool, error) {
	_, err := get(ctx, q, viewer, id)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	var gives bool
	if err := sqlx.GetContext(ctx, q, &gives, `SELECT EXISTS (SELECT 1 FROM gifts WHERE item_id = ? AND giver_id = ?)`, id, viewer.ID); err != nil {
		return false, fmt.Errorf("find the gifts of a giver: %w", err)
	}
	return gives, nil
}
