package gift

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/user"
)

// This file keeps open gifts right when something changes under them
// (docs/spec.md, Gifts, Who a gift is for, Occasions for a gift and Changes
// while a gift is open). The change is made by another package (a list, an
// occasion, a user, an item), which tells the observer that [Service.Watch]
// registers, inside its own transaction. So the change and what it does to the
// gifts commit together.
//
// [Service.settle] is the one rule. It looks at the state after the change, so it
// has no memory of what the change was, and it is safe to run again. For each open
// gift in its scope, in this order:
//
//  1. When the giver is the only recipient of the gift's list, the gift is
//     released, as an unclaim does.
//  2. When the giver is not a recipient and sees none of the recipients, the gift is
//     "no longer shared": flagged once, when it becomes so (gifts.unshared).
//  3. Unless the gift's occasion date has passed (the gift waits for its reveal),
//     the gift's occasion must be one that applies to a recipient the giver can see
//     and may pick. When the giver sees none of the recipients, it must apply to
//     some recipient other than the giver. If it doesn't, the gift moves to the
//     next upcoming occasion of those recipients, and is flagged occasion_moved.
//
// A change in a list's recipients also raises recipients_changed first, which needs
// the recipients from before.

// Watch makes the service the observer of the services that change what an open
// gift depends on. Call it once, before the services are used.
func (s *Service) Watch(lists *list.Service, occasions *occasion.Service, users *user.Service, items *item.Service, groups *group.Service) {
	w := watcher{s}
	groups.Observe(w)
	lists.Observe(w)
	occasions.Observe(w)
	users.Observe(w)
	items.Observe(w)
}

// watcher implements the observers of the packages list, occasion, user and item.
type watcher struct{ s *Service }

// RecipientsChanged implements [list.Observer].
func (w watcher) RecipientsChanged(ctx context.Context, tx *sqlx.Tx, listID int64, before []int64) error {
	return w.s.recipientsChanged(ctx, tx, listID, before)
}

// Deleting implements [occasion.Observer].
func (w watcher) Deleting(ctx context.Context, tx *sqlx.Tx, id int64) error {
	return w.s.settle(ctx, tx, scope{where: `gifts.occasion_id = ?`, args: []any{id}, gone: id})
}

// Changed implements [occasion.Observer].
func (w watcher) Changed(ctx context.Context, tx *sqlx.Tx, id int64, rule bool) error {
	return w.s.settle(ctx, tx, scope{where: `gifts.occasion_id = ?`, args: []any{id}, redate: rule})
}

// BirthdayChanged implements [user.Observer].
func (w watcher) BirthdayChanged(ctx context.Context, tx *sqlx.Tx, userID int64) error {
	var id int64
	err := tx.GetContext(ctx, &id, `SELECT id FROM occasions WHERE kind = 'birthday' AND user_id = ?`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find a birthday occasion: %w", err)
	}
	return w.s.settle(ctx, tx, scope{where: `gifts.occasion_id = ?`, args: []any{id}, redate: true})
}

// VisibilityChanged implements [group.Observer] and [user.Observer]: the open
// gifts that the users give, or that are on a list that one of them is a recipient
// of, may have a giver who now sees more or fewer of the recipients.
func (w watcher) VisibilityChanged(ctx context.Context, tx *sqlx.Tx, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	where, args, err := sqlx.In(`gifts.giver_id IN (?) OR items.list_id IN (SELECT list_id FROM list_recipients WHERE user_id IN (?))`, userIDs, userIDs)
	if err != nil {
		return fmt.Errorf("find the gifts of the users: %w", err)
	}
	return w.s.settle(ctx, tx, scope{where: where, args: args})
}

// Deactivated implements [user.Observer]: the open gifts of a deactivated giver
// are released. Each one is deleted, and its quantity goes back on the item for
// all givers. The gifts they gave stay in their recipients' Received.
func (w watcher) Deactivated(ctx context.Context, tx *sqlx.Tx, userID int64) error {
	return w.s.releaseOf(ctx, tx, userID)
}

