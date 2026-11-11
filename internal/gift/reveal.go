package gift

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/user"
)

// This file is reveal (docs/spec.md, Gifts, Reveal; ADR 0011). At 00:00 on the
// day after its occasion, at the latest of its recipients' midnights, an open gift
// is given:
//
//   - `given_at` is set;
//   - the gift copies the item's title, its price, its primary image (as a new
//     image) and the occasion's name, so later edits don't change what was given;
//   - the gift is for the recipients of its list at that moment, never its giver:
//     for a list that was deleted with the gift kept, they are its last recipients;
//   - the given quantity leaves the item ([item.Given]);
//   - the flags of the gift go: a given gift is never flagged.
//
// Each gift is given in a transaction of its own, which reads the gift again and
// gives it only if it is still open and its moment has passed, so a run that
// overlaps another, or a crash, never gives a gift twice, and a change since the
// run found the gift is never missed. The first run
// after a start gives every gift whose moment passed while the server was down.

// Revealer gives the gifts whose moment has come.
type Revealer struct {
	db     *sqlx.DB
	images *image.Service
	now    func() time.Time
	// fallback is the zone of a recipient who has no timezone. It is the
	// occasion service's, as in [Service].
	fallback *time.Location
	// afterFind, if set, runs after a run has found its gifts and before it gives
	// the first one. See [Revealer.AfterFind].
	afterFind func()
}

// NewRevealer returns a Revealer. images copies the item's image. occ gives the
// zone of a recipient who has no timezone (the server's TZ), the same one that
// the gift service uses for the reveal moment it shows, so the moment a client
// reads and the moment the revealer gives the gift can't differ. now is the
// clock, so tests control time.
func NewRevealer(db *sqlx.DB, images *image.Service, occ *occasion.Service, now func() time.Time) *Revealer {
	return &Revealer{db: db, images: images, now: func() time.Time { return now().UTC() }, fallback: occ.FallbackZone()}
}

// AfterFind sets a function that runs once in each run, after the run has found
// the open gifts and before it gives the first. It lets a test change a gift in
// the gap between the two, which a real request can do at any time. Call it
// before the first run.
func (r *Revealer) AfterFind(fn func()) { r.afterFind = fn }

