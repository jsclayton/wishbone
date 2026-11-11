package item

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

// The item price is the price givers see (docs/routes.md, Items, The item
// price; ADR 0005). It starts from a store link: an item with no price takes the
// first price that one of its store links gets, once, when the link is created
// with the item, added, or given a price by hand. After that only a user who can
// edit the list changes it. A refresh never does.
//
// "Never had a price" is an item with no price and price_cleared unset. A change
// to the item that clears a price the item had sets price_cleared, so a cleared
// price stays cleared.

// clearedSQL is the SET clause for price_cleared in an update that writes
// price_minor from the first argument. It is set when the update clears a price
// the item had. SQLite reads the old price_minor in a SET expression.
const clearedSQL = "price_cleared = price_cleared OR (price_minor IS NOT NULL AND ? IS NULL)"

// adoptStoreLinkPrice is the one place a store link's price becomes the item
// price. Call it, in the same transaction, every time a store link gets a price
// on a route other than a refresh. It does nothing when the link has no price,
// or when the item has, or has had, a price of its own.
func adoptStoreLinkPrice(ctx context.Context, tx *sqlx.Tx, itemID int64, minor *int64, currency string) error {
	if minor == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET price_minor = ?, top_price_minor = NULL, currency = ?
		 WHERE id = ? AND price_minor IS NULL AND price_cleared = 0`, *minor, currency, itemID); err != nil {
		return fmt.Errorf("adopt store link price: %w", err)
	}
	return nil
}

// ErrStoreLinkNotFound means the item has no store link with the ID.
var ErrStoreLinkNotFound = errors.New("no such store link")

// StoreLinkChanges is a merge patch to a store link's price.
type StoreLinkChanges struct {
	// PriceMinor is the store's price in the currency's minor unit. null clears
	// the price and its currency.
	PriceMinor patch.Optional[int64]
	// Currency is an ISO 4217 code. A price with no currency keeps the link's
	// currency, or is in [DefaultCurrency].
	Currency patch.Optional[string]
}

// StoreLinkGuard runs inside the transaction of [Service.ChangeStoreLink] and
// [Service.DeleteStoreLink], before the write, with the store link as the viewer
// sees it. An error from it stops the write: an If-Match check goes there.
type StoreLinkGuard func(ctx context.Context, q sqlx.ExtContext, current StoreLink) error

// editableItem loads an item that the viewer can change.
func editableItem(ctx context.Context, tx *sqlx.Tx, viewer user.User, itemID int64) (Item, error) {
	it, err := get(ctx, tx, viewer, itemID)
	if err != nil {
		return Item{}, err
	}
	if !it.CanEdit {
		return Item{}, list.NotEditable(ctx, tx, viewer, it.ListID)
	}
	return it, nil
}

func findStoreLink(it Item, id int64) (StoreLink, bool) {
	for _, l := range it.StoreLinks {
		if l.ID == id {
			return l, true
		}
	}
	return StoreLink{}, false
}

// AddStoreLink adds a store link to the end of an item that the viewer can
// change, and returns it. The caller has normalized the address, named the store,
// checked the blocked stores and read the price. It returns [ErrNotFound] for an
// item the viewer can't see, [list.ErrNotEditable] for one they see and can't
// change, [ErrTooManyStoreLinks] and [ErrInvalid]. An item with no price that has
// never had one takes the link's price.
func (s *Service) AddStoreLink(ctx context.Context, viewer user.User, itemID int64, n NewStoreLink) (StoreLink, error) {
	n, err := cleanStoreLink(n, 0)
	if err != nil {
		return StoreLink{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return StoreLink{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	it, err := editableItem(ctx, tx, viewer, itemID)
	if err != nil {
		return StoreLink{}, err
	}
	if len(it.StoreLinks) >= MaxStoreLinks {
		return StoreLink{}, ErrTooManyStoreLinks
	}
	id, err := insertStoreLink(ctx, tx, itemID, n, s.now())
	if err != nil {
		return StoreLink{}, err
	}
	if err := adoptStoreLinkPrice(ctx, tx, itemID, n.PriceMinor, n.Currency); err != nil {
		return StoreLink{}, err
	}
	after, err := get(ctx, tx, viewer, itemID)
	if err != nil {
		return StoreLink{}, err
	}
	if err := tx.Commit(); err != nil {
		return StoreLink{}, fmt.Errorf("commit: %w", err)
	}
	link, _ := findStoreLink(after, id)
	return link, nil
}

// ChangeStoreLink sets, changes or clears the price of a store link of an item
// that the viewer can change, and returns the link. The captured date becomes
// now when the change sets a price or clears one. A change that leaves a link
// without a price as it is captures nothing. It returns [ErrNotFound],
// [list.ErrNotEditable], [ErrStoreLinkNotFound] and [ErrInvalid]. An item with no
// price that has never had one takes the link's first price.
func (s *Service) ChangeStoreLink(ctx context.Context, viewer user.User, itemID, linkID int64, c StoreLinkChanges, guard StoreLinkGuard) (StoreLink, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return StoreLink{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	it, err := editableItem(ctx, tx, viewer, itemID)
	if err != nil {
		return StoreLink{}, err
	}
	cur, ok := findStoreLink(it, linkID)
	if !ok {
		return StoreLink{}, ErrStoreLinkNotFound
	}
	if guard != nil {
		if err := guard(ctx, tx, cur); err != nil {
			return StoreLink{}, err
		}
	}

	price, currency, captured := cur.PriceMinor, cur.Currency, cur.CapturedAt
	if c.PriceMinor.Set {
		price = c.PriceMinor.Value
		if price == nil {
			currency = nil
		}
	}
	if price != nil && *price < 0 {
		return StoreLink{}, invalid("price_minor", "the price can't be negative")
	}
	if c.Currency.Set {
		switch {
		case c.Currency.Value == nil && price != nil:
			return StoreLink{}, invalid("currency", "the currency can't be cleared while there is a price: clear the price instead")
		case c.Currency.Value != nil && price == nil:
			return StoreLink{}, invalid("currency", "a currency needs a price")
		case c.Currency.Value != nil && !validCurrency(*c.Currency.Value):
			return StoreLink{}, invalid("currency", "use a 3-letter currency code such as USD")
		case c.Currency.Value != nil:
			currency = c.Currency.Value
		}
	}
	if price != nil && currency == nil {
		def := DefaultCurrency
		currency = &def
	}
	if (c.PriceMinor.Set || c.Currency.Set) && (price != nil || cur.PriceMinor != nil) {
		now := s.now()
		captured = &now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE store_links SET price_minor = ?, currency = ?, captured_at = ? WHERE id = ? AND item_id = ?`,
		price, currency, captured, linkID, itemID); err != nil {
		return StoreLink{}, fmt.Errorf("update store link: %w", err)
	}
	if err := flagStoreLinkPrices(ctx, tx, itemID, s.now(), []storeLinkPrice{{cur, price, currency}}); err != nil {
		return StoreLink{}, err
	}
	if c.PriceMinor.Set && price != nil {
		if err := adoptStoreLinkPrice(ctx, tx, itemID, price, *currency); err != nil {
			return StoreLink{}, err
		}
	}
	after, err := get(ctx, tx, viewer, itemID)
	if err != nil {
		return StoreLink{}, err
	}
	if err := tx.Commit(); err != nil {
		return StoreLink{}, fmt.Errorf("commit: %w", err)
	}
	link, _ := findStoreLink(after, linkID)
	return link, nil
}