// Moved implements [item.Observer].
func (w watcher) Moved(ctx context.Context, tx *sqlx.Tx, itemID int64) error {
	return w.s.settle(ctx, tx, scope{where: `gifts.item_id = ?`, args: []any{itemID}})
}

// releaseOf releases the open gifts of a giver, as an unclaim does: each is
// deleted, with the copy of an image from an earlier reveal, and an item that was
// kept only for it goes. The quantity is not stored on the gift's item, so it is
// back for every giver at once.
func (s *Service) releaseOf(ctx context.Context, tx *sqlx.Tx, giverID int64) error {
	var gifts []struct {
		ID     int64 `db:"id"`
		ItemID int64 `db:"item_id"`
	}
	if err := tx.SelectContext(ctx, &gifts, `SELECT id, item_id FROM gifts WHERE giver_id = ? AND given_at IS NULL ORDER BY id`, giverID); err != nil {
		return fmt.Errorf("find the open gifts of a giver: %w", err)
	}
	now := s.now()
	for _, g := range gifts {
		if err := s.deleteGift(ctx, tx, g.ID); err != nil {
			return fmt.Errorf("release a gift: %w", err)
		}
		if err := item.PurgeIfUnused(ctx, tx, g.ItemID, now); err != nil {
			return err
		}
	}
	return nil
}

// scope says which open gifts [Service.settle] looks at.
type scope struct {
	// where is a condition on the gifts and items tables, with its arguments.
	where string
	args  []any
	// gone is an occasion that is about to be deleted: it counts as an occasion
	// that applies to nobody.
	gone int64
	// redate is true when the date rule of the gifts' occasion changed, so a gift
	// whose date is not the occasion's next date moves to it.
	redate bool
}

// settleRow is an open gift that settle looks at.
type settleRow struct {
	ID           int64         `db:"id"`
	ItemID       int64         `db:"item_id"`
	GiverID      int64         `db:"giver_id"`
	ListID       int64         `db:"list_id"`
	OccasionID   sql.NullInt64 `db:"occasion_id"`
	OccasionDate string        `db:"occasion_date"`
	Unshared     bool          `db:"unshared"`
}

// recipientsChanged flags the open gifts on the list whose recipients changed,
// and then settles them. before holds the recipients before the change. A gift
// is never for its giver, so a recipient who is the giver is left out of both
// sides: a giver who joins the list changes nothing for their own gift.
func (s *Service) recipientsChanged(ctx context.Context, tx *sqlx.Tx, listID int64, before []int64) error {
	after, err := list.RecipientIDs(ctx, tx, listID)
	if err != nil {
		return err
	}
	var gifts []settleRow
	if err := tx.SelectContext(ctx, &gifts, settleSQL+` WHERE gifts.given_at IS NULL AND gifts.giver_id IS NOT NULL AND items.list_id = ? ORDER BY gifts.id`, listID); err != nil {
		return fmt.Errorf("find the open gifts on a list: %w", err)
	}
	now := s.now()
	for _, g := range gifts {
		notGiver := func(id int64) bool { return id != g.GiverID }
		old := slices.DeleteFunc(slices.Clone(before), func(id int64) bool { return !notGiver(id) })
		cur := slices.DeleteFunc(slices.Clone(after), func(id int64) bool { return !notGiver(id) })
		if err := giftflag.ChangeRecipients(ctx, tx, g.ID, old, cur, now); err != nil {
			return err
		}
	}
	return s.settle(ctx, tx, scope{where: `items.list_id = ?`, args: []any{listID}})
}

const settleSQL = `SELECT gifts.id, gifts.item_id, gifts.giver_id, items.list_id, gifts.occasion_id, gifts.occasion_date, gifts.unshared
	FROM gifts JOIN items ON items.id = gifts.item_id`

