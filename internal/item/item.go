// Package item owns the items table: the things a list's recipients want. An
// item is on exactly one list, at a position, and the users who can edit the
// list (internal/list) add, change, reorder, move and delete it.
//
// Who can see an item and who can change it follow the item's list: the
// conditions are internal/list's [list.VisibleTo] and [list.EditableBy], and
// are repeated nowhere here.
//
// See the layout notes in package user.
package item

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

const (
	// MaxTitleLength is the longest item title, in characters.
	MaxTitleLength = 300
	// MaxNotesLength is the longest item note, in characters.
	MaxNotesLength = 2000
	// DefaultCurrency is the currency of a price that names none. A client may
	// leave the currency out, so the default is the server's (docs/spec.md,
	// API principles, Server and clients).
	DefaultCurrency = "USD"
)

var (
	// ErrNotFound means no item has the ID, or the viewer can't see its list.
	ErrNotFound = errors.New("no such item")
	// ErrTargetNotFound means the list an item moves to doesn't exist, or the
	// viewer can't see it.
	ErrTargetNotFound = errors.New("no such list to move the item to")
	// ErrCannotEditTarget means the viewer sees the list an item moves to but
	// can't edit it.
	ErrCannotEditTarget = errors.New("you can't edit the list to move the item to")
	// ErrInvalid means the input failed a rule. The message is safe to show.
	ErrInvalid = errors.New("invalid input")
)

type invalidError struct{ msg string }

func (e invalidError) Error() string        { return e.msg }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

// FieldError says which input field failed a rule. It matches [ErrInvalid].
type FieldError struct {
	// Field is the name of the field in the input, such as "title".
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func invalid(field, format string, args ...any) error {
	return &FieldError{Field: field, Err: invalidError{fmt.Sprintf(format, args...)}}
}

// Item is an item as the viewer sees it.
type Item struct {
	ID     int64
	ListID int64
	Title  string
	Notes  *string
	// PriceMinor and TopPriceMinor are in the currency's minor unit. The top
	// price makes the price a range, and needs a price.
	PriceMinor    *int64
	TopPriceMinor *int64
	// Currency is an ISO 4217 code. It is nil exactly when PriceMinor is.
	Currency    *string
	Quantity    int
	IsUnlimited bool
	// Position is a 0-based index on the list, with no gaps.
	Position int
	// AddedBy is the user who added the item, or nil once that user is deleted.
	AddedBy   *user.Summary
	CreatedAt time.Time
	// CanEdit is the viewer's power over the item: their power over its list.
	CanEdit bool
	// StoreLinks are at most [MaxStoreLinks], never nil. A viewer who can edit
	// the list gets them in the order added, and everyone else by price.
	StoreLinks []StoreLink
	// Images are at most [MaxImages], never nil, in the order they were added.
	// PrimaryImage is one of them, or nil when there are none.
	Images       []image.Summary
	PrimaryImage *image.Summary
	// Claim is the claim state of a giver, and nil for a recipient, who never
	// sees it (docs/routes.md, Item fields).
	Claim *ClaimState
}

// ClaimState is what a giver sees of an item's gifts.
type ClaimState struct {
	// StillWanted is how many other givers can still claim, or nil for an
	// unlimited item.
	StillWanted *int
	// MyClaimedQuantity is the sum of the viewer's own open gifts on the item.
	MyClaimedQuantity int
	// CanClaim is true when the viewer can see an active recipient of the list
	// and some are still wanted (or the item is unlimited).
	CanClaim bool
}

// SortKeys are the sort keys of the items on a list (docs/spec.md, API
// principles, Collections). The first is the default.
var SortKeys = []page.Key{{Name: "position"}, {Name: "created_at", Time: true}, {Name: "price"}}

// New is the input for [Service.Add].
type New struct {
	Title         string
	Notes         string // empty for none
	PriceMinor    *int64
	TopPriceMinor *int64
	Currency      string // empty for the default, with a price
	Quantity      int
	IsUnlimited   bool
	// StoreLinks are at most [MaxStoreLinks]. An item with no price takes the
	// price of the first one that has a price.
	StoreLinks []NewStoreLink
	// ImageIDs are at most [MaxImages] uploaded images, in order. The viewer's
	// right to use them is checked by the [AddGuard]. PrimaryImageID is one of
	// them, or nil for the first.
	ImageIDs       []int64
	PrimaryImageID *int64
	// ImageFrom, if set, runs inside the transaction of [Service.Add] when ImageIDs
	// is empty, and may return one more image for the item, which becomes its
	// primary image. Add again uses it to copy a gift's image as a new image that
	// commits with the item. The caller removes the file of a copy if Add fails.
	ImageFrom func(ctx context.Context, q sqlx.ExtContext) (imageID int64, ok bool, err error)
}

// Changes is a merge patch to an item.
type Changes struct {
	Title         patch.Optional[string]
	Notes         patch.Optional[string]
	PriceMinor    patch.Optional[int64]
	TopPriceMinor patch.Optional[int64]
	Currency      patch.Optional[string]
	Quantity      patch.Optional[int]
	IsUnlimited   patch.Optional[bool]
	// Position moves the item to this index on its list, and the others shift.
	// An index past the end is the end.
	Position patch.Optional[int]
	// ListID moves the item to another list, at the end of it (or at Position,
	// if that is set too).
	ListID patch.Optional[int64]
	// ImageIDs replaces the whole array of the item's images, in the order
	// given. An image left out is detached, and the sweep deletes it. The array
	// can't be null: [] removes every image.
	ImageIDs patch.Optional[[]int64]
	// PrimaryImageID is one of the item's images after the change. null makes
	// the first image the primary image. Without it, the primary image stays if
	// it is still one of the images, and otherwise the first image takes its place.
	PrimaryImageID patch.Optional[int64]
}

// fields is the part of an item that the title, notes, price, quantity and
// unlimited flag make, with the rules that bind them together.
type fields struct {
	title         string
	notes         *string
	priceMinor    *int64
	topPriceMinor *int64
	currency      *string
	quantity      int
	isUnlimited   bool
}

func cleanTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		return "", invalid("title", "an item needs a title")
	case utf8.RuneCountInString(title) > MaxTitleLength:
		return "", invalid("title", "the title must be at most %d characters", MaxTitleLength)
	}
	return title, nil
}

