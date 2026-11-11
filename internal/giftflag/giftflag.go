// Package giftflag owns the flags on a giver's open gift and the diff behind the
// changed flag (docs/spec.md, Gifts, Changes while a gift is open; docs/routes.md,
// Gift fields). A flag says that something happened to a gift that its giver
// should answer. Flags are the giver's only: no code here is reached by a
// recipient.
//
// The package depends on no other package of the app, so both internal/item
// (which sets flags when it changes, moves or deletes an item) and internal/gift
// (which shows and clears them) can import it. Every function that writes takes
// the transaction of the change, so the change and its flags commit together.
//
// A reveal is final. Every write here goes through [openGifts], so a gift that is
// given is never flagged.
package giftflag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"
)

// The codes of the flags (docs/routes.md, Gift fields).
const (
	Changed           = "changed"
	Removed           = "removed"
	Moved             = "moved"
	OccasionMoved     = "occasion_moved"
	NoLongerShared    = "no_longer_shared"
	Restored          = "restored"
	RecipientsChanged = "recipients_changed"
)

// Codes lists every code, in the order the documentation names them.
var Codes = []string{Changed, Removed, Moved, OccasionMoved, NoLongerShared, Restored, RecipientsChanged}

// Known says whether the code is one of [Codes].
func Known(code string) bool { return slices.Contains(Codes, code) }

// The fields of a changed flag's diff.
const (
	FieldTitle        = "title"
	FieldNotes        = "notes"
	FieldQuantity     = "quantity"
	FieldIsUnlimited  = "is_unlimited"
	FieldPrice        = "price"
	FieldPrimaryImage = "primary_image"
	// FieldStoreLinkPrice and FieldStoreLinkRemoved name a store link by its URL
	// in [Change.Label].
	FieldStoreLinkPrice   = "store_link_price"
	FieldStoreLinkRemoved = "store_link_removed"
)

// openGifts is the condition for a gift that has not been given.
const openGifts = "given_at IS NULL"

// Change is one line of the diff behind a changed flag. Old and New are JSON.
type Change struct {
	Field string
	// Label names the store link, by its URL, for a store link's line. It is
	// empty otherwise.
	Label    string
	Old, New json.RawMessage
}

// Value turns a value into the JSON of a change's Old or New. A nil value is null.
func Value(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("giftflag: a diff value can't be encoded: %v", err))
	}
	return b
}

// Flag is one flag on a gift.
type Flag struct {
	Code string
	// Changes is the diff of a changed flag.
	Changes []Change
	// ListID is the new list of a moved flag.
	ListID int64
	// OldRecipients and NewRecipients are the user IDs of a recipients_changed
	// flag: the recipients the giver last saw, and the ones now.
	OldRecipients, NewRecipients []int64
	// By is the user who restored the gift, for a restored flag.
	By int64
}

// Raise sets a flag that has no data of its own (occasion_moved or
// no_longer_shared) on an open gift. A flag the gift already has stays.
func Raise(ctx context.Context, tx *sqlx.Tx, giftID int64, code string, now time.Time) error {
	return raise(ctx, tx, giftID, code, nil, now)
}

// Restore raises the restored flag on an open gift: the gift was given, and the
// user (a recipient, or a parent of one) said that it wasn't received. A flag the
// gift already has is replaced, so it names the latest user.
func Restore(ctx context.Context, tx *sqlx.Tx, giftID, by int64, now time.Time) error {
	return raise(ctx, tx, giftID, Restored, map[string]int64{"by": by}, now)
}

// ClearRemoved clears the removed flag from every open gift on an item. An item
// that comes back to its list, because a restore put its quantity back, is no
// longer removed.
func ClearRemoved(ctx context.Context, tx *sqlx.Tx, itemID int64) error {
	ids, err := openGiftIDs(ctx, tx, itemID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := Clear(ctx, tx, id, []string{Removed}); err != nil {
			return err
		}
	}
	return nil
}

type recipientsData struct {
	Old []int64 `json:"old"`
	New []int64 `json:"new"`
}

func sameIDs(a, b []int64) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