// settle makes the open gifts in the scope right, in the transaction tx. See the
// notes at the top of this file.
func (s *Service) settle(ctx context.Context, tx *sqlx.Tx, sc scope) error {
	var gifts []settleRow
	if err := tx.SelectContext(ctx, &gifts, settleSQL+` WHERE gifts.given_at IS NULL AND gifts.giver_id IS NOT NULL AND (`+sc.where+`) ORDER BY gifts.id`, sc.args...); err != nil {
		return fmt.Errorf("find the open gifts to settle: %w", err)
	}
	type recipientSet struct {
		ids  []int64
		kept bool
	}
	recipients := map[int64]recipientSet{}
	givers := map[int64]user.User{}
	for _, g := range gifts {
		set, ok := recipients[g.ListID]
		if !ok {
			var err error
			if set.ids, err = list.RecipientIDs(ctx, tx, g.ListID); err != nil {
				return err
			}
			if len(set.ids) == 0 {
				// A list that was deleted with its gifts kept: the gifts are for its
				// last recipients, and nobody sees the list.
				set.kept = true
				if err := tx.SelectContext(ctx, &set.ids, `SELECT user_id FROM kept_list_recipients WHERE list_id = ? ORDER BY user_id`, g.ListID); err != nil {
					return fmt.Errorf("find the last recipients of a deleted list: %w", err)
				}
			}
			recipients[g.ListID] = set
		}
		giver, ok := givers[g.GiverID]
		if !ok {
			var err error
			if giver, err = user.Get(ctx, tx, g.GiverID); err != nil {
				return fmt.Errorf("find the giver of a gift: %w", err)
			}
			givers[g.GiverID] = giver
		}
		if err := s.settleOne(ctx, tx, sc, g, giver, set.ids, set.kept); err != nil {
			return err
		}
	}
	return nil
}

// settleOne settles one gift. recipients are the recipients of its list, or the
// last ones of a list that was deleted with its gifts kept (kept): nobody sees that
// list, so the giver sees none of them, and the gift is neither released nor
// flagged no longer shared (the delete of the list flagged it removed).
func (s *Service) settleOne(ctx context.Context, tx *sqlx.Tx, sc scope, g settleRow, giver user.User, recipients []int64, kept bool) error {
	if len(recipients) == 0 {
		return nil // no one to give to; every list has a recipient, so this does not happen
	}
	isRecipient := slices.Contains(recipients, giver.ID)
	others := slices.DeleteFunc(slices.Clone(recipients), func(id int64) bool { return id == giver.ID })
	now := s.now()

	// 1. The giver is the only recipient: the gift is released.
	if len(others) == 0 && !kept {
		if err := s.deleteGift(ctx, tx, g.ID); err != nil {
			return fmt.Errorf("release a gift: %w", err)
		}
		return item.PurgeIfUnused(ctx, tx, g.ItemID, now)
	}

	// 2. The giver sees none of the recipients.
	givable, args := list.GivableRecipients(giver, "?")
	var visible []int64
	if err := tx.SelectContext(ctx, &visible, givable, append([]any{g.ListID}, args...)...); err != nil {
		return fmt.Errorf("find the recipients to give to: %w", err)
	}
	if unshared := !kept && !isRecipient && len(visible) == 0; unshared != g.Unshared {
		if _, err := tx.ExecContext(ctx, `UPDATE gifts SET unshared = ? WHERE id = ?`, unshared, g.ID); err != nil {
			return fmt.Errorf("mark a gift shared or not: %w", err)
		}
		if unshared {
			if err := giftflag.Raise(ctx, tx, g.ID, giftflag.NoLongerShared, now); err != nil {
				return err
			}
		}
	}

	// 3. The occasion. A gift past its date waits for its reveal.
	loc := s.occ.FallbackZone()
	if giver.Timezone != nil {
		if l, err := time.LoadLocation(*giver.Timezone); err == nil {
			loc = l
		}
	}
	if g.OccasionDate < occasion.DateOf(now, loc).String() {
		return nil
	}
	options, err := s.nextOccasions(ctx, tx, giver, g.ListID, others, len(visible) > 0)
	if err != nil {
		return err
	}
	options = slices.DeleteFunc(options, func(o occasion.Occasion) bool { return o.ID == sc.gone })
	if len(options) == 0 {
		return nil // every user has a birthday, so this does not happen
	}
	target := options[0]
	if i := slices.IndexFunc(options, func(o occasion.Occasion) bool { return g.OccasionID.Valid && o.ID == g.OccasionID.Int64 }); i >= 0 {
		// It still applies. Only a change of its date moves the gift, to the new date.
		if !sc.redate || options[i].NextDate.String() == g.OccasionDate {
			return nil
		}
		target = options[i]
	}
	if err := giftflag.Raise(ctx, tx, g.ID, giftflag.OccasionMoved, now); err != nil {
		return err
	}
	return s.moveOccasion(ctx, tx, g, target, false)
}