func cleanNotes(notes string) (*string, error) {
	notes = strings.TrimSpace(notes)
	switch {
	case utf8.RuneCountInString(notes) > MaxNotesLength:
		return nil, invalid("notes", "the notes must be at most %d characters", MaxNotesLength)
	case notes == "":
		return nil, nil
	}
	return &notes, nil
}

// check applies the rules that tie the price fields together, and fills in the
// default currency. currencyGiven says the input named a currency.
func (f fields) check(currencyGiven bool) (fields, error) {
	switch {
	case f.quantity < 1:
		return f, invalid("quantity", "the quantity must be at least 1")
	case f.priceMinor != nil && *f.priceMinor < 0:
		return f, invalid("price_minor", "the price can't be negative")
	case f.topPriceMinor != nil && f.priceMinor == nil:
		return f, invalid("top_price_minor", "the top price needs a price")
	case f.topPriceMinor != nil && *f.topPriceMinor < *f.priceMinor:
		return f, invalid("top_price_minor", "the top price can't be below the price")
	}
	if f.priceMinor == nil {
		if currencyGiven && f.currency != nil {
			return f, invalid("currency", "a currency needs a price")
		}
		f.currency = nil
		return f, nil
	}
	if f.currency == nil {
		c := DefaultCurrency
		f.currency = &c
	}
	if !validCurrency(*f.currency) {
		return f, invalid("currency", "use a 3-letter currency code such as USD")
	}
	return f, nil
}

func validCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func (n New) fields() (fields, error) {
	title, err := cleanTitle(n.Title)
	if err != nil {
		return fields{}, err
	}
	notes, err := cleanNotes(n.Notes)
	if err != nil {
		return fields{}, err
	}
	f := fields{title: title, notes: notes, priceMinor: n.PriceMinor, topPriceMinor: n.TopPriceMinor, quantity: n.Quantity, isUnlimited: n.IsUnlimited}
	if n.Quantity == 0 {
		f.quantity = 1
	}
	if n.Currency != "" {
		f.currency = &n.Currency
	}
	return f.check(n.Currency != "")
}

// apply merges the changes into the item's current fields, and checks the
// result.
func (c Changes) apply(current Item) (fields, error) {
	f := fields{title: current.Title, notes: current.Notes, priceMinor: current.PriceMinor, topPriceMinor: current.TopPriceMinor,
		currency: current.Currency, quantity: current.Quantity, isUnlimited: current.IsUnlimited}
	if c.Title.Set {
		if c.Title.Value == nil {
			return f, invalid("title", "an item needs a title")
		}
		title, err := cleanTitle(*c.Title.Value)
		if err != nil {
			return f, err
		}
		f.title = title
	}
	if c.Notes.Set {
		f.notes = nil
		if c.Notes.Value != nil {
			notes, err := cleanNotes(*c.Notes.Value)
			if err != nil {
				return f, err
			}
			f.notes = notes
		}
	}
	if c.PriceMinor.Set {
		f.priceMinor = c.PriceMinor.Value
	}
	if c.TopPriceMinor.Set {
		f.topPriceMinor = c.TopPriceMinor.Value
	}
	if c.Currency.Set {
		f.currency = c.Currency.Value
	}
	if c.Quantity.Set {
		if c.Quantity.Value == nil {
			return f, invalid("quantity", "the quantity must be at least 1")
		}
		f.quantity = *c.Quantity.Value
	}
	if c.IsUnlimited.Set {
		if c.IsUnlimited.Value == nil {
			return f, invalid("is_unlimited", "is_unlimited can't be null")
		}
		f.isUnlimited = *c.IsUnlimited.Value
	}
	return f.check(c.Currency.Set)
}