// ChangeRecipients records that the recipients of an open gift changed from
// oldIDs to newIDs, as the IDs of the users it is for. The flag keeps the first old
// recipients, like a changed flag keeps the first old value, so it always reads
// "since you last looked". If the recipients are the ones the giver last saw, the
// flag goes away, or is not raised.
func ChangeRecipients(ctx context.Context, tx *sqlx.Tx, giftID int64, oldIDs, newIDs []int64, now time.Time) error {
	var raw sql.NullString
	err := tx.GetContext(ctx, &raw, `SELECT data FROM gift_flags WHERE gift_id = ? AND code = ?`, giftID, RecipientsChanged)
	switch {
	case err == nil && raw.Valid:
		var d recipientsData
		if err := json.Unmarshal([]byte(raw.String), &d); err != nil {
			return fmt.Errorf("read a recipients_changed flag: %w", err)
		}
		oldIDs = d.Old
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("find a recipients_changed flag: %w", err)
	}
	if sameIDs(oldIDs, newIDs) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gift_flags WHERE gift_id = ? AND code = ?`, giftID, RecipientsChanged); err != nil {
			return fmt.Errorf("clear a recipients_changed flag: %w", err)
		}
		return nil
	}
	return raise(ctx, tx, giftID, RecipientsChanged, recipientsData{Old: oldIDs, New: newIDs}, now)
}

// ForgetUser takes a user who is about to be deleted out of the flags that name
// them (docs/spec.md, Deactivation and deletion, Deletion): a recipients_changed
// flag drops them from its old and new recipients, and a restored flag that they
// raised has no user. A flag stays when it loses the user, because its giver still
// has something to answer: the recipients changed, and the gift was restored.
func ForgetUser(ctx context.Context, tx *sqlx.Tx, userID int64) error {
	var rows []struct {
		ID   int64  `db:"id"`
		Code string `db:"code"`
		Data string `db:"data"`
	}
	if err := tx.SelectContext(ctx, &rows, `SELECT id, code, data FROM gift_flags WHERE code IN (?, ?) AND data IS NOT NULL`,
		RecipientsChanged, Restored); err != nil {
		return fmt.Errorf("find the flags that name a user: %w", err)
	}
	for _, r := range rows {
		switch r.Code {
		case RecipientsChanged:
			var d recipientsData
			if err := json.Unmarshal([]byte(r.Data), &d); err != nil {
				return fmt.Errorf("read a recipients_changed flag: %w", err)
			}
			if !slices.Contains(d.Old, userID) && !slices.Contains(d.New, userID) {
				continue
			}
			d.Old = slices.DeleteFunc(d.Old, func(id int64) bool { return id == userID })
			d.New = slices.DeleteFunc(d.New, func(id int64) bool { return id == userID })
			b, err := json.Marshal(d)
			if err != nil {
				return fmt.Errorf("encode a flag: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE gift_flags SET data = ? WHERE id = ?`, string(b), r.ID); err != nil {
				return fmt.Errorf("drop a user from a recipients_changed flag: %w", err)
			}
		case Restored:
			var d struct {
				By int64 `json:"by"`
			}
			if err := json.Unmarshal([]byte(r.Data), &d); err != nil {
				return fmt.Errorf("read a restored flag: %w", err)
			}
			if d.By != userID {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE gift_flags SET data = NULL WHERE id = ?`, r.ID); err != nil {
				return fmt.Errorf("drop a user from a restored flag: %w", err)
			}
		}
	}
	return nil
}

// openGiftIDs returns the open gifts on the item, in order.
func openGiftIDs(ctx context.Context, tx *sqlx.Tx, itemID int64) ([]int64, error) {
	var ids []int64
	if err := tx.SelectContext(ctx, &ids, `SELECT id FROM gifts WHERE item_id = ? AND `+openGifts+` ORDER BY id`, itemID); err != nil {
		return nil, fmt.Errorf("find the open gifts of an item: %w", err)
	}
	return ids, nil
}

// raise sets a flag on a gift. A flag it already has is replaced, so data is
// the latest.
func raise(ctx context.Context, tx *sqlx.Tx, giftID int64, code string, data any, now time.Time) error {
	var raw sql.NullString
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("encode a flag: %w", err)
		}
		raw = sql.NullString{String: string(b), Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gift_flags (gift_id, code, data, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (gift_id, code) DO UPDATE SET data = excluded.data`, giftID, code, raw, now); err != nil {
		return fmt.Errorf("flag a gift: %w", err)
	}
	return nil
}

