package gift

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/user"
)

// This file is restore, "I didn't get this" (docs/spec.md, Gifts, Restore;
// ADR 0011). A gift's recipients, or their parents, say that a given gift wasn't
// received, and the gift opens again for its giver:
//
//   - the gift is open: `given_at` and the recipients it was given to are cleared,
//     and it is hidden again from everyone but its giver (see [VisibleTo]);
//   - it keeps the copy from its reveal (title, price, image, occasion name) until
//     the next reveal, which copies again and lets go of the old image;
//   - its occasion is the next upcoming one ([Service.nextOccasions]), and it
//     merges into the giver's open gift on the item for that occasion, if there is
//     one;
//   - it is flagged restored, and removed too when the item or its list is gone;
//   - the quantity goes back on the item ([item.Restored]), claimed by the gift.
//
// A gift whose giver is deleted or deactivated, or is the only recipient of the
// list, can't be opened again: it is deleted, and the quantity goes back on the
// item unclaimed.

// Restore says that a given gift that the viewer receives wasn't received. It
// returns [ErrNotFound] for a gift the viewer can't see, [ErrNotRestorable]
// for one they see and don't receive: an open gift, or one they gave, and
// [ErrUserDeactivated] for a parent whose child receives it while deactivated.
func (s *Service) Restore(ctx context.Context, viewer user.User, id int64) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getIn(ctx, tx, viewer, id)
	if err != nil {
		return err
	}
	if !current.CanRestore {
		return notReceived(ctx, tx, viewer, current, ErrNotRestorable)
	}
	var row struct {
		ItemID  int64         `db:"item_id"`
		GiverID sql.NullInt64 `db:"giver_id"`
		ListID  int64         `db:"list_id"`
	}
	if err := tx.GetContext(ctx, &row, `SELECT gifts.item_id, gifts.giver_id, items.list_id
		FROM gifts JOIN items ON items.id = gifts.item_id WHERE gifts.id = ?`, id); err != nil {
		return fmt.Errorf("find the gift to restore: %w", err)
	}

	// The recipients the gift would be for now, and whether the giver still gives.
	recipients, err := list.RecipientIDs(ctx, tx, row.ListID)
	if err != nil {
		return err
	}
	kept := len(recipients) == 0
	if kept {
		// A list that was deleted with its gifts kept: the gift is for its last recipients.
		if err := tx.SelectContext(ctx, &recipients, `SELECT user_id FROM kept_list_recipients WHERE list_id = ? ORDER BY user_id`, row.ListID); err != nil {
			return fmt.Errorf("find the last recipients of a deleted list: %w", err)
		}
	}
	var giver user.User
	gone := !row.GiverID.Valid
	if !gone {
		if giver, err = user.Get(ctx, tx, row.GiverID.Int64); err != nil {
			return fmt.Errorf("find the giver of a gift: %w", err)
		}
		gone = !giver.Active()
	}
	others := slices.DeleteFunc(slices.Clone(recipients), func(r int64) bool { return row.GiverID.Valid && r == row.GiverID.Int64 })
	released := gone || (len(others) == 0 && !kept)

	there, err := item.Restored(ctx, tx, row.ItemID, current.Quantity)
	if err != nil {
		return err
	}
	now := s.now()
	if released {
		// Nobody can have it back: it is deleted, and the quantity is unclaimed.
		if err := s.deleteGift(ctx, tx, id); err != nil {
			return err
		}
		if err := item.PurgeIfUnused(ctx, tx, row.ItemID, now); err != nil {
			return err
		}
		return commit(tx)
	}

	// The flags first, because a merge adds them to the gift that takes this one in.
	if err := giftflag.Restore(ctx, tx, id, viewer.ID, now); err != nil {
		return err
	}
	if !there {
		if err := giftflag.Raise(ctx, tx, id, giftflag.Removed, now); err != nil {
			return err
		}
	}
	// Open again, for the next occasion by the rule of an open gift, or merged into
	// the giver's gift for it. Nothing is flagged occasion_moved: the restore is the
	// news. The copy stays; the recipients it was given to go.
	sees, err := hasGivable(ctx, tx, giver, row.ListID)
	if err != nil {
		return err
	}
	options, err := s.nextOccasions(ctx, tx, giver, row.ListID, others, sees)
	if err != nil {
		return err
	}
	if len(options) > 0 {
		if err := s.moveOccasion(ctx, tx, settleRow{ID: id, ItemID: row.ItemID, GiverID: giver.ID, ListID: row.ListID}, options[0], true); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE gifts SET given_at = NULL, giver_saw_list = NULL, giver_saw_occasion = NULL WHERE id = ?`, id); err != nil {
		// Every user has a birthday, so this does not happen.
		return fmt.Errorf("open a gift again: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gift_recipients WHERE gift_id = ?`, id); err != nil {
		return fmt.Errorf("forget who a gift was given to: %w", err)
	}
	// A giver who sees none of the recipients is flagged, as for any open gift.
	if err := s.settle(ctx, tx, scope{where: `gifts.id = ?`, args: []any{id}}); err != nil {
		return err
	}
	return commit(tx)
}

// Source is what Add again copies from a given gift: its title, its price and the
// copy of the image (docs/spec.md, Gifts, Add again). It is the copy from the
// reveal, so it doesn't depend on what the item is now, or whether it exists.
type Source struct {
	Title         string
	PriceMinor    *int64
	TopPriceMinor *int64
	Currency      *string
	// ImageID is the gift's copy of the image, or nil when it has none. The caller
	// copies it as a new image.
	ImageID *int64
}

// AddAgainSource returns what a new item copies from a given gift that the
// viewer receives. It returns [ErrNotFound] for a gift the viewer can't see, which
// is also a gift that doesn't exist or isn't given yet, and
// [ErrAddAgainNotAllowed] for one they see and don't receive: the gift they gave.
// The answer depends on the gift only, so it says nothing of its item.
func (s *Service) AddAgainSource(ctx context.Context, viewer user.User, id int64) (Source, error) {
	g, err := s.getIn(ctx, s.db, viewer, id)
	if err != nil {
		return Source{}, err
	}
	if !g.CanRestore {
		return Source{}, notReceived(ctx, s.db, viewer, g, ErrAddAgainNotAllowed)
	}
	src := Source{Title: g.Title, PriceMinor: g.PriceMinor, TopPriceMinor: g.TopPriceMinor, Currency: g.Currency}
	if g.Image != nil {
		src.ImageID = &g.Image.ID
	}
	return src, nil
}

func commit(tx *sqlx.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