type row struct {
	ID            int64          `db:"id"`
	ListID        int64          `db:"list_id"`
	Title         string         `db:"title"`
	Notes         sql.NullString `db:"notes"`
	PriceMinor    sql.NullInt64  `db:"price_minor"`
	TopPriceMinor sql.NullInt64  `db:"top_price_minor"`
	Currency      sql.NullString `db:"currency"`
	Quantity      int            `db:"quantity"`
	IsUnlimited   bool           `db:"is_unlimited"`
	Position      int            `db:"position"`
	CreatedAt     time.Time      `db:"created_at"`
	CanEdit       bool           `db:"can_edit"`

	Claimed       int  `db:"claimed"`
	Mine          int  `db:"mine"`
	IsRecipient   bool `db:"is_recipient"`
	SeesRecipient bool `db:"sees_recipient"`

	AddedByID     sql.NullInt64  `db:"added_by"`
	AddedByName   sql.NullString `db:"added_by_name"`
	AvatarImageID sql.NullInt64  `db:"added_by_avatar_image_id"`
	AvatarWidth   sql.NullInt64  `db:"added_by_avatar_width"`
	AvatarHeight  sql.NullInt64  `db:"added_by_avatar_height"`
}

func (r row) item() Item {
	it := Item{ID: r.ID, ListID: r.ListID, Title: r.Title, Quantity: r.Quantity, IsUnlimited: r.IsUnlimited, Position: r.Position,
		CreatedAt: r.CreatedAt, CanEdit: r.CanEdit}
	if !r.IsRecipient {
		c := ClaimState{MyClaimedQuantity: r.Mine}
		if !r.IsUnlimited {
			left := max(r.Quantity-r.Claimed, 0)
			c.StillWanted = &left
		}
		c.CanClaim = r.SeesRecipient && (r.IsUnlimited || *c.StillWanted > 0)
		it.Claim = &c
	}
	if r.Notes.Valid {
		it.Notes = &r.Notes.String
	}
	if r.PriceMinor.Valid {
		it.PriceMinor = &r.PriceMinor.Int64
	}
	if r.TopPriceMinor.Valid {
		it.TopPriceMinor = &r.TopPriceMinor.Int64
	}
	if r.Currency.Valid {
		it.Currency = &r.Currency.String
	}
	if r.AddedByID.Valid {
		s := user.Summary{ID: r.AddedByID.Int64, Name: r.AddedByName.String}
		if r.AvatarImageID.Valid {
			s.Avatar = &image.Summary{ID: r.AvatarImageID.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
		}
		it.AddedBy = &s
	}
	return it
}

// selectSQL selects an item as the viewer sees it. The item's list is joined as
// `lists`, so the conditions of internal/list apply as they are. The first
// placeholder is the viewer's edit rule, so its arguments come first.
func selectSQL(viewer user.User) (string, []any) {
	edit, args := EditableBy(viewer)
	recipients, recipientArgs := list.GivableRecipients(viewer, "items.list_id")
	args = append(append(args, viewer.ID, viewer.ID), recipientArgs...)
	return `SELECT items.id, items.list_id, items.title, items.notes, items.price_minor, items.top_price_minor, items.currency,
		items.quantity, items.is_unlimited, items.position, items.created_at, items.added_by,
		(SELECT name FROM users WHERE users.id = items.added_by) AS added_by_name,
		(SELECT avatar_image_id FROM users WHERE users.id = items.added_by) AS added_by_avatar_image_id,
		(SELECT width FROM images WHERE images.id = (SELECT avatar_image_id FROM users WHERE users.id = items.added_by)) AS added_by_avatar_width,
		(SELECT height FROM images WHERE images.id = (SELECT avatar_image_id FROM users WHERE users.id = items.added_by)) AS added_by_avatar_height,
		(` + edit + `) AS can_edit,
		COALESCE((SELECT SUM(g.quantity) FROM gifts g WHERE g.item_id = items.id AND g.given_at IS NULL), 0) AS claimed,
		COALESCE((SELECT SUM(g.quantity) FROM gifts g WHERE g.item_id = items.id AND g.given_at IS NULL AND g.giver_id = ?), 0) AS mine,
		items.list_id IN (SELECT list_id FROM list_recipients WHERE user_id = ?) AS is_recipient,
		EXISTS (` + recipients + `) AS sees_recipient
		FROM items JOIN lists ON lists.id = items.list_id`, args
}

// EditableBy is [list.EditableBy]: an item can be changed by the users who can
// edit its list.
func EditableBy(viewer user.User) (string, []any) { return list.EditableBy(viewer) }

// Service reads and changes items.
type Service struct {
	db       *sqlx.DB
	now      func() time.Time
	observer Observer
}

// Observer is told about a change to an item that can make its open gifts wrong
// (docs/spec.md, Items, Moving an item). It runs inside the transaction of the
// change, so the change and what it does to the gifts commit together, and an
// error from it undoes the change. package gift implements it.
type Observer interface {
	// Moved runs after the item moved to another list, and its open gifts were
	// flagged moved.
	Moved(ctx context.Context, tx *sqlx.Tx, itemID int64) error
}

// Observe sets the observer of the service. Call it before the service is used.
func (s *Service) Observe(o Observer) { s.observer = o }

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// get loads an item that the viewer can see. q is a database or a transaction.
func get(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (Item, error) {
	seen, seenArgs := list.VisibleTo(viewer)
	shown, shownArgs := list.ItemShownTo(viewer)
	query, args := selectSQL(viewer)
	var r row
	err := sqlx.GetContext(ctx, q, &r, query+` WHERE items.id = ? AND `+seen+` AND `+shown, slices.Concat(args, []any{id}, seenArgs, shownArgs)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("get item: %w", err)
	}
	items := []Item{r.item()}
	if err := loadExtras(ctx, q, items); err != nil {
		return Item{}, err
	}
	return items[0], nil
}

// GetVisible loads an item whose list the viewer can see. It returns
// [ErrNotFound] when there is no such item, and when the viewer can't see it.
func (s *Service) GetVisible(ctx context.Context, viewer user.User, id int64) (Item, error) {
	return get(ctx, s.db, viewer, id)
}

// CanSee says whether the viewer can see an item.
func (s *Service) CanSee(ctx context.Context, viewer user.User, id int64) (bool, error) {
	return CanSee(ctx, s.db, viewer, id)
}

// orderBy is the SQL of a sort: the ORDER BY terms, and the condition for the
// rows after a position. cmp is the comparison that means "later" in the
// direction of the sort.
func orderBy(req page.Request) (order string, after func() (string, []any, error)) {
	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	switch req.Key {
	case "created_at":
		return "items.created_at " + dir + ", items.id " + dir, func() (string, []any, error) {
			t, err := time.Parse(time.RFC3339Nano, req.After.Value)
			if err != nil {
				return "", nil, page.ErrBadCursor
			}
			return `(items.created_at ` + cmp + ` ? OR (items.created_at = ? AND items.id ` + cmp + ` ?))`,
				[]any{t, t, req.After.ID}, nil
		}
	case "price":
		// The low end of a range, by currency code and then by amount. An item
		// with no price is last, in both directions.
		return "(items.price_minor IS NULL), items.currency " + dir + ", items.price_minor " + dir + ", items.id " + dir,
			func() (string, []any, error) {
				if req.After.Value == priceNone {
					return `(items.price_minor IS NULL AND items.id ` + cmp + ` ?)`, []any{req.After.ID}, nil
				}
				currency, amount, ok := parsePrice(req.After.Value)
				if !ok {
					return "", nil, page.ErrBadCursor
				}
				return `(items.price_minor IS NULL OR (items.currency ` + cmp + ` ? OR (items.currency = ? AND
					(items.price_minor ` + cmp + ` ? OR (items.price_minor = ? AND items.id ` + cmp + ` ?)))))`,
					[]any{currency, currency, amount, amount, req.After.ID}, nil
			}
	default: // position
		return "items.position " + dir + ", items.id " + dir, func() (string, []any, error) {
			n, err := strconv.ParseInt(req.After.Value, 10, 64)
			if err != nil {
				return "", nil, page.ErrBadCursor
			}
			return `(items.position ` + cmp + ` ? OR (items.position = ? AND items.id ` + cmp + ` ?))`,
				[]any{n, n, req.After.ID}, nil
		}
	}
}

// priceNone is the value of the position of an item with no price in a sort by price.
const priceNone = "-"

func priceValue(it Item) string {
	if it.PriceMinor == nil {
		return priceNone
	}
	return *it.Currency + ":" + strconv.FormatInt(*it.PriceMinor, 10)
}

func parsePrice(v string) (currency string, amount int64, ok bool) {
	currency, raw, found := strings.Cut(v, ":")
	if !found || !validCurrency(currency) {
		return "", 0, false
	}
	amount, err := strconv.ParseInt(raw, 10, 64)
	return currency, amount, err == nil
}

// Visible returns one page of the items on a list that the viewer can see, and
// how many items they can see on it in all. It returns [list.ErrNotFound] when
// the viewer can't see the list.
func (s *Service) Visible(ctx context.Context, viewer user.User, listID int64, req page.Request) (items []Item, total int, next *string, err error) {
	if _, err := list.GetVisible(ctx, s.db, viewer, listID); err != nil {
		return nil, 0, nil, err
	}
	seen, seenArgs := list.VisibleTo(viewer)
	shown, shownArgs := list.ItemShownTo(viewer)
	where := `items.list_id = ? AND ` + seen + ` AND ` + shown
	whereArgs := slices.Concat([]any{listID}, seenArgs, shownArgs)
	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM items JOIN lists ON lists.id = items.list_id WHERE `+where, whereArgs...); err != nil {
		return nil, 0, nil, fmt.Errorf("count items: %w", err)
	}

	order, after := orderBy(req)
	query, args := selectSQL(viewer)
	query += ` WHERE ` + where
	args = append(args, whereArgs...)
	if req.After != nil {
		cond, condArgs, err := after()
		if err != nil {
			return nil, 0, nil, err
		}
		query += ` AND ` + cond
		args = append(args, condArgs...)
	}
	query += ` ORDER BY ` + order + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []row
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list items: %w", err)
	}
	all := make([]Item, len(rows))
	for i, r := range rows {
		all[i] = r.item()
	}
	if err := loadExtras(ctx, s.db, all); err != nil {
		return nil, 0, nil, err
	}
	items, next = page.Slice(req, all, func(it Item) page.Position {
		switch req.Key {
		case "created_at":
			return page.Position{Value: page.FormatTime(it.CreatedAt), ID: it.ID}
		case "price":
			return page.Position{Value: priceValue(it), ID: it.ID}
		}
		return page.Position{Value: strconv.Itoa(it.Position), ID: it.ID}
	})
	return items, total, next, nil
}

// Guard runs inside the transaction of [Service.Update] and [Service.Delete],
// before the write, with the item as the viewer sees it. q is the transaction.
// An error from it stops the write.
type Guard func(ctx context.Context, q sqlx.ExtContext, current Item) error

// Add puts a new item at the end of a list that the viewer can edit, and
// returns it. It returns [list.ErrNotFound] for a list the viewer can't see,
// [list.ErrNotEditable] for one they see and can't edit, [ErrInvalid],
// [ErrTooManyStoreLinks], and [ErrTooManyImages]. guard, if there is one, runs
// inside the transaction before the item is written.
func (s *Service) Add(ctx context.Context, viewer user.User, listID int64, n New, guard AddGuard) (Item, error) {
	f, err := n.fields()
	if err != nil {
		return Item{}, err
	}
	if len(n.StoreLinks) > MaxStoreLinks {
		return Item{}, ErrTooManyStoreLinks
	}
	links := make([]NewStoreLink, len(n.StoreLinks))
	for i, l := range n.StoreLinks {
		if links[i], err = cleanStoreLink(l, i); err != nil {
			return Item{}, err
		}
		// An item with no price starts from the price of its first priced store
		// link, whether the input left the price out or sent it as null: a client
		// that sends a search result on unchanged has no price for the item,
		// because the page's price is on the store link (see [clearedSQL]). An
		// item with a price, or a range, keeps it.
		if f.priceMinor == nil && f.topPriceMinor == nil && links[i].PriceMinor != nil {
			price, currency := *links[i].PriceMinor, links[i].Currency
			f.priceMinor, f.currency = &price, &currency
		}
	}
	n.StoreLinks = links
	primary, err := cleanImages(n.ImageIDs, n.PrimaryImageID)
	if err != nil {
		return Item{}, err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	l, err := list.GetVisible(ctx, tx, viewer, listID)
	if err != nil {
		return Item{}, err
	}
	if !l.CanEdit {
		return Item{}, list.NotEditable(ctx, tx, viewer, l.ID)
	}
	if guard != nil {
		if err := guard(ctx, tx); err != nil {
			return Item{}, err
		}
	}
	if n.ImageFrom != nil && len(n.ImageIDs) == 0 {
		imageID, ok, err := n.ImageFrom(ctx, tx)
		if err != nil {
			return Item{}, err
		}
		if ok {
			n.ImageIDs, primary = []int64{imageID}, 0
		}
	}
	var position int
	if err := tx.GetContext(ctx, &position, `SELECT count(*) FROM items WHERE list_id = ? AND deleted_at IS NULL`, listID); err != nil {
		return Item{}, fmt.Errorf("count items: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO items (list_id, title, notes, price_minor, top_price_minor, currency, quantity, is_unlimited, position, added_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		listID, f.title, f.notes, f.priceMinor, f.topPriceMinor, f.currency, f.quantity, f.isUnlimited, position, viewer.ID, s.now())
	if err != nil {
		return Item{}, fmt.Errorf("insert item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Item{}, fmt.Errorf("insert item: %w", err)
	}
	if err := insertExtras(ctx, tx, id, n, primary, s.now()); err != nil {
		return Item{}, err
	}
	it, err := get(ctx, tx, viewer, id)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit: %w", err)
	}
	return it, nil
}

// Update applies a merge patch to an item that the viewer can change, and
// returns the item as it is after. It returns [ErrNotFound] for an item the
// viewer can't see, [list.ErrNotEditable] for one they see and can't change,
// [ErrInvalid], and for a move [ErrTargetNotFound] and [ErrCannotEditTarget].
// guard runs inside the same transaction as the write: an If-Match check goes
// there.
//
// The other items on the list shift to make room for a new position and to
// close the gap an item leaves, so positions stay 0 to n-1. A move to another
// list puts the item at the end of it, unless the changes give a position too.
func (s *Service) Update(ctx context.Context, viewer user.User, id int64, c Changes, guard Guard) (Item, error) {
	if c.Position.Set && (c.Position.Value == nil || *c.Position.Value < 0) {
		return Item{}, invalid("position", "the position is an index of 0 or more")
	}
	if c.ListID.Set && c.ListID.Value == nil {
		return Item{}, invalid("list_id", "the list can't be null")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := get(ctx, tx, viewer, id)
	if err != nil {
		return Item{}, err
	}
	if !current.CanEdit {
		return Item{}, list.NotEditable(ctx, tx, viewer, current.ListID)
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return Item{}, err
		}
	}
	f, err := c.apply(current)
	if err != nil {
		return Item{}, err
	}
	// What the edit changes for the item's open gifts (docs/spec.md, Items, Who
	// changes an item). The price rule reads price_cleared before the write.
	had, err := hadPrice(ctx, tx, id)
	if err != nil {
		return Item{}, err
	}
	changes := editChanges(current, f, had)
	if _, err := tx.ExecContext(ctx,
		`UPDATE items SET `+clearedSQL+`, title = ?, notes = ?, price_minor = ?, top_price_minor = ?, currency = ?, quantity = ?, is_unlimited = ? WHERE id = ?`,
		f.priceMinor, f.title, f.notes, f.priceMinor, f.topPriceMinor, f.currency, f.quantity, f.isUnlimited, id); err != nil {
		return Item{}, fmt.Errorf("update item: %w", err)
	}
	if c.ImageIDs.Set || c.PrimaryImageID.Set {
		primary, err := replaceImages(ctx, tx, current, c, s.now())
		if err != nil {
			return Item{}, err
		}
		// Only a new primary image flags: a change to another image is not part of
		// the gift.
		var was *int64
		if current.PrimaryImage != nil {
			was = &current.PrimaryImage.ID
		}
		if !equalPtr(was, primary) {
			changes = append(changes, giftflag.Change{Field: giftflag.FieldPrimaryImage, Old: giftflag.Value(deref(was)), New: giftflag.Value(deref(primary))})
		}
	}
	if err := giftflag.ItemChanged(ctx, tx, id, changes, s.now()); err != nil {
		return Item{}, err
	}

	listID, position := current.ListID, current.Position
	if c.ListID.Set && *c.ListID.Value != current.ListID {
		target, err := list.GetVisible(ctx, tx, viewer, *c.ListID.Value)
		if errors.Is(err, list.ErrNotFound) {
			return Item{}, ErrTargetNotFound
		}
		if err != nil {
			return Item{}, err
		}
		if !target.CanEdit {
			if err := list.NotEditable(ctx, tx, viewer, target.ID); errors.Is(err, list.ErrRecipientDeactivated) {
				return Item{}, err
			}
			return Item{}, ErrCannotEditTarget
		}
		// Close the gap on the old list, and take the last place on the new one.
		if err := shiftAfter(ctx, tx, current.ListID, current.Position, -1); err != nil {
			return Item{}, err
		}
		if err := tx.GetContext(ctx, &position, `SELECT count(*) FROM items WHERE list_id = ? AND deleted_at IS NULL`, target.ID); err != nil {
			return Item{}, fmt.Errorf("count items: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE items SET list_id = ?, position = ? WHERE id = ?`, target.ID, position, id); err != nil {
			return Item{}, fmt.Errorf("move item: %w", err)
		}
		listID = target.ID
		// The open gifts follow the item, each flagged moved.
		if err := giftflag.ItemMoved(ctx, tx, id, target.ID, s.now()); err != nil {
			return Item{}, err
		}
		if s.observer != nil {
			if err := s.observer.Moved(ctx, tx, id); err != nil {
				return Item{}, err
			}
		}
	}
	if c.Position.Set {
		if err := moveTo(ctx, tx, listID, id, position, *c.Position.Value); err != nil {
			return Item{}, err
		}
	}

	updated, err := get(ctx, tx, viewer, id)
	if err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit: %w", err)
	}
	return updated, nil
}