// ItemRemoved flags every open gift on a deleted item removed. The giver keeps
// the gift or unclaims it.
func ItemRemoved(ctx context.Context, tx *sqlx.Tx, itemID int64, now time.Time) error {
	ids, err := openGiftIDs(ctx, tx, itemID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := raise(ctx, tx, id, Removed, nil, now); err != nil {
			return err
		}
	}
	return nil
}

// ItemMoved flags every open gift on an item that moved to another list moved.
// The flag holds the new list, which the reader shows only to a giver who can
// see it.
func ItemMoved(ctx context.Context, tx *sqlx.Tx, itemID, listID int64, now time.Time) error {
	ids, err := openGiftIDs(ctx, tx, itemID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := raise(ctx, tx, id, Moved, map[string]int64{"list_id": listID}, now); err != nil {
			return err
		}
	}
	return nil
}

// ItemChanged adds the changes to the diff of every open gift on the item, and
// raises each gift's changed flag. A field that changes again keeps its first old
// value, so the diff always reads "since you picked it". If it changes back, the
// line goes away, and so does the flag when nothing else is left.
func ItemChanged(ctx context.Context, tx *sqlx.Tx, itemID int64, changes []Change, now time.Time) error {
	if len(changes) == 0 {
		return nil
	}
	ids, err := openGiftIDs(ctx, tx, itemID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, c := range changes {
			if err := record(ctx, tx, id, c); err != nil {
				return err
			}
		}
		if err := syncChanged(ctx, tx, id, now); err != nil {
			return err
		}
	}
	return nil
}

func record(ctx context.Context, tx *sqlx.Tx, giftID int64, c Change) error {
	if c.Field == FieldStoreLinkRemoved {
		// A removed link is a line of its own, and can't change back.
		_, err := tx.ExecContext(ctx, `INSERT INTO gift_changes (gift_id, field, label, old_value, new_value) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (gift_id, field, label) DO NOTHING`, giftID, c.Field, c.Label, string(c.Old), string(c.New))
		return err
	}
	var prev struct {
		ID  int64  `db:"id"`
		Old string `db:"old_value"`
	}
	err := tx.GetContext(ctx, &prev, `SELECT id, old_value FROM gift_changes WHERE gift_id = ? AND field = ? AND label = ?`, giftID, c.Field, c.Label)
	switch {
	case err == nil && prev.Old == string(c.New):
		_, err = tx.ExecContext(ctx, `DELETE FROM gift_changes WHERE id = ?`, prev.ID)
		return err
	case err == nil:
		_, err = tx.ExecContext(ctx, `UPDATE gift_changes SET new_value = ? WHERE id = ?`, string(c.New), prev.ID)
		return err
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gift_changes (gift_id, field, label, old_value, new_value) VALUES (?, ?, ?, ?, ?)`,
		giftID, c.Field, c.Label, string(c.Old), string(c.New))
	return err
}

// syncChanged keeps the changed flag in step with the diff: it exists exactly
// while the gift has lines.
func syncChanged(ctx context.Context, tx *sqlx.Tx, giftID int64, now time.Time) error {
	var lines bool
	if err := tx.GetContext(ctx, &lines, `SELECT EXISTS (SELECT 1 FROM gift_changes WHERE gift_id = ?)`, giftID); err != nil {
		return err
	}
	if lines {
		_, err := tx.ExecContext(ctx, `INSERT INTO gift_flags (gift_id, code, created_at) VALUES (?, ?, ?)
			ON CONFLICT (gift_id, code) DO NOTHING`, giftID, Changed, now)
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM gift_flags WHERE gift_id = ? AND code = ?`, giftID, Changed)
	return err
}