// Schedule runs [Revealer.Run] now and then every interval until ctx ends. The
// first run is the catch-up after downtime. A failed run is logged and tried
// again on the next tick. A gift that fails is logged with its ID and no other
// data, and does not stop the others. logger may be nil.
func (r *Revealer) Schedule(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		n, err := r.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		logFailures(logger, err)
		if n > 0 {
			logger.Info("gave gifts", slog.Int("count", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// logFailures logs each failed gift of a run, or the run's own error.
func logFailures(logger *slog.Logger, err error) {
	if err == nil {
		return
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		logger.Error("reveal failed", slog.Any("error", err))
		return
	}
	for _, e := range joined.Unwrap() {
		var failed *GiftError
		if errors.As(e, &failed) {
			logger.Error("reveal failed for a gift", slog.Int64("gift_id", failed.GiftID), slog.Any("error", failed.Err))
		} else {
			logger.Error("reveal failed", slog.Any("error", e))
		}
	}
}

// Run gives every open gift whose moment has passed, and returns how many it
// gave. A gift that fails is skipped and does not stop the others: Run goes on to
// the rest, and returns an error that holds a [GiftError] for each gift that
// failed. The next run tries those gifts again.
func (r *Revealer) Run(ctx context.Context) (int, error) {
	return r.run(ctx, r.now(), 0)
}

// RunFor gives the open gifts on the lists that a user is a recipient of, as if
// the clock read at. It leaves the clock and every other gift alone. It exists for
// the e2e build, whose tests share one server: a clock jump or a reveal for
// everyone would change other tests' data.
func (r *Revealer) RunFor(ctx context.Context, recipient int64, at time.Time) (int, error) {
	return r.run(ctx, at.UTC(), recipient)
}

// GiftError is the failure to give one gift. It names the gift by its ID only, so
// a log line holds no personal data.
type GiftError struct {
	GiftID int64
	Err    error
}

func (e *GiftError) Error() string { return fmt.Sprintf("give gift %d: %v", e.GiftID, e.Err) }
func (e *GiftError) Unwrap() error { return e.Err }

// due is an open gift as one transaction reads it, with what reveal copies.
type due struct {
	ID           int64         `db:"id"`
	ItemID       int64         `db:"item_id"`
	GiverID      int64         `db:"giver_id"`
	ListID       int64         `db:"list_id"`
	Quantity     int           `db:"quantity"`
	OccasionID   sql.NullInt64 `db:"occasion_id"`
	OccasionDate string        `db:"occasion_date"`
}

// run gives the due gifts as of now. A non-zero recipient limits it to the gifts
// on the lists that the user is a recipient of.
//
// The first query only finds the gifts that may be due. It is stale as soon as it
// ends: a giver can change a gift, and a user can move its item, before the gift's
// own transaction opens. So [Revealer.give] reads the gift again, and decides
// again whether it is open and due.
func (r *Revealer) run(ctx context.Context, now time.Time, recipient int64) (int, error) {
	var open []int64
	// No timezone is more than a day ahead of UTC, so a date after tomorrow's is not
	// due. The query only skips gifts that can't be due.
	if err := r.db.SelectContext(ctx, &open, `SELECT gifts.id
		FROM gifts JOIN items ON items.id = gifts.item_id
		WHERE gifts.given_at IS NULL AND gifts.giver_id IS NOT NULL AND gifts.occasion_date <= ?
		AND (? = 0 OR items.list_id IN (SELECT list_id FROM list_recipients WHERE user_id = ?
			UNION SELECT list_id FROM kept_list_recipients WHERE user_id = ?))
		ORDER BY gifts.occasion_date, gifts.id`,
		now.AddDate(0, 0, 1).Format("2006-01-02"), recipient, recipient, recipient); err != nil {
		return 0, fmt.Errorf("find the open gifts: %w", err)
	}
	if len(open) == 0 {
		return 0, nil
	}
	if r.afterFind != nil {
		r.afterFind()
	}
	n := 0
	var failed []error
	for _, id := range open {
		if err := ctx.Err(); err != nil {
			return n, errors.Join(append(failed, err)...)
		}
		given, err := r.give(ctx, id, now, recipient)
		switch {
		case err != nil:
			failed = append(failed, &GiftError{GiftID: id, Err: err})
		case given:
			n++
		}
	}
	return n, errors.Join(failed...)
}

// give gives one gift, in a transaction. It reads the gift again there, and gives
// it only if it is still open and its moment has passed: it reports false when
// another run got there first, or when a change since the first query moved the
// moment (or, with a non-zero recipient, the list) out of reach.
func (r *Revealer) give(ctx context.Context, id int64, now time.Time, recipient int64) (done bool, err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var row struct {
		ItemID       int64         `db:"item_id"`
		GiverID      sql.NullInt64 `db:"giver_id"`
		ListID       int64         `db:"list_id"`
		Quantity     int           `db:"quantity"`
		OccasionID   sql.NullInt64 `db:"occasion_id"`
		OccasionDate string        `db:"occasion_date"`
		GivenAt      sql.NullTime  `db:"given_at"`
	}
	if err := tx.GetContext(ctx, &row, `SELECT gifts.item_id, gifts.giver_id, items.list_id, gifts.quantity,
			gifts.occasion_id, gifts.occasion_date, gifts.given_at
		FROM gifts JOIN items ON items.id = gifts.item_id WHERE gifts.id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("find the gift: %w", err)
	}
	if row.GivenAt.Valid || !row.GiverID.Valid {
		return false, nil
	}
	g := due{ID: id, ItemID: row.ItemID, GiverID: row.GiverID.Int64, ListID: row.ListID, Quantity: row.Quantity,
		OccasionID: row.OccasionID, OccasionDate: row.OccasionDate}
	if recipient != 0 {
		ids, err := recipientsAtReveal(ctx, tx, g.ListID)
		if err != nil {
			return false, err
		}
		if !slices.Contains(ids, recipient) {
			return false, nil
		}
	}
	date, err := occasion.ParseDate(g.OccasionDate)
	if err != nil {
		return false, fmt.Errorf("bad occasion date %q: %w", g.OccasionDate, err)
	}
	zones, err := recipientZones(ctx, tx, []int64{g.ListID})
	if err != nil {
		return false, err
	}
	if now.Before(revealAt(date, zones[g.ListID], row.GiverID, r.fallback)) {
		return false, nil
	}

	res, err := tx.ExecContext(ctx, `UPDATE gifts SET given_at = ? WHERE id = ? AND given_at IS NULL`, now, g.ID)
	if err != nil {
		return false, fmt.Errorf("set given_at: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}

	var it struct {
		Title         string         `db:"title"`
		PriceMinor    sql.NullInt64  `db:"price_minor"`
		TopPriceMinor sql.NullInt64  `db:"top_price_minor"`
		Currency      sql.NullString `db:"currency"`
		ImageID       sql.NullInt64  `db:"image_id"`
	}
	if err := tx.GetContext(ctx, &it, `SELECT items.title, items.price_minor, items.top_price_minor, items.currency,
		(SELECT image_id FROM item_images WHERE item_id = items.id AND is_primary = 1) AS image_id
		FROM items WHERE items.id = ?`, g.ItemID); err != nil {
		return false, fmt.Errorf("find the item: %w", err)
	}
	var occasionName sql.NullString
	if g.OccasionID.Valid {
		// The occasion may be gone by now: the gift then keeps no name.
		err := tx.GetContext(ctx, &occasionName, `SELECT name FROM occasions WHERE id = ?`, g.OccasionID.Int64)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("find the occasion: %w", err)
		}
	}

	// What the giver could see, so the gift shows them the same later.
	giver, err := user.Get(ctx, tx, g.GiverID)
	if err != nil {
		return false, fmt.Errorf("find the giver: %w", err)
	}
	sawList, sawOccasion, err := giverSight(ctx, tx, giver, g)
	if err != nil {
		return false, err
	}

	// The picture is copied as a new image of the gift's own. A picture whose file
	// is gone leaves the gift without one.
	var copied *image.Image
	if it.ImageID.Valid {
		img, err := r.images.Copy(ctx, tx, g.GiverID, it.ImageID.Int64)
		switch {
		case errors.Is(err, image.ErrNotFound):
		case err != nil:
			return false, fmt.Errorf("copy the image: %w", err)
		default:
			copied = &img
			// A copy that doesn't reach a committed row is an orphan, so remove its file.
			defer func() {
				if !done {
					_ = r.images.Discard(img)
				}
			}()
		}
	}
	var copiedID sql.NullInt64
	if copied != nil {
		copiedID = sql.NullInt64{Int64: copied.ID, Valid: true}
	}
	// A gift that a restore opened again still holds the copy from the reveal
	// before. The new copy replaces it, so the old image is let go and the sweep
	// deletes it (docs/spec.md, Gifts, Restore).
	var oldCopy sql.NullInt64
	if err := tx.GetContext(ctx, &oldCopy, `SELECT image_id FROM gifts WHERE id = ?`, g.ID); err != nil {
		return false, fmt.Errorf("find the copy of a restored gift: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gifts SET title = ?, price_minor = ?, top_price_minor = ?, currency = ?, image_id = ?,
			occasion_name = ?, giver_saw_list = ?, giver_saw_occasion = ? WHERE id = ?`,
		it.Title, it.PriceMinor, it.TopPriceMinor, it.Currency, copiedID, occasionName, sawList, sawOccasion, g.ID); err != nil {
		return false, fmt.Errorf("copy what was given: %w", err)
	}
	if oldCopy.Valid {
		if err := image.Detach(ctx, tx, now, oldCopy.Int64); err != nil {
			return false, err
		}
	}

	// The recipients, now and for good: the list's, or a deleted list's last ones.
	// Never the giver.
	recipients, err := recipientsAtReveal(ctx, tx, g.ListID)
	if err != nil {
		return false, err
	}
	recipients = slices.DeleteFunc(recipients, func(id int64) bool { return id == g.GiverID })
	for i, id := range recipients {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gift_recipients (gift_id, user_id, position) VALUES (?, ?, ?)`, g.ID, id, i); err != nil {
			return false, fmt.Errorf("record a recipient: %w", err)
		}
	}

	if err := giftflag.Clear(ctx, tx, g.ID, giftflag.Codes); err != nil {
		return false, err
	}
	if err := item.Given(ctx, tx, g.ItemID, g.Quantity, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// giverSight says whether the giver could see the gift's list, and its occasion
// (docs/routes.md, Gift fields): the occasion is one of a recipient they could
// see, that they could see themselves.
func giverSight(ctx context.Context, tx *sqlx.Tx, giver user.User, g due) (sawList, sawOccasion bool, err error) {
	seen, args := list.VisibleTo(giver)
	if err := tx.GetContext(ctx, &sawList, `SELECT EXISTS (SELECT 1 FROM lists WHERE lists.id = ? AND `+seen+`)`, append([]any{g.ListID}, args...)...); err != nil {
		return false, false, fmt.Errorf("find out whether the giver sees the list: %w", err)
	}
	if !sawList || !g.OccasionID.Valid {
		return sawList, false, nil
	}
	pickable, err := occasion.PickableIDs(ctx, tx, giver, g.ListID)
	if err != nil {
		return false, false, err
	}
	return sawList, pickable[g.OccasionID.Int64], nil
}

// recipientsAtReveal returns the recipients of a list in the order the list shows
// them, or the last recipients of a list that was deleted with its gifts kept.
func recipientsAtReveal(ctx context.Context, tx *sqlx.Tx, listID int64) ([]int64, error) {
	ids, err := list.RecipientIDs(ctx, tx, listID)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		return ids, nil
	}
	if err := tx.SelectContext(ctx, &ids, `SELECT user_id FROM kept_list_recipients WHERE list_id = ? ORDER BY user_id`, listID); err != nil {
		return nil, fmt.Errorf("find the last recipients of a deleted list: %w", err)
	}
	return ids, nil
}