// shiftAfter moves every item after a position on a list by delta places.
func shiftAfter(ctx context.Context, tx *sqlx.Tx, listID int64, position, delta int) error {
	if _, err := tx.ExecContext(ctx, `UPDATE items SET position = position + ? WHERE list_id = ? AND deleted_at IS NULL AND position > ?`, delta, listID, position); err != nil {
		return fmt.Errorf("shift items: %w", err)
	}
	return nil
}

// moveTo moves an item that is at index from on its list to index to (the last
// index if to is past it), and shifts the items in between.
func moveTo(ctx context.Context, tx *sqlx.Tx, listID, id int64, from, to int) error {
	var count int
	if err := tx.GetContext(ctx, &count, `SELECT count(*) FROM items WHERE list_id = ? AND deleted_at IS NULL`, listID); err != nil {
		return fmt.Errorf("count items: %w", err)
	}
	to = min(to, count-1)
	switch {
	case to < from:
		if _, err := tx.ExecContext(ctx, `UPDATE items SET position = position + 1 WHERE list_id = ? AND deleted_at IS NULL AND position >= ? AND position < ?`, listID, to, from); err != nil {
			return fmt.Errorf("shift items: %w", err)
		}
	case to > from:
		if _, err := tx.ExecContext(ctx, `UPDATE items SET position = position - 1 WHERE list_id = ? AND deleted_at IS NULL AND position > ? AND position <= ?`, listID, from, to); err != nil {
			return fmt.Errorf("shift items: %w", err)
		}
	default:
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET position = ? WHERE id = ?`, to, id); err != nil {
		return fmt.Errorf("move item: %w", err)
	}
	return nil
}

// Delete removes an item that the viewer can change, and closes the gap in the
// positions of the list. It returns [ErrNotFound] for an item the viewer can't
// see and [list.ErrNotEditable] for one they see and can't change. guard runs
// inside the same transaction as the write.
func (s *Service) Delete(ctx context.Context, viewer user.User, id int64, guard Guard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := get(ctx, tx, viewer, id)
	if err != nil {
		return err
	}
	if !current.CanEdit {
		return list.NotEditable(ctx, tx, viewer, current.ListID)
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return err
		}
	}
	if err := s.remove(ctx, tx, id); err != nil {
		return err
	}
	if err := shiftAfter(ctx, tx, current.ListID, current.Position, -1); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// remove deletes one item, in the transaction of the delete. It checks nothing
// and doesn't close the gap in the positions. It is the one place that removes an
// item, for [Service.Delete] and [Service.RemoveAllOn], so the gift effects of a
// delete live here.
//
// An item that has gifts is deleted softly (docs/spec.md, Items, Who changes
// an item): it keeps its row, its images and its store links, so the gifts keep
// what they were given, and nobody sees it again ([list.ItemShownTo]). The last
// gift to go purges it ([PurgeIfUnused]). An item with no gift is deleted for
// good: its images go to the sweep, and its store links go with it.
func (s *Service) remove(ctx context.Context, tx *sqlx.Tx, id int64) error {
	var kept bool
	if err := tx.GetContext(ctx, &kept, `SELECT EXISTS (SELECT 1 FROM gifts WHERE item_id = ?)`, id); err != nil {
		return fmt.Errorf("find the gifts of an item: %w", err)
	}
	if kept {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET deleted_at = ? WHERE id = ?`, s.now(), id); err != nil {
			return fmt.Errorf("soft delete item: %w", err)
		}
		// Each open gift is flagged removed: its giver keeps it or unclaims it.
		return giftflag.ItemRemoved(ctx, tx, id, s.now())
	}
	return purge(ctx, tx, id, s.now())
}