// Clear removes the flags with these codes from a gift. Clearing changed also
// drops the diff. A code the gift doesn't have is skipped.
func Clear(ctx context.Context, tx *sqlx.Tx, giftID int64, codes []string) error {
	if len(codes) == 0 {
		return nil
	}
	query, args, err := sqlx.In(`DELETE FROM gift_flags WHERE gift_id = ? AND code IN (?)`, giftID, codes)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("clear flags: %w", err)
	}
	if slices.Contains(codes, Changed) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gift_changes WHERE gift_id = ?`, giftID); err != nil {
			return fmt.Errorf("clear the diff: %w", err)
		}
	}
	return nil
}

// Merge adds the flags of the gift that moves to the flags of the gift that
// stays. The staying gift keeps a flag, or a diff line, that it already has.
func Merge(ctx context.Context, tx *sqlx.Tx, stayingID, movingID int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO gift_flags (gift_id, code, data, created_at)
		SELECT ?, code, data, created_at FROM gift_flags WHERE gift_id = ? ORDER BY id`, stayingID, movingID); err != nil {
		return fmt.Errorf("merge flags: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO gift_changes (gift_id, field, label, old_value, new_value)
		SELECT ?, field, label, old_value, new_value FROM gift_changes WHERE gift_id = ? ORDER BY id`, stayingID, movingID); err != nil {
		return fmt.Errorf("merge the diff: %w", err)
	}
	return nil
}

// Load returns the flags of the gifts, by gift ID, oldest first. A gift with
// none has no entry.
func Load(ctx context.Context, q sqlx.QueryerContext, giftIDs []int64) (map[int64][]Flag, error) {
	out := map[int64][]Flag{}
	if len(giftIDs) == 0 {
		return out, nil
	}
	query, args, err := sqlx.In(`SELECT gift_id, code, data FROM gift_flags WHERE gift_id IN (?) ORDER BY id`, giftIDs)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		GiftID int64          `db:"gift_id"`
		Code   string         `db:"code"`
		Data   sql.NullString `db:"data"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("load flags: %w", err)
	}
	changes, err := loadChanges(ctx, q, giftIDs)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		f := Flag{Code: r.Code}
		switch r.Code {
		case Changed:
			f.Changes = changes[r.GiftID]
		case Moved:
			var d struct {
				ListID int64 `json:"list_id"`
			}
			if r.Data.Valid {
				if err := json.Unmarshal([]byte(r.Data.String), &d); err != nil {
					return nil, fmt.Errorf("read a moved flag: %w", err)
				}
			}
			f.ListID = d.ListID
		case Restored:
			var d struct {
				By int64 `json:"by"`
			}
			if r.Data.Valid {
				if err := json.Unmarshal([]byte(r.Data.String), &d); err != nil {
					return nil, fmt.Errorf("read a restored flag: %w", err)
				}
			}
			f.By = d.By
		case RecipientsChanged:
			var d recipientsData
			if r.Data.Valid {
				if err := json.Unmarshal([]byte(r.Data.String), &d); err != nil {
					return nil, fmt.Errorf("read a recipients_changed flag: %w", err)
				}
			}
			f.OldRecipients, f.NewRecipients = d.Old, d.New
		}
		out[r.GiftID] = append(out[r.GiftID], f)
	}
	return out, nil
}

func loadChanges(ctx context.Context, q sqlx.QueryerContext, giftIDs []int64) (map[int64][]Change, error) {
	query, args, err := sqlx.In(`SELECT gift_id, field, label, old_value, new_value FROM gift_changes WHERE gift_id IN (?) ORDER BY id`, giftIDs)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		GiftID   int64  `db:"gift_id"`
		Field    string `db:"field"`
		Label    string `db:"label"`
		OldValue string `db:"old_value"`
		NewValue string `db:"new_value"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("load the diff: %w", err)
	}
	out := map[int64][]Change{}
	for _, r := range rows {
		out[r.GiftID] = append(out[r.GiftID], Change{Field: r.Field, Label: r.Label, Old: json.RawMessage(r.OldValue), New: json.RawMessage(r.NewValue)})
	}
	return out, nil
}

// NeedsAttention says whether the flags make an open gift need attention:
// changed or removed.
func NeedsAttention(flags []Flag) bool {
	return slices.ContainsFunc(flags, func(f Flag) bool { return f.Code == Changed || f.Code == Removed })
}