// nextOccasions returns the occasions that an open gift can have, soonest first,
// by the rule of docs/spec.md, Gifts, Occasions for a gift: the occasions the
// giver may pick on the list, or, when the giver sees none of the recipients
// (sees is false), the upcoming occasions of the recipients other than the giver.
// The first is the default. q is a database or a transaction.
func (s *Service) nextOccasions(ctx context.Context, q sqlx.QueryerContext, giver user.User, listID int64, others []int64, sees bool) ([]occasion.Occasion, error) {
	if sees {
		return s.occ.Pickable(ctx, q, giver, listID)
	}
	return s.occ.UpcomingFor(ctx, q, giver, others)
}

// moveOccasion moves an open gift to the occasion and its next date. When the
// giver already has an open gift on the item for that occasion, that gift takes
// this one in (see [Service.merge]). The caller raises any flag.
//
// reopen is for a gift that a restore opens again: it is still given, and the same
// statement that moves it makes it open, because the database allows one open gift
// for an item, a giver and an occasion, and the one that takes it in may be the
// gift for the occasion it had.
func (s *Service) moveOccasion(ctx context.Context, tx *sqlx.Tx, g settleRow, to occasion.Occasion, reopen bool) error {
	var staying struct {
		ID        int64          `db:"id"`
		Quantity  int            `db:"quantity"`
		FromLabel sql.NullString `db:"from_label"`
		Note      sql.NullString `db:"note"`
	}
	err := tx.GetContext(ctx, &staying, `SELECT id, quantity, from_label, note FROM gifts
		WHERE item_id = ? AND giver_id = ? AND occasion_id = ? AND given_at IS NULL AND id <> ?`, g.ItemID, g.GiverID, to.ID, g.ID)
	switch {
	case err == nil:
		var moving struct {
			Quantity  int            `db:"quantity"`
			FromLabel sql.NullString `db:"from_label"`
			Note      sql.NullString `db:"note"`
		}
		if err := tx.GetContext(ctx, &moving, `SELECT quantity, from_label, note FROM gifts WHERE id = ?`, g.ID); err != nil {
			return fmt.Errorf("find the gift that moves: %w", err)
		}
		_, err := s.merge(ctx, tx, staying.ID, g.ID, moving.Quantity, nilIfInvalid(moving.FromLabel.String, moving.FromLabel.Valid),
			nilIfInvalid(moving.Note.String, moving.Note.Valid), staying.Quantity, staying.FromLabel, staying.Note)
		return err
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("find the gift that takes this one in: %w", err)
	}
	query := `UPDATE gifts SET occasion_id = ?, occasion_date = ? WHERE id = ?`
	if reopen {
		query = `UPDATE gifts SET occasion_id = ?, occasion_date = ?, given_at = NULL, giver_saw_list = NULL, giver_saw_occasion = NULL WHERE id = ?`
	}
	if _, err := tx.ExecContext(ctx, query, to.ID, to.NextDate.String(), g.ID); err != nil {
		return fmt.Errorf("move a gift to an occasion: %w", err)
	}
	return nil
}
