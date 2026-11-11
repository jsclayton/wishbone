package item

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
)

// This file turns what a user who can edit the list does to an item into the
// diff behind the changed flag of the item's open gifts (docs/spec.md, Items,
// Who changes an item; Gifts, Changes while a gift is open). The rules:
//
//   - editing the title, notes, quantity, unlimited flag or price flags;
//   - setting the item price for the first time, from no price, does not flag;
//   - changing a picture other than the primary image does not flag;
//   - removing a store link, or changing its price, flags;
//   - adding a store link, setting a store link's price for the first time, or a
//     refresh with no price change does not flag.
//
// The flags are written in the transaction of the change, by [giftflag].

// diffPrice is a price as a diff shows it.
type diffPrice struct {
	PriceMinor    *int64 `json:"price_minor"`
	TopPriceMinor *int64 `json:"top_price_minor"`
	Currency      string `json:"currency"`
}

// priceJSON is the diff value of a price, and null when there is none.
func priceJSON(minor, top *int64, currency *string) json.RawMessage {
	if minor == nil || currency == nil {
		return giftflag.Value(nil)
	}
	return giftflag.Value(diffPrice{PriceMinor: minor, TopPriceMinor: top, Currency: *currency})
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func equalPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// editChanges returns the lines of the diff for an edit of the item's own fields.
// hadPrice says that the item has had a price once (price_cleared): without it, a
// price that appears is the item's first and is not a change to the gift.
func editChanges(before Item, after fields, hadPrice bool) []giftflag.Change {
	var out []giftflag.Change
	if before.Title != after.title {
		out = append(out, giftflag.Change{Field: giftflag.FieldTitle, Old: giftflag.Value(before.Title), New: giftflag.Value(after.title)})
	}
	if !equalPtr(before.Notes, after.notes) {
		out = append(out, giftflag.Change{Field: giftflag.FieldNotes, Old: giftflag.Value(deref(before.Notes)), New: giftflag.Value(deref(after.notes))})
	}
	if before.Quantity != after.quantity {
		out = append(out, giftflag.Change{Field: giftflag.FieldQuantity, Old: giftflag.Value(before.Quantity), New: giftflag.Value(after.quantity)})
	}
	if before.IsUnlimited != after.isUnlimited {
		out = append(out, giftflag.Change{Field: giftflag.FieldIsUnlimited, Old: giftflag.Value(before.IsUnlimited), New: giftflag.Value(after.isUnlimited)})
	}
	oldPrice := priceJSON(before.PriceMinor, before.TopPriceMinor, before.Currency)
	newPrice := priceJSON(after.priceMinor, after.topPriceMinor, after.currency)
	if string(oldPrice) != string(newPrice) && (before.PriceMinor != nil || hadPrice) {
		out = append(out, giftflag.Change{Field: giftflag.FieldPrice, Old: oldPrice, New: newPrice})
	}
	return out
}

// hadPrice says whether the item has had a price once: it has one, or it had one
// that a change cleared.
func hadPrice(ctx context.Context, tx *sqlx.Tx, itemID int64) (bool, error) {
	var cleared bool
	if err := tx.GetContext(ctx, &cleared, `SELECT price_cleared FROM items WHERE id = ?`, itemID); err != nil {
		return false, fmt.Errorf("read price_cleared: %w", err)
	}
	return cleared, nil
}

// storeLinkPriceChange is the diff line for a store link whose price becomes the
// new one, or nil when it flags nothing: the price stays, or the link has never
// had a price (a captured time means it had one once: a refresh writes one only
// with a price, and a hand edit writes one even when it clears the price).
func storeLinkPriceChange(l StoreLink, minor *int64, currency *string) *giftflag.Change {
	if l.PriceMinor == nil && l.CapturedAt == nil {
		return nil
	}
	oldPrice := priceJSON(l.PriceMinor, nil, l.Currency)
	newPrice := priceJSON(minor, nil, currency)
	if string(oldPrice) == string(newPrice) {
		return nil
	}
	return &giftflag.Change{Field: giftflag.FieldStoreLinkPrice, Label: l.URL, Old: oldPrice, New: newPrice}
}

// storeLinkPrice is a store link and the price it is getting.
type storeLinkPrice struct {
	link     StoreLink
	minor    *int64
	currency *string
}

// flagStoreLinkPrices reports the price changes of store links to the item's open
// gifts. Call it in the transaction of the change, with the links as they were
// before it.
func flagStoreLinkPrices(ctx context.Context, tx *sqlx.Tx, itemID int64, now time.Time, prices []storeLinkPrice) error {
	var changes []giftflag.Change
	for _, p := range prices {
		if c := storeLinkPriceChange(p.link, p.minor, p.currency); c != nil {
			changes = append(changes, *c)
		}
	}
	return giftflag.ItemChanged(ctx, tx, itemID, changes, now)
}
