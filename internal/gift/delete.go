package gift

import (
	"context"
	"fmt"
	"slices"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/item"
)

// This file is the gift side of deleting a user (docs/spec.md, Deactivation and
// deletion, Deletion). internal/userdelete runs the steps, in its order, inside one
// transaction, and each step here is a function of the transaction it is given.
// The rule that puts an open gift right when the user's lists and occasions change
// is still [Service.settle]: a step here that has to move or flag a gift calls it
// with a scope, and repeats none of it.

// ReleaseOf releases the open gifts that the user is giving, as an unclaim does
// (see releaseOf). The gifts they gave stay.
func (s *Service) ReleaseOf(ctx context.Context, tx *sqlx.Tx, giverID int64) error {
	return s.releaseOf(ctx, tx, giverID)
}

// KeepGiverName stores the name of a user who is about to be deleted on the gifts
// they gave, so Received still names them (docs/spec.md, Gifts, Reveal). A gift
// that is open is released instead, and keeps nothing.
func (s *Service) KeepGiverName(ctx context.Context, tx *sqlx.Tx, giverID int64, name string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE gifts SET giver_name = ? WHERE giver_id = ? AND given_at IS NOT NULL`, name, giverID); err != nil {
		return fmt.Errorf("keep the name of a giver: %w", err)
	}
	return nil
}

// DeleteGivenOnlyTo deletes the given gifts that the user alone received: nobody
// else could keep them, so they leave their giver's Given too. A given gift for the
// user and other recipients stays for the others, and the user leaves its
// recipients with their row (gift_recipients goes with the user). The quantity does
// not go back on the item: it left the item at reveal, and a quantity that came
// back would show whoever sees the list that the item had been given.
func (s *Service) DeleteGivenOnlyTo(ctx context.Context, tx *sqlx.Tx, userID int64) error {
	var gifts []struct {
		ID     int64 `db:"id"`
		ItemID int64 `db:"item_id"`
	}
	if err := tx.SelectContext(ctx, &gifts, `SELECT gifts.id, gifts.item_id FROM gifts
		WHERE gifts.given_at IS NOT NULL
		AND EXISTS (SELECT 1 FROM gift_recipients WHERE gift_recipients.gift_id = gifts.id AND gift_recipients.user_id = ?)
		AND NOT EXISTS (SELECT 1 FROM gift_recipients WHERE gift_recipients.gift_id = gifts.id AND gift_recipients.user_id <> ?)
		ORDER BY gifts.id`, userID, userID); err != nil {
		return fmt.Errorf("find the gifts given only to a user: %w", err)
	}
	for _, g := range gifts {
		if err := s.deleteGift(ctx, tx, g.ID); err != nil {
			return fmt.Errorf("delete a gift given only to a user: %w", err)
		}
		if err := item.PurgeIfUnused(ctx, tx, g.ItemID, s.now()); err != nil {
			return err
		}
	}
	return nil
}

// DeleteOpenOn deletes every open gift on the items of a list that is being
// deleted, whoever gives it. Unlike a list's delete by a user, no gift is kept for
// its giver, because nobody can receive it (the list's only recipient is being
// deleted). An item that was deleted softly and has no gift left goes for good.
func (s *Service) DeleteOpenOn(ctx context.Context, tx *sqlx.Tx, listID int64) error {
	var gifts []struct {
		ID     int64 `db:"id"`
		ItemID int64 `db:"item_id"`
	}
	if err := tx.SelectContext(ctx, &gifts, `SELECT gifts.id, gifts.item_id FROM gifts JOIN items ON items.id = gifts.item_id
		WHERE items.list_id = ? AND gifts.given_at IS NULL ORDER BY gifts.id`, listID); err != nil {
		return fmt.Errorf("find the open gifts on a list: %w", err)
	}
	for _, g := range gifts {
		if err := s.deleteGift(ctx, tx, g.ID); err != nil {
			return fmt.Errorf("delete an open gift on a deleted list: %w", err)
		}
		if err := item.PurgeIfUnused(ctx, tx, g.ItemID, s.now()); err != nil {
			return err
		}
	}
	return nil
}

// ForgetKeptRecipient takes a user who is about to be deleted out of the last
// recipients of the lists that were deleted earlier with their gifts kept
// (kept_list_recipients), and settles the open gifts on them. An open gift that only
// the user could receive is deleted: nobody is left to give it to, and a gift is
// never for its giver. So a kept list whose remaining recipients are only the
// giver releases the gift, because the user's delete is what left it with nobody.
// A list that is kept for the giver alone from the start is not touched here: its
// gift reopens with its old occasion date (see Restore).
//
// The open gifts that stay are for the remaining recipients. They are settled, so
// one that was for an occasion of the user moves to the next occasion of the others,
// and is flagged.
func (s *Service) ForgetKeptRecipient(ctx context.Context, tx *sqlx.Tx, userID int64) error {
	var listIDs []int64
	if err := tx.SelectContext(ctx, &listIDs, `SELECT list_id FROM kept_list_recipients WHERE user_id = ? ORDER BY list_id`, userID); err != nil {
		return fmt.Errorf("find the lists kept for a user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM kept_list_recipients WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("forget a user among the last recipients: %w", err)
	}
	for _, listID := range listIDs {
		var remaining []int64
		if err := tx.SelectContext(ctx, &remaining, `SELECT user_id FROM kept_list_recipients WHERE list_id = ? ORDER BY user_id`, listID); err != nil {
			return fmt.Errorf("find the last recipients of a deleted list: %w", err)
		}
		var gifts []struct {
			ID      int64 `db:"id"`
			ItemID  int64 `db:"item_id"`
			GiverID int64 `db:"giver_id"`
		}
		if err := tx.SelectContext(ctx, &gifts, `SELECT gifts.id, gifts.item_id, gifts.giver_id FROM gifts JOIN items ON items.id = gifts.item_id
			WHERE items.list_id = ? AND gifts.given_at IS NULL AND gifts.giver_id IS NOT NULL ORDER BY gifts.id`, listID); err != nil {
			return fmt.Errorf("find the open gifts on a kept list: %w", err)
		}
		for _, g := range gifts {
			others := slices.DeleteFunc(slices.Clone(remaining), func(id int64) bool { return id == g.GiverID })
			if len(others) > 0 {
				continue
			}
			if err := s.deleteGift(ctx, tx, g.ID); err != nil {
				return fmt.Errorf("delete a gift that only a deleted user could receive: %w", err)
			}
			if err := item.PurgeIfUnused(ctx, tx, g.ItemID, s.now()); err != nil {
				return err
			}
		}
		if err := s.settle(ctx, tx, scope{where: `items.list_id = ?`, args: []any{listID}}); err != nil {
			return err
		}
	}
	return nil
}

// SettleOccasionsOf settles the open gifts whose occasion belongs to the user, who
// is about to be deleted and is no longer a recipient of any list. It catches a
// gift that nothing above moved, so none is left pointing at an occasion that the
// delete takes (gifts.occasion_id is ON DELETE SET NULL).
func (s *Service) SettleOccasionsOf(ctx context.Context, tx *sqlx.Tx, userID int64) error {
	return s.settle(ctx, tx, scope{where: `gifts.occasion_id IN (SELECT id FROM occasions WHERE user_id = ?)`, args: []any{userID}})
}