// DeleteStoreLink removes a store link from an item that the viewer can change.
// The item stays, with the price it has. It returns [ErrNotFound],
// [list.ErrNotEditable] and [ErrStoreLinkNotFound].
func (s *Service) DeleteStoreLink(ctx context.Context, viewer user.User, itemID, linkID int64, guard StoreLinkGuard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	it, err := editableItem(ctx, tx, viewer, itemID)
	if err != nil {
		return err
	}
	cur, ok := findStoreLink(it, linkID)
	if !ok {
		return ErrStoreLinkNotFound
	}
	if guard != nil {
		if err := guard(ctx, tx, cur); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM store_links WHERE id = ? AND item_id = ?`, linkID, itemID); err != nil {
		return fmt.Errorf("delete store link: %w", err)
	}
	removed := giftflag.Change{Field: giftflag.FieldStoreLinkRemoved, Label: cur.URL, Old: giftflag.Value(cur.URL), New: giftflag.Value(nil)}
	if err := giftflag.ItemChanged(ctx, tx, itemID, []giftflag.Change{removed}, s.now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ReadPrice is the price that a refresh read for one store link.
type ReadPrice struct {
	LinkID     int64
	PriceMinor int64
	Currency   string
}

// SavePrices saves the prices a refresh read, each with the time it was
// captured, on an item that the viewer can change, and returns the item. It
// never changes the item's own price, its title or its images. A link that is
// gone since the refresh began is skipped. It returns [ErrNotFound] and
// [list.ErrNotEditable].
func (s *Service) SavePrices(ctx context.Context, viewer user.User, itemID int64, reads []ReadPrice) (Item, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	before, err := editableItem(ctx, tx, viewer, itemID)
	if err != nil {
		return Item{}, err
	}
	now := s.now()
	var prices []storeLinkPrice
	for _, r := range reads {
		if l, ok := findStoreLink(before, r.LinkID); ok {
			currency := r.Currency
			prices = append(prices, storeLinkPrice{l, &r.PriceMinor, &currency})
		}
	}
	if err := flagStoreLinkPrices(ctx, tx, itemID, now, prices); err != nil {
		return Item{}, err
	}
	for _, r := range reads {
		if _, err := tx.ExecContext(ctx, `UPDATE store_links SET price_minor = ?, currency = ?, captured_at = ? WHERE id = ? AND item_id = ?`,
			r.PriceMinor, r.Currency, now, r.LinkID, itemID); err != nil {
			return Item{}, fmt.Errorf("save price: %w", err)
		}
	}
	it, err := get(ctx, tx, viewer, itemID)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit: %w", err)
	}
	return it, nil
}
