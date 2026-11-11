package gift

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

// New is the input for [Service.Claim].
type New struct {
	// Quantity is how many the giver will give. Zero means 1.
	Quantity int
	// OccasionID is the occasion, or nil for the first one that the giver may pick.
	OccasionID *int64
	FromLabel  string // empty for none
	Note       string // empty for none
	IsBought   bool
}

// Changes is a merge patch to a gift.
type Changes struct {
	Quantity   patch.Optional[int]
	OccasionID patch.Optional[int64]
	FromLabel  patch.Optional[string]
	Note       patch.Optional[string]
	IsBought   patch.Optional[bool]
}

// Guard runs inside the transaction of [Service.Update] and [Service.Delete],
// before the write, with the gift as the viewer sees it. An error from it stops
// the write.
type Guard func(ctx context.Context, q sqlx.ExtContext, current Gift) error

// itemRow is the part of an item that a claim checks.
type itemRow struct {
	ID          int64 `db:"id"`
	ListID      int64 `db:"list_id"`
	Quantity    int   `db:"quantity"`
	IsUnlimited bool  `db:"is_unlimited"`
	IsRecipient bool  `db:"is_recipient"`
	Claimed     int   `db:"claimed"`
}

// loadItem loads an item on a list that the viewer can see, and that was not
// deleted. It does not hide a fully claimed item: the role that claims is a user
// who can see the list and isn't one of its recipients, and a claim that loses
// to another is a 409, not a 404. q is a database or a transaction.
func loadItem(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (itemRow, error) {
	seen, args := list.VisibleTo(viewer)
	var r itemRow
	err := sqlx.GetContext(ctx, q, &r, `SELECT items.id, items.list_id, items.quantity, items.is_unlimited,
		items.list_id IN (SELECT list_id FROM list_recipients WHERE user_id = ?) AS is_recipient,
		COALESCE((SELECT SUM(g.quantity) FROM gifts g WHERE g.item_id = items.id AND g.given_at IS NULL), 0) AS claimed
		FROM items JOIN lists ON lists.id = items.list_id
		WHERE items.id = ? AND items.deleted_at IS NULL AND `+seen, append([]any{viewer.ID, id}, args...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return itemRow{}, ErrNotFound
	}
	if err != nil {
		return itemRow{}, fmt.Errorf("load item: %w", err)
	}
	return r, nil
}

// left is how many more can be claimed, with the claims as they are.
func (r itemRow) left() int { return max(r.Quantity-r.Claimed, 0) }

// Claim starts a gift: the viewer claims some of an item for an occasion of its
// list's recipients (docs/spec.md, Gifts). It returns [ErrNotFound] for an
// item the viewer can't see, [ErrRecipientCannotClaim], [ErrUserDeactivated],
// [ErrOccasionNotAllowed], [ErrAlreadyClaimed], [ErrFullyClaimed], and
// [ErrInvalid].
//
// The check of how many are still wanted and the insert run in one transaction.
// The database lets one writer at a time, so two givers who claim the last one at
// once are serialized: the first wins, and the second gets [ErrFullyClaimed].
func (s *Service) Claim(ctx context.Context, viewer user.User, itemID int64, n New) (Gift, error) {
	quantity := n.Quantity
	if quantity == 0 {
		quantity = 1
	}
	if quantity < 1 {
		return Gift{}, invalid("quantity", "the quantity must be at least 1")
	}
	label, err := cleanFromLabel(n.FromLabel)
	if err != nil {
		return Gift{}, err
	}
	note, err := cleanNote(n.Note)
	if err != nil {
		return Gift{}, err
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Gift{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	it, err := loadItem(ctx, tx, viewer, itemID)
	if err != nil {
		return Gift{}, err
	}
	if it.IsRecipient {
		return Gift{}, ErrRecipientCannotClaim
	}
	pick, err := s.pick(ctx, tx, viewer, it.ListID, n.OccasionID, ErrUserDeactivated)
	if err != nil {
		return Gift{}, err
	}
	date := pick.NextDate.String()

	var taken bool
	if err := tx.GetContext(ctx, &taken, `SELECT EXISTS (SELECT 1 FROM gifts WHERE item_id = ? AND giver_id = ? AND occasion_id = ? AND given_at IS NULL)`,
		it.ID, viewer.ID, pick.ID); err != nil {
		return Gift{}, fmt.Errorf("find an open gift: %w", err)
	}
	if taken {
		return Gift{}, ErrAlreadyClaimed
	}
	if !it.IsUnlimited && quantity > it.left() {
		return Gift{}, ErrFullyClaimed
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO gifts (item_id, giver_id, quantity, occasion_id, occasion_date, from_label, note, is_bought, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, it.ID, viewer.ID, quantity, pick.ID, date, label, note, n.IsBought, s.now())
	if err != nil {
		return Gift{}, fmt.Errorf("insert gift: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Gift{}, fmt.Errorf("insert gift: %w", err)
	}
	g, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return Gift{}, err
	}
	if err := tx.Commit(); err != nil {
		return Gift{}, fmt.Errorf("commit: %w", err)
	}
	return g, nil
}

// hasGivable says whether the viewer has a recipient of the list to give to
// ([list.GivableRecipients]). q is a database or a transaction.
func hasGivable(ctx context.Context, q sqlx.QueryerContext, viewer user.User, listID int64) (bool, error) {
	givable, args := list.GivableRecipients(viewer, "?")
	var found bool
	if err := sqlx.GetContext(ctx, q, &found, `SELECT EXISTS (`+givable+`)`, append([]any{listID}, args...)...); err != nil {
		return false, fmt.Errorf("find the recipients to give to: %w", err)
	}
	return found, nil
}

// pick chooses the occasion of a gift on the list: the one with the ID, which
// must be one the giver may pick, or the first of them when id is nil. It returns
// noOne when the giver has no recipient to give to: they see none of the
// recipients other than themselves, or the ones they see are deactivated. A claim
// answers [ErrUserDeactivated] and a change of a gift's occasion answers
// [ErrOccasionLocked].
func (s *Service) pick(ctx context.Context, q sqlx.QueryerContext, viewer user.User, listID int64, id *int64, noOne error) (occasion.Occasion, error) {
	found, err := hasGivable(ctx, q, viewer, listID)
	if err != nil {
		return occasion.Occasion{}, err
	}
	if !found {
		return occasion.Occasion{}, noOne
	}
	options, err := s.occ.Pickable(ctx, q, viewer, listID)
	if err != nil {
		return occasion.Occasion{}, err
	}
	if id == nil {
		if len(options) == 0 {
			return occasion.Occasion{}, ErrOccasionNotAllowed
		}
		return options[0], nil
	}
	for _, o := range options {
		if o.ID == *id {
			return o, nil
		}
	}
	return occasion.Occasion{}, ErrOccasionNotAllowed
}

// validate trims and checks the fields that are set.
func (c Changes) validate() (Changes, error) {
	if c.Quantity.Set {
		if c.Quantity.Value == nil || *c.Quantity.Value < 1 {
			return c, invalid("quantity", "the quantity must be at least 1")
		}
	}
	if c.OccasionID.Set && c.OccasionID.Value == nil {
		return c, invalid("occasion_id", "a gift needs an occasion")
	}
	if c.IsBought.Set && c.IsBought.Value == nil {
		return c, invalid("is_bought", "is_bought is true or false")
	}
	if c.FromLabel.Set && c.FromLabel.Value != nil {
		label, err := cleanFromLabel(*c.FromLabel.Value)
		if err != nil {
			return c, err
		}
		c.FromLabel.Value = label
	}
	if c.Note.Set && c.Note.Value != nil {
		note, err := cleanNote(*c.Note.Value)
		if err != nil {
			return c, err
		}
		c.Note.Value = note
	}
	return c, nil
}

// Update applies a merge patch to an open gift of the viewer's, and returns the
// gift that is left. When the new occasion is one that the giver already has an
// open gift on the same item for, that gift takes this one in, and it is the one
// returned: the gift that moved is deleted, so its address is a 404 from then on
// (docs/spec.md, Gifts, Occasions for a gift).
//
// It returns [ErrNotFound] for a gift the viewer can't see, [ErrNotEditable] for
// one that is given, [ErrInvalid], [ErrOccasionNotAllowed],
// [ErrQuantityRaiseNotAllowed], and [ErrFullyClaimed]. guard runs inside the same
// transaction as the write: an If-Match check goes there.
func (s *Service) Update(ctx context.Context, viewer user.User, id int64, c Changes, guard Guard) (Gift, error) {
	c, err := c.validate()
	if err != nil {
		return Gift{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Gift{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return Gift{}, err
	}
	if !current.CanEdit {
		return Gift{}, ErrNotEditable
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return Gift{}, err
		}
	}
	it, err := loadItemForGift(ctx, tx, current.ItemID)
	if err != nil {
		return Gift{}, err
	}

	quantity, label, note, bought := current.Quantity, current.FromLabel, current.Note, current.IsBought
	if c.Quantity.Set {
		quantity = *c.Quantity.Value
	}
	if c.FromLabel.Set {
		label = c.FromLabel.Value
	}
	if c.Note.Set {
		note = c.Note.Value
	}
	if c.IsBought.Set {
		bought = *c.IsBought.Value
	}
	if quantity > current.Quantity {
		// A recipient never raises, whether or not more are still wanted, so the
		// refusal shows nothing of the claim state.
		var recipient bool
		if err := tx.GetContext(ctx, &recipient, `SELECT EXISTS (SELECT 1 FROM list_recipients WHERE list_id = ? AND user_id = ?)`, it.ListID, viewer.ID); err != nil {
			return Gift{}, fmt.Errorf("find a recipient: %w", err)
		}
		if recipient {
			return Gift{}, ErrQuantityRaiseNotAllowed
		}
		if !it.IsUnlimited && quantity-current.Quantity > it.left() {
			return Gift{}, ErrFullyClaimed
		}
	}

	occasionID, occasionDate := current.occasionID, current.OccasionDate.String()
	if c.OccasionID.Set {
		// A giver who sees none of the recipients can't pick, and the answer is the
		// same whichever occasion they name, even the one the gift has.
		found, err := hasGivable(ctx, tx, viewer, it.ListID)
		if err != nil {
			return Gift{}, err
		}
		if !found {
			return Gift{}, ErrOccasionLocked
		}
	}
	if c.OccasionID.Set && (!current.occasionID.Valid || *c.OccasionID.Value != current.occasionID.Int64) {
		pick, err := s.pick(ctx, tx, viewer, it.ListID, c.OccasionID.Value, ErrOccasionLocked)
		if err != nil {
			return Gift{}, err
		}
		var staying struct {
			ID        int64          `db:"id"`
			Quantity  int            `db:"quantity"`
			FromLabel sql.NullString `db:"from_label"`
			Note      sql.NullString `db:"note"`
		}
		err = tx.GetContext(ctx, &staying, `SELECT id, quantity, from_label, note FROM gifts
			WHERE item_id = ? AND giver_id = ? AND occasion_id = ? AND given_at IS NULL AND id <> ?`, current.ItemID, viewer.ID, pick.ID, id)
		switch {
		case err == nil:
			merged, err := s.merge(ctx, tx, staying.ID, id, quantity, label, note, staying.Quantity, staying.FromLabel, staying.Note)
			if err != nil {
				return Gift{}, err
			}
			g, err := s.getIn(ctx, tx, viewer, merged)
			if err != nil {
				return Gift{}, err
			}
			if err := tx.Commit(); err != nil {
				return Gift{}, fmt.Errorf("commit: %w", err)
			}
			return g, nil
		case !errors.Is(err, sql.ErrNoRows):
			return Gift{}, fmt.Errorf("find the gift that takes this one in: %w", err)
		}
		occasionID, occasionDate = sql.NullInt64{Int64: pick.ID, Valid: true}, pick.NextDate.String()
	}

	if _, err := tx.ExecContext(ctx, `UPDATE gifts SET quantity = ?, from_label = ?, note = ?, is_bought = ?, occasion_id = ?, occasion_date = ? WHERE id = ?`,
		quantity, label, note, bought, occasionID, occasionDate, id); err != nil {
		return Gift{}, fmt.Errorf("update gift: %w", err)
	}
	g, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return Gift{}, err
	}
	if err := tx.Commit(); err != nil {
		return Gift{}, fmt.Errorf("commit: %w", err)
	}
	return g, nil
}

// merge adds the gift that moves into the gift that stays, and deletes the one
// that moved. The staying gift keeps its ID, its bought mark and its from label,
// unless it has none, and then it takes the moving gift's. The quantities add up.
// A note of the moving gift goes at the end of the staying gift's, on a new line.
func (s *Service) merge(ctx context.Context, tx *sqlx.Tx, stayingID, movingID int64, quantity int, label, note *string,
	stayingQuantity int, stayingLabel, stayingNote sql.NullString) (int64, error) {
	if !stayingLabel.Valid {
		stayingLabel = sql.NullString{String: deref(label), Valid: label != nil}
	}
	if note != nil {
		joined := *note
		if stayingNote.Valid {
			joined = stayingNote.String + "\n" + *note
		}
		stayingNote = sql.NullString{String: joined, Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gifts SET quantity = ?, from_label = ?, note = ? WHERE id = ?`,
		stayingQuantity+quantity, stayingLabel, stayingNote, stayingID); err != nil {
		return 0, fmt.Errorf("merge gifts: %w", err)
	}
	// The moving gift's flags are added to the staying gift's flags.
	if err := giftflag.Merge(ctx, tx, stayingID, movingID); err != nil {
		return 0, err
	}
	if err := s.deleteGift(ctx, tx, movingID); err != nil {
		return 0, fmt.Errorf("merge gifts: %w", err)
	}
	return stayingID, nil
}

// deleteGift deletes a gift in the transaction of the change that ends it: an
// unclaim, a merge, a release, a restore of a gift whose giver is gone. It lets go
// of the copy of an image that an earlier reveal made, so the sweep deletes it
// (docs/spec.md, Gifts, Occasions for a gift: the moving gift is deleted "with
// any copy from an earlier reveal"). It doesn't purge the item.
func (s *Service) deleteGift(ctx context.Context, tx *sqlx.Tx, id int64) error {
	var held sql.NullInt64
	if err := tx.GetContext(ctx, &held, `SELECT image_id FROM gifts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("find the copy of a gift: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gifts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete gift: %w", err)
	}
	if held.Valid {
		return image.Detach(ctx, tx, s.now(), held.Int64)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// loadItemForGift loads the item of a gift, also when it was deleted softly.
func loadItemForGift(ctx context.Context, q sqlx.QueryerContext, id int64) (itemRow, error) {
	var r itemRow
	err := sqlx.GetContext(ctx, q, &r, `SELECT items.id, items.list_id, items.quantity, items.is_unlimited, 0 AS is_recipient,
		COALESCE((SELECT SUM(g.quantity) FROM gifts g WHERE g.item_id = items.id AND g.given_at IS NULL), 0) AS claimed
		FROM items WHERE items.id = ?`, id)
	if err != nil {
		return itemRow{}, fmt.Errorf("load the item of a gift: %w", err)
	}
	return r, nil
}

// Delete unclaims an open gift of the viewer's: the quantity goes back to all
// givers. It returns [ErrNotFound] for a gift the viewer can't see and [ErrGiven]
// for one that is given. guard runs inside the same transaction as the write.
func (s *Service) Delete(ctx context.Context, viewer user.User, id int64, guard Guard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return err
	}
	if current.State == StateGiven {
		return ErrGiven
	}
	if !current.CanEdit {
		return ErrNotEditable
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return err
		}
	}
	if err := s.deleteGift(ctx, tx, id); err != nil {
		return err
	}
	// An item deleted while it had this gift goes for good with the last gift.
	if err := item.PurgeIfUnused(ctx, tx, current.ItemID, s.now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Acknowledge clears flags on an open gift of the viewer's, and returns the gift
// that is left. codes names the flags to clear, and nil means every flag (an empty
// slice clears none). A code
// the gift doesn't have is skipped. Keeping a gift whose item was deleted is
// acknowledging removed. It returns [ErrNotFound] for a gift the viewer can't
// see, [ErrNotEditable] for one that is given, and [ErrInvalid] for a code that
// isn't a flag's.
func (s *Service) Acknowledge(ctx context.Context, viewer user.User, id int64, codes []string) (Gift, error) {
	for _, c := range codes {
		if !giftflag.Known(c) {
			return Gift{}, invalid("codes", "%q is not a flag", c)
		}
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Gift{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return Gift{}, err
	}
	if !current.CanEdit {
		return Gift{}, ErrNotEditable
	}
	if codes == nil {
		for _, f := range current.Flags {
			codes = append(codes, f.Code)
		}
	}
	if err := giftflag.Clear(ctx, tx, id, codes); err != nil {
		return Gift{}, err
	}
	g, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return Gift{}, err
	}
	if err := tx.Commit(); err != nil {
		return Gift{}, fmt.Errorf("commit: %w", err)
	}
	return g, nil
}