// purge deletes an item for good, and detaches its images.
func purge(ctx context.Context, tx *sqlx.Tx, id int64, now time.Time) error {
	if err := detachImages(ctx, tx, id, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete item: %w", err)
	}
	return nil
}

// PurgeIfUnused deletes an item that was deleted softly, once no gift is left
// on it. The package of gifts calls it in the transaction that removes a gift.
// It does nothing for an item that is live or still has a gift.
func PurgeIfUnused(ctx context.Context, tx *sqlx.Tx, id int64, now time.Time) error {
	var listID int64
	err := tx.GetContext(ctx, &listID, `SELECT list_id FROM items WHERE id = ? AND deleted_at IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM gifts WHERE item_id = items.id)`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find an unused deleted item: %w", err)
	}
	if err := purge(ctx, tx, id, now); err != nil {
		return err
	}
	// A list that was deleted while its items had gifts goes with the last of them.
	return list.PurgeIfUnused(ctx, tx, listID)
}

// Given takes the quantity of a gift that was just given off an item, in the
// transaction of the reveal (docs/spec.md, Gifts, Reveal). It does nothing to
// an unlimited item, and nothing to an item that is already deleted: a user's
// delete keeps the quantity the item had, and an item that left its list when its
// whole quantity was given has 0 already. So a deleted item with quantity 0 is
// exactly an item that left its list because it was given (see [Restored]).
//
// An item with quantity left stays on its list. An item whose whole quantity was
// given gets quantity 0 and leaves its list as a delete does, softly, and the
// items after it close the gap; a later restore puts the quantity back and the
// item on its list again. The gifts that are still open on an item that leaves
// its list are flagged removed, as a user's delete flags them: the item is gone
// from the list, and their givers keep the gift or unclaim it. An item that stays
// flags nothing: the open gifts are for the quantity that is left.
func Given(ctx context.Context, tx *sqlx.Tx, id int64, quantity int, now time.Time) error {
	var it struct {
		ListID      int64        `db:"list_id"`
		Position    int          `db:"position"`
		Quantity    int          `db:"quantity"`
		IsUnlimited bool         `db:"is_unlimited"`
		DeletedAt   sql.NullTime `db:"deleted_at"`
	}
	if err := tx.GetContext(ctx, &it, `SELECT list_id, position, quantity, is_unlimited, deleted_at FROM items WHERE id = ?`, id); err != nil {
		return fmt.Errorf("find the item of a given gift: %w", err)
	}
	if it.IsUnlimited || it.DeletedAt.Valid {
		return nil
	}
	left := max(it.Quantity-quantity, 0)
	if _, err := tx.ExecContext(ctx, `UPDATE items SET quantity = ? WHERE id = ?`, left, id); err != nil {
		return fmt.Errorf("take the given quantity off an item: %w", err)
	}
	if left > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET deleted_at = ? WHERE id = ?`, now.UTC(), id); err != nil {
		return fmt.Errorf("take an item off its list: %w", err)
	}
	if err := shiftAfter(ctx, tx, it.ListID, it.Position, -1); err != nil {
		return err
	}
	return giftflag.ItemRemoved(ctx, tx, id, now)
}

// Restored puts the quantity of a gift that a restore opened again back on its
// item, in the transaction of the restore (docs/spec.md, Gifts, Restore). It
// reports whether the item is still there to take it: an item that a user deleted
// stays deleted, and so does the item of a list that was deleted. Then it changes
// nothing.
//
// It works from what the item has left. An unlimited item needs no quantity back.
// An item on its list gets the quantity added to what it has. An item that left
// its list because its whole quantity was given (deleted, with quantity 0) gets
// the quantity as its whole quantity and is on its list again, in the last place.
// The open gifts that were flagged removed when it left are no longer removed.
func Restored(ctx context.Context, tx *sqlx.Tx, id int64, quantity int) (there bool, err error) {
	var it struct {
		ListID      int64        `db:"list_id"`
		Quantity    int          `db:"quantity"`
		IsUnlimited bool         `db:"is_unlimited"`
		DeletedAt   sql.NullTime `db:"deleted_at"`
		ListDeleted bool         `db:"list_deleted"`
	}
	if err := tx.GetContext(ctx, &it, `SELECT items.list_id, items.quantity, items.is_unlimited, items.deleted_at,
		lists.deleted_at IS NOT NULL AS list_deleted
		FROM items JOIN lists ON lists.id = items.list_id WHERE items.id = ?`, id); err != nil {
		return false, fmt.Errorf("find the item of a restored gift: %w", err)
	}
	switch {
	case it.ListDeleted:
		return false, nil
	case it.IsUnlimited && !it.DeletedAt.Valid:
		return true, nil
	case it.DeletedAt.Valid && (it.IsUnlimited || it.Quantity != 0):
		return false, nil
	}
	if !it.DeletedAt.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET quantity = quantity + ? WHERE id = ?`, quantity, id); err != nil {
			return false, fmt.Errorf("put the quantity back on an item: %w", err)
		}
		return true, nil
	}
	var position int
	if err := tx.GetContext(ctx, &position, `SELECT count(*) FROM items WHERE list_id = ? AND deleted_at IS NULL`, it.ListID); err != nil {
		return false, fmt.Errorf("count items: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE items SET quantity = ?, deleted_at = NULL, position = ? WHERE id = ?`, quantity, position, id); err != nil {
		return false, fmt.Errorf("put an item back on its list: %w", err)
	}
	return true, giftflag.ClearRemoved(ctx, tx, id)
}

// RemoveAllOn deletes every item on a list, in the transaction that deletes the
// list (docs/spec.md, Lists, Leaving and deleting), each as a delete of the
// item does ([Service.remove]). It doesn't close the gaps in the positions,
// because the list goes too. It checks nothing: the caller has checked that the
// viewer may delete the list.
func (s *Service) RemoveAllOn(ctx context.Context, tx *sqlx.Tx, listID int64) error {
	var ids []int64
	if err := tx.SelectContext(ctx, &ids, `SELECT id FROM items WHERE list_id = ? AND deleted_at IS NULL`, listID); err != nil {
		return fmt.Errorf("find the items of a list: %w", err)
	}
	for _, id := range ids {
		if err := s.remove(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
