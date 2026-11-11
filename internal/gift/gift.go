// Package gift owns the gifts table: a giver's claim on an item, and the gift
// that it starts. A gift is one record for its whole life, from the claim to the
// recipients' Received (ADR 0011). While it is open, only its giver sees it. At
// reveal ([Revealer]) it is given: it keeps a copy of what was given and the users
// it was given to, and those users and their parents see it.
//
// Who can see a gift is in rules.go, and nowhere else. A gift has a `given_at`
// from the start, and a restore clears it. Its flags are in package
// giftflag, which item and list changes write; this package reads them and lets
// the giver clear them ([Service.Acknowledge]).
//
// See the layout notes in package user.
package gift

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/user"
)

const (
	// MaxFromLabelLength is the longest from label, in characters.
	MaxFromLabelLength = 60
	// MaxNoteLength is the longest private note, in characters.
	MaxNoteLength = 2000
	// AttentionDays is how near a reveal must be for an open gift that is not
	// bought to need attention.
	AttentionDays = 14
)

var (
	// ErrNotFound means no gift has the ID, or the viewer can't see it. For a
	// claim it means there is no such item, or the viewer can't see its list.
	ErrNotFound = errors.New("no such gift")
	// ErrNotEditable means the viewer sees the gift and can't change it: it is
	// given.
	ErrNotEditable = errors.New("you can't change this gift")
	// ErrNotRestorable means the viewer sees the gift and is not one of its
	// recipients or their parent, or the gift is open: only the people it was
	// given to say that they didn't get it.
	ErrNotRestorable = errors.New("you can't restore this gift")
	// ErrAddAgainNotAllowed means the viewer sees the gift and is not one of its
	// recipients or their parent, so they can't add it again: the gift they gave.
	ErrAddAgainNotAllowed = errors.New("you can't add this gift again")
	// ErrGiven means the gift is given, so it can't be unclaimed.
	ErrGiven = errors.New("the gift is given")
	// ErrRecipientCannotClaim means the viewer is a recipient of the list, and a
	// recipient never claims on it.
	ErrRecipientCannotClaim = errors.New("a recipient can't claim an item on their own list")
	// ErrUserDeactivated means the viewer can see the list only through a
	// deactivated recipient, so there is nobody to give to.
	ErrUserDeactivated = errors.New("the recipient is deactivated")
	// ErrFullyClaimed means not as many as the quantity are still wanted.
	ErrFullyClaimed = errors.New("not that many are still wanted")
	// ErrAlreadyClaimed means the giver already has an open gift on the item for
	// the occasion.
	ErrAlreadyClaimed = errors.New("you already claimed this item for that occasion")
	// ErrOccasionNotAllowed means the occasion is not one that the giver may pick
	// for a gift on the list.
	ErrOccasionNotAllowed = errors.New("that occasion is not one of the recipients' occasions that you can pick")
	// ErrOccasionLocked means the giver sees none of the recipients other than
	// themselves, so the server picks the gift's occasion and the giver can't.
	ErrOccasionLocked = errors.New("you can't pick an occasion: you can see none of the other recipients")
	// ErrQuantityRaiseNotAllowed means the giver is a recipient of the list, so
	// they can lower the quantity of a gift and never raise it.
	ErrQuantityRaiseNotAllowed = errors.New("a recipient can't raise the quantity of a gift")
	// ErrInvalid means the input failed a rule. The message is safe to show.
	ErrInvalid = errors.New("invalid input")
)

type invalidError struct{ msg string }

func (e invalidError) Error() string        { return e.msg }
func (e invalidError) Is(target error) bool { return target == ErrInvalid }

// FieldError says which input field failed a rule. It matches [ErrInvalid].
type FieldError struct {
	// Field is the name of the field in the input, such as "from_label".
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

func invalid(field, format string, args ...any) error {
	return &FieldError{Field: field, Err: invalidError{fmt.Sprintf(format, args...)}}
}

// State is where a gift is in its life.
type State string

const (
	// StateOpen is a gift that is claimed and not given yet.
	StateOpen State = "open"
	// StateGiven is a gift that reveal gave to its recipients.
	StateGiven State = "given"
)

// OccasionSummary is an occasion as a gift holds it (docs/routes.md, Summary
// shapes).
type OccasionSummary struct {
	ID   int64
	Kind occasion.Type
	Name string
}

// Flag is a flag on an open gift, as its giver sees it (docs/routes.md, Gift
// fields).
type Flag struct {
	Code string
	// Changes is the diff of a changed flag.
	Changes []giftflag.Change
	// List is the new list of a moved flag, or nil when the giver can't see it.
	List *user.ListSummary
	// OldRecipients and NewRecipients are the users a recipients_changed flag
	// names. ShowRecipients is false when the giver can't see the list: the flag
	// then names nobody.
	OldRecipients, NewRecipients []user.Summary
	ShowRecipients               bool
	// By is the user who restored the gift, or nil when the giver can't see that
	// user (a restored flag).
	By *user.Summary
}

// Gift is a gift as the viewer sees it.
type Gift struct {
	ID       int64
	State    State
	Quantity int

	// ItemID is the item, which may have been deleted softly. Title, Image and the
	// price are the item's now, or the copy from the last reveal once the gift has
	// one (a given gift).
	ItemID        int64
	Title         string
	Image         *image.Summary
	PriceMinor    *int64
	TopPriceMinor *int64
	Currency      *string
	List          user.ListSummary

	// Recipients are the list's recipients now, without the giver, for an open gift,
	// and the users it was given to for a given one. ShowRecipients is false when
	// the field is absent: the viewer is the giver, and could not see the list (now,
	// or at reveal).
	Recipients     []user.Summary
	ShowRecipients bool

	// Giver is nil once the giver is deleted. GiverName is the name as it was.
	Giver     *user.Summary
	GiverName string

	FromLabel *string
	// Occasion and OccasionName are nil when the viewer may not see the
	// occasion: only the reveal date shows then. OccasionDate is the date the
	// occasion fell on when it was picked.
	Occasion     *OccasionSummary
	OccasionName *string
	OccasionDate occasion.Date
	// RevealAt is 00:00 on the day after the occasion, in the zone of the last
	// recipient to reach it.
	RevealAt time.Time
	GivenAt  *time.Time

	// IsGiver is true when the viewer is the giver. The fields below are the
	// giver's own: for anyone else they are zero, because a recipient and a parent
	// never see them, and the zero value says nothing.
	IsGiver  bool
	IsBought bool
	Note     *string
	// Flags is what happened to the gift that its giver should answer, oldest
	// first. It is never nil. A given gift has none.
	Flags []Flag
	// NeedsAttention is true for an open gift that is not bought and is revealed
	// within [AttentionDays], and for one flagged changed or removed.
	NeedsAttention bool
	CreatedAt      time.Time

	// CanEdit is the viewer's power to change and to unclaim the gift.
	// CanRaiseQuantity is true when the giver may raise the quantity.
	// CanAcknowledge is true when the viewer can edit the gift and it has flags.
	CanEdit          bool
	CanRaiseQuantity bool
	CanAcknowledge   bool
	// CanRestore is true when the viewer receives the gift (a recipient, or a
	// parent of one), and so can restore it and add it again. CanAddAgain is the
	// same value.
	CanRestore bool

	// occasionID is the occasion as stored, also when the viewer may not see it.
	occasionID sql.NullInt64
}

// SortKeys are the sort keys of the gifts collection (docs/routes.md, Gift
// routes). The first is the default.
var SortKeys = []page.Key{{Name: "reveal_at", Time: true}, {Name: "given_at", Time: true}}

// Filter narrows the gifts collection. A nil field is no filter.
type Filter struct {
	State      *State
	GiverID    *int64
	ItemID     *int64
	ListID     *int64
	OccasionID *int64
	// RecipientID keeps the given gifts that have this user among the recipients
	// the viewer sees.
	RecipientID *int64
	// NeedsAttention keeps the gifts whose `needs_attention` is this value.
	NeedsAttention *bool
}

// Service reads and changes gifts.
type Service struct {
	db  *sqlx.DB
	occ *occasion.Service
	now func() time.Time
}

// NewService returns a Service. occ picks a gift's occasion. now is the clock, so
// tests control time.
func NewService(db *sqlx.DB, occ *occasion.Service, now func() time.Time) *Service {
	return &Service{db: db, occ: occ, now: func() time.Time { return now().UTC() }}
}

type row struct {
	ID           int64          `db:"id"`
	ItemID       int64          `db:"item_id"`
	GiverID      sql.NullInt64  `db:"giver_id"`
	Quantity     int            `db:"quantity"`
	OccasionID   sql.NullInt64  `db:"occasion_id"`
	OccasionDate string         `db:"occasion_date"`
	FromLabel    sql.NullString `db:"from_label"`
	Note         sql.NullString `db:"note"`
	IsBought     bool           `db:"is_bought"`
	CreatedAt    time.Time      `db:"created_at"`
	GivenAt      sql.NullTime   `db:"given_at"`

	ListID        int64          `db:"list_id"`
	ListName      string         `db:"list_name"`
	Title         string         `db:"title"`
	PriceMinor    sql.NullInt64  `db:"price_minor"`
	TopPriceMinor sql.NullInt64  `db:"top_price_minor"`
	Currency      sql.NullString `db:"currency"`
	ItemQuantity  int            `db:"item_quantity"`
	IsUnlimited   bool           `db:"is_unlimited"`
	Claimed       int            `db:"claimed"`
	ImageID       sql.NullInt64  `db:"image_id"`
	ImageWidth    sql.NullInt64  `db:"image_width"`
	ImageHeight   sql.NullInt64  `db:"image_height"`

	// The copy that reveal made, NULL while the gift has none.
	CopyTitle         sql.NullString `db:"copy_title"`
	CopyPriceMinor    sql.NullInt64  `db:"copy_price_minor"`
	CopyTopPriceMinor sql.NullInt64  `db:"copy_top_price_minor"`
	CopyCurrency      sql.NullString `db:"copy_currency"`
	CopyImageID       sql.NullInt64  `db:"copy_image_id"`
	CopyImageWidth    sql.NullInt64  `db:"copy_image_width"`
	CopyImageHeight   sql.NullInt64  `db:"copy_image_height"`
	CopyOccasionName  sql.NullString `db:"copy_occasion_name"`
	GiverSawList      sql.NullBool   `db:"giver_saw_list"`
	GiverSawOccasion  sql.NullBool   `db:"giver_saw_occasion"`

	GiverName    sql.NullString `db:"giver_name"`
	AvatarID     sql.NullInt64  `db:"avatar_image_id"`
	AvatarWidth  sql.NullInt64  `db:"avatar_width"`
	AvatarHeight sql.NullInt64  `db:"avatar_height"`
	OccasionKind sql.NullString `db:"occasion_kind"`
	OccasionName sql.NullString `db:"occasion_name"`
}

// selectSQL selects a gift with the item, the list, the giver, and the occasion
// that it points at. The gift's table is `gifts`, so [VisibleTo] applies as it is.
const selectSQL = `SELECT gifts.id, gifts.item_id, gifts.giver_id, gifts.quantity, gifts.occasion_id, gifts.occasion_date,
	gifts.from_label, gifts.note, gifts.is_bought, gifts.created_at, gifts.given_at,
	items.list_id, lists.name AS list_name, items.title, items.price_minor, items.top_price_minor, items.currency,
	items.quantity AS item_quantity, items.is_unlimited,
	COALESCE((SELECT SUM(og.quantity) FROM gifts og WHERE og.item_id = gifts.item_id AND og.given_at IS NULL), 0) AS claimed,
	primary_image.image_id, primary_images.width AS image_width, primary_images.height AS image_height,
	gifts.title AS copy_title, gifts.price_minor AS copy_price_minor, gifts.top_price_minor AS copy_top_price_minor,
	gifts.currency AS copy_currency, gifts.image_id AS copy_image_id, copy_images.width AS copy_image_width,
	copy_images.height AS copy_image_height, gifts.occasion_name AS copy_occasion_name,
	gifts.giver_saw_list, gifts.giver_saw_occasion,
	COALESCE(giver.name, gifts.giver_name) AS giver_name, giver.avatar_image_id, giver_avatars.width AS avatar_width, giver_avatars.height AS avatar_height,
	occasions.kind AS occasion_kind, occasions.name AS occasion_name
	FROM gifts
	JOIN items ON items.id = gifts.item_id
	JOIN lists ON lists.id = items.list_id
	LEFT JOIN item_images primary_image ON primary_image.item_id = items.id AND primary_image.is_primary = 1
	LEFT JOIN images primary_images ON primary_images.id = primary_image.image_id
	LEFT JOIN images copy_images ON copy_images.id = gifts.image_id
	LEFT JOIN users giver ON giver.id = gifts.giver_id
	LEFT JOIN images giver_avatars ON giver_avatars.id = giver.avatar_image_id
	LEFT JOIN occasions ON occasions.id = gifts.occasion_id`

func nilIfInvalid[T any](v T, ok bool) *T {
	if !ok {
		return nil
	}
	return &v
}

// build turns the rows into gifts as the viewer sees it. It reads the
// recipients, the zones, and what the viewer may see, once for all the rows. q is
// a database or a transaction.
func (s *Service) build(ctx context.Context, q sqlx.QueryerContext, viewer user.User, rows []row) ([]Gift, error) {
	if len(rows) == 0 {
		return []Gift{}, nil
	}
	var listIDs, givenIDs []int64
	for _, r := range rows {
		if !slices.Contains(listIDs, r.ListID) {
			listIDs = append(listIDs, r.ListID)
		}
		if r.GivenAt.Valid {
			givenIDs = append(givenIDs, r.ID)
		}
	}
	recipients, err := list.RecipientsOf(ctx, q, listIDs)
	if err != nil {
		return nil, err
	}
	given, err := givenRecipients(ctx, q, givenIDs)
	if err != nil {
		return nil, err
	}
	zones, err := recipientZones(ctx, q, listIDs)
	if err != nil {
		return nil, err
	}
	seenLists, mine, err := listSight(ctx, q, viewer, listIDs)
	if err != nil {
		return nil, err
	}
	pickable := map[int64]map[int64]bool{}
	for _, id := range listIDs {
		if !seenLists[id] {
			continue
		}
		if pickable[id], err = occasion.PickableIDs(ctx, q, viewer, id); err != nil {
			return nil, err
		}
	}

	flags, err := loadFlags(ctx, q, viewer, rows, seenLists)
	if err != nil {
		return nil, err
	}
	restorable, err := CanRestoreOf(ctx, q, viewer, givenIDs)
	if err != nil {
		return nil, err
	}

	now := s.now()
	out := make([]Gift, len(rows))
	for i, r := range rows {
		isGiver := r.GiverID.Valid && r.GiverID.Int64 == viewer.ID
		g := Gift{
			ID: r.ID, State: StateOpen, Quantity: r.Quantity, ItemID: r.ItemID, Title: r.Title,
			PriceMinor: nilIfInvalid(r.PriceMinor.Int64, r.PriceMinor.Valid), TopPriceMinor: nilIfInvalid(r.TopPriceMinor.Int64, r.TopPriceMinor.Valid),
			Currency:  nilIfInvalid(r.Currency.String, r.Currency.Valid),
			List:      user.ListSummary{ID: r.ListID, Name: r.ListName},
			FromLabel: nilIfInvalid(r.FromLabel.String, r.FromLabel.Valid), GiverName: r.GiverName.String,
			IsGiver: isGiver,
		}
		if r.ImageID.Valid {
			g.Image = &image.Summary{ID: r.ImageID.Int64, Width: int(r.ImageWidth.Int64), Height: int(r.ImageHeight.Int64)}
		}
		if r.CopyTitle.Valid {
			// A gift that was given keeps what was given, however the item changes.
			g.Title = r.CopyTitle.String
			g.PriceMinor, g.TopPriceMinor = nilIfInvalid(r.CopyPriceMinor.Int64, r.CopyPriceMinor.Valid), nilIfInvalid(r.CopyTopPriceMinor.Int64, r.CopyTopPriceMinor.Valid)
			g.Currency = nilIfInvalid(r.CopyCurrency.String, r.CopyCurrency.Valid)
			g.Image = nil
			if r.CopyImageID.Valid {
				g.Image = &image.Summary{ID: r.CopyImageID.Int64, Width: int(r.CopyImageWidth.Int64), Height: int(r.CopyImageHeight.Int64)}
			}
		}
		if isGiver {
			g.IsBought, g.Note, g.CreatedAt = r.IsBought, nilIfInvalid(r.Note.String, r.Note.Valid), r.CreatedAt
		}
		if r.GivenAt.Valid {
			g.State, g.GivenAt = StateGiven, &r.GivenAt.Time
		}
		if r.GiverID.Valid {
			g.Giver = &user.Summary{ID: r.GiverID.Int64, Name: r.GiverName.String}
			if r.AvatarID.Valid {
				g.Giver.Avatar = &image.Summary{ID: r.AvatarID.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
			}
		}
		switch {
		case g.State == StateGiven:
			// The users it was given to. Its giver sees them only if they could see the
			// list at reveal. A recipient and a parent see them all.
			g.ShowRecipients = !isGiver || r.GiverSawList.Bool
			g.Recipients = given[r.ID]
		case seenLists[r.ListID]:
			// A gift is never for its giver, so a giver who is a recipient is left out.
			g.ShowRecipients = true
			g.Recipients = slices.DeleteFunc(slices.Clone(recipients[r.ListID]), func(u user.Summary) bool { return r.GiverID.Valid && u.ID == r.GiverID.Int64 })
		}
		date, err := occasion.ParseDate(r.OccasionDate)
		if err != nil {
			return nil, fmt.Errorf("gift %d has a bad occasion date %q: %w", r.ID, r.OccasionDate, err)
		}
		g.OccasionDate, g.occasionID = date, r.OccasionID
		g.RevealAt = revealAt(date, zones[r.ListID], r.GiverID, s.occ.FallbackZone())
		switch {
		case g.State == StateGiven:
			// The name is the copy from reveal. The summary stays while the occasion
			// exists. A giver who could not see the occasion at reveal sees neither.
			if !isGiver || r.GiverSawOccasion.Bool {
				g.OccasionName = nilIfInvalid(r.CopyOccasionName.String, r.CopyOccasionName.Valid)
				if r.OccasionID.Valid {
					g.Occasion = &OccasionSummary{ID: r.OccasionID.Int64, Kind: occasion.Type(r.OccasionKind.String), Name: r.OccasionName.String}
				}
			}
		case r.OccasionID.Valid && pickable[r.ListID][r.OccasionID.Int64]:
			// A giver sees the occasion only while it is one of a recipient they can
			// see, and one they can see themselves (docs/routes.md, Gift fields).
			g.Occasion = &OccasionSummary{ID: r.OccasionID.Int64, Kind: occasion.Type(r.OccasionKind.String), Name: r.OccasionName.String}
			g.OccasionName = &r.OccasionName.String
		}
		g.CanEdit = canEdit(viewer, nilIfInvalid(r.GiverID.Int64, r.GiverID.Valid), g.State == StateGiven)
		left := r.ItemQuantity - r.Claimed
		g.CanRaiseQuantity = g.CanEdit && !mine[r.ListID] && (r.IsUnlimited || left > 0)
		if g.State == StateOpen && isGiver {
			g.Flags = flags[r.ID]
		}
		if g.Flags == nil {
			g.Flags = []Flag{}
		}
		g.CanAcknowledge = g.CanEdit && len(g.Flags) > 0
		g.CanRestore = restorable[r.ID]
		g.NeedsAttention = g.State == StateOpen && isGiver &&
			((!g.IsBought && g.RevealAt.Before(now.AddDate(0, 0, AttentionDays))) || slices.ContainsFunc(g.Flags, func(f Flag) bool {
				return f.Code == giftflag.Changed || f.Code == giftflag.Removed
			}))
		out[i] = g
	}
	return out, nil
}

// recipientZone is the timezone of a recipient of a list. A nil Zone is a
// recipient with none.
type recipientZone struct {
	UserID int64
	Zone   *string
}

// revealAt is the moment a gift on the occasion date is given: the latest of the
// recipients' midnights after it, so the day has started for every one of them. A
// gift is never for its giver, so the giver's own midnight does not count. A
// recipient with no zone, or one the system doesn't know, is in the fallback zone.
func revealAt(date occasion.Date, zones []recipientZone, giverID sql.NullInt64, fallback *time.Location) time.Time {
	var latest time.Time
	for _, z := range zones {
		if giverID.Valid && z.UserID == giverID.Int64 {
			continue
		}
		loc := fallback
		if z.Zone != nil {
			if l, err := time.LoadLocation(*z.Zone); err == nil {
				loc = l
			}
		}
		if t := occasion.RevealAt(date, loc); t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return occasion.RevealAt(date, fallback).UTC()
	}
	return latest.UTC()
}

// loadFlags returns the flags of the gifts, by gift ID. A moved flag shows the
// new list only when the viewer can see it, and a recipients_changed flag names
// the recipients only when the viewer can see the gift's list. Every gift gets a
// non-nil slice.
func loadFlags(ctx context.Context, q sqlx.QueryerContext, viewer user.User, rows []row, seenLists map[int64]bool) (map[int64][]Flag, error) {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	stored, err := giftflag.Load(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	var listIDs, userIDs, restorers []int64
	for _, fs := range stored {
		for _, f := range fs {
			switch f.Code {
			case giftflag.Moved:
				listIDs = append(listIDs, f.ListID)
			case giftflag.RecipientsChanged:
				userIDs = append(userIDs, f.OldRecipients...)
				userIDs = append(userIDs, f.NewRecipients...)
			case giftflag.Restored:
				restorers = append(restorers, f.By)
			}
		}
	}
	// A restored flag names the user who restored the gift only if the giver can
	// see that user.
	if len(restorers) > 0 {
		seen, args := user.VisibleTo(viewer)
		query, inArgs, err := sqlx.In(`SELECT users.id FROM users WHERE users.id IN (?) AND `+seen, append([]any{restorers}, args...)...)
		if err != nil {
			return nil, fmt.Errorf("restorers: %w", err)
		}
		var visible []int64
		if err := sqlx.SelectContext(ctx, q, &visible, query, inArgs...); err != nil {
			return nil, fmt.Errorf("restorers: %w", err)
		}
		userIDs = append(userIDs, visible...)
		restorers = visible
	}
	people, err := summariesOf(ctx, q, userIDs)
	if err != nil {
		return nil, err
	}
	lists := map[int64]user.ListSummary{}
	if len(listIDs) > 0 {
		seen, args := list.VisibleTo(viewer)
		query, inArgs, err := sqlx.In(`SELECT lists.id, lists.name FROM lists WHERE lists.id IN (?) AND `+seen, append([]any{listIDs}, args...)...)
		if err != nil {
			return nil, fmt.Errorf("moved lists: %w", err)
		}
		var found []user.ListSummary
		if err := sqlx.SelectContext(ctx, q, &found, query, inArgs...); err != nil {
			return nil, fmt.Errorf("moved lists: %w", err)
		}
		for _, l := range found {
			lists[l.ID] = l
		}
	}
	out := make(map[int64][]Flag, len(rows))
	for _, r := range rows {
		flags := []Flag{}
		for _, f := range stored[r.ID] {
			flag := Flag{Code: f.Code, Changes: f.Changes}
			if f.Code == giftflag.Moved {
				if l, ok := lists[f.ListID]; ok {
					flag.List = &l
				}
			}
			if f.Code == giftflag.Restored && slices.Contains(restorers, f.By) {
				if u, ok := people[f.By]; ok {
					flag.By = &u
				}
			}
			if f.Code == giftflag.RecipientsChanged && seenLists[r.ListID] {
				flag.ShowRecipients = true
				flag.OldRecipients, flag.NewRecipients = pick(people, f.OldRecipients), pick(people, f.NewRecipients)
			}
			flags = append(flags, flag)
		}
		out[r.ID] = flags
	}
	return out, nil
}

// pick returns the summaries of the users with these IDs, in that order. A user
// who is gone is left out.
func pick(people map[int64]user.Summary, ids []int64) []user.Summary {
	out := []user.Summary{}
	for _, id := range ids {
		if u, ok := people[id]; ok {
			out = append(out, u)
		}
	}
	return out
}

// summariesOf returns the summaries of the users with these IDs. A summary needs
// no check that the viewer can see the user. q is a database or a transaction.
func summariesOf(ctx context.Context, q sqlx.QueryerContext, ids []int64) (map[int64]user.Summary, error) {
	out := map[int64]user.Summary{}
	if len(ids) == 0 {
		return out, nil
	}
	query, args, err := sqlx.In(`SELECT users.id, users.name, users.avatar_image_id, images.width, images.height
		FROM users LEFT JOIN images ON images.id = users.avatar_image_id WHERE users.id IN (?)`, ids)
	if err != nil {
		return nil, fmt.Errorf("user summaries: %w", err)
	}
	var rows []struct {
		ID     int64         `db:"id"`
		Name   string        `db:"name"`
		Avatar sql.NullInt64 `db:"avatar_image_id"`
		Width  sql.NullInt64 `db:"width"`
		Height sql.NullInt64 `db:"height"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("user summaries: %w", err)
	}
	for _, r := range rows {
		u := user.Summary{ID: r.ID, Name: r.Name}
		if r.Avatar.Valid {
			u.Avatar = &image.Summary{ID: r.Avatar.Int64, Width: int(r.Width.Int64), Height: int(r.Height.Int64)}
		}
		out[r.ID] = u
	}
	return out, nil
}

// recipientZones returns the recipients of the lists with their timezones, by
// list ID. It includes recipients whom nobody sees: the reveal waits for them
// all. A list that was deleted with a gift kept has its last recipients in
// kept_list_recipients, and the gift is for them.
func recipientZones(ctx context.Context, q sqlx.QueryerContext, listIDs []int64) (map[int64][]recipientZone, error) {
	query, args, err := sqlx.In(`SELECT lr.list_id, users.id AS user_id, users.timezone FROM (
			SELECT list_id, user_id FROM list_recipients UNION ALL SELECT list_id, user_id FROM kept_list_recipients) lr
		JOIN users ON users.id = lr.user_id WHERE lr.list_id IN (?)`, listIDs)
	if err != nil {
		return nil, fmt.Errorf("recipient zones: %w", err)
	}
	var rows []struct {
		ListID   int64          `db:"list_id"`
		UserID   int64          `db:"user_id"`
		Timezone sql.NullString `db:"timezone"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("recipient zones: %w", err)
	}
	out := map[int64][]recipientZone{}
	for _, r := range rows {
		out[r.ListID] = append(out[r.ListID], recipientZone{UserID: r.UserID, Zone: nilIfInvalid(r.Timezone.String, r.Timezone.Valid)})
	}
	return out, nil
}

// givenRecipients returns the users that the given gifts were given to, by gift
// ID, in the order the list showed them at reveal.
func givenRecipients(ctx context.Context, q sqlx.QueryerContext, giftIDs []int64) (map[int64][]user.Summary, error) {
	out := map[int64][]user.Summary{}
	if len(giftIDs) == 0 {
		return out, nil
	}
	query, args, err := sqlx.In(`SELECT gift_recipients.gift_id, users.id, users.name, users.avatar_image_id,
			images.width AS avatar_width, images.height AS avatar_height
		FROM gift_recipients JOIN users ON users.id = gift_recipients.user_id
		LEFT JOIN images ON images.id = users.avatar_image_id
		WHERE gift_recipients.gift_id IN (?) ORDER BY gift_recipients.gift_id, gift_recipients.position`, giftIDs)
	if err != nil {
		return nil, fmt.Errorf("given recipients: %w", err)
	}
	var rows []struct {
		GiftID       int64         `db:"gift_id"`
		ID           int64         `db:"id"`
		Name         string        `db:"name"`
		AvatarID     sql.NullInt64 `db:"avatar_image_id"`
		AvatarWidth  sql.NullInt64 `db:"avatar_width"`
		AvatarHeight sql.NullInt64 `db:"avatar_height"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("given recipients: %w", err)
	}
	for _, r := range rows {
		u := user.Summary{ID: r.ID, Name: r.Name}
		if r.AvatarID.Valid {
			u.Avatar = &image.Summary{ID: r.AvatarID.Int64, Width: int(r.AvatarWidth.Int64), Height: int(r.AvatarHeight.Int64)}
		}
		out[r.GiftID] = append(out[r.GiftID], u)
	}
	for _, id := range giftIDs {
		if out[id] == nil {
			out[id] = []user.Summary{}
		}
	}
	return out, nil
}

// listSight returns which of the lists the viewer can see, and which of them
// the viewer is a recipient of.
func listSight(ctx context.Context, q sqlx.QueryerContext, viewer user.User, listIDs []int64) (seen, mine map[int64]bool, err error) {
	visible, args := list.VisibleTo(viewer)
	query, inArgs, err := sqlx.In(`SELECT lists.id FROM lists WHERE lists.id IN (?) AND `+visible, append([]any{listIDs}, args...)...)
	if err != nil {
		return nil, nil, fmt.Errorf("list sight: %w", err)
	}
	var ids []int64
	if err := sqlx.SelectContext(ctx, q, &ids, query, inArgs...); err != nil {
		return nil, nil, fmt.Errorf("list sight: %w", err)
	}
	seen = map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	query, inArgs, err = sqlx.In(`SELECT list_id FROM list_recipients WHERE user_id = ? AND list_id IN (?)`, viewer.ID, listIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("list sight: %w", err)
	}
	ids = nil
	if err := sqlx.SelectContext(ctx, q, &ids, query, inArgs...); err != nil {
		return nil, nil, fmt.Errorf("list sight: %w", err)
	}
	mine = map[int64]bool{}
	for _, id := range ids {
		mine[id] = true
	}
	return seen, mine, nil
}

// getIn loads a gift that the viewer can see. q is a database or a transaction.
func (s *Service) getIn(ctx context.Context, q sqlx.QueryerContext, viewer user.User, id int64) (Gift, error) {
	seen, args := VisibleTo(viewer)
	var rows []row
	if err := sqlx.SelectContext(ctx, q, &rows, selectSQL+` WHERE gifts.id = ? AND `+seen, append([]any{id}, args...)...); err != nil {
		return Gift{}, fmt.Errorf("get gift: %w", err)
	}
	if len(rows) == 0 {
		return Gift{}, ErrNotFound
	}
	gifts, err := s.build(ctx, q, viewer, rows)
	if err != nil {
		return Gift{}, err
	}
	return gifts[0], nil
}

// GetVisible loads a gift that the viewer can see. It returns [ErrNotFound] when
// there is no such gift, and when the viewer can't see it.
func (s *Service) GetVisible(ctx context.Context, viewer user.User, id int64) (Gift, error) {
	return s.getIn(ctx, s.db, viewer, id)
}

// noTime is the sort key of a gift with no `given_at`. It sorts after every time.
const noTime = "~"

func sortKey(g Gift, key string) string {
	if key == "given_at" {
		if g.GivenAt == nil {
			return noTime
		}
		return page.FormatTime(*g.GivenAt)
	}
	return page.FormatTime(g.RevealAt)
}

// compare orders two positions in the collection's sort: the key, then the ID,
// both reversed for a descending sort. A gift with no `given_at` is last in both
// directions.
func compare(req page.Request, aKey string, aID int64, bKey string, bID int64) int {
	if req.Key == "given_at" {
		aNone, bNone := aKey == noTime, bKey == noTime
		switch {
		case aNone && !bNone:
			return 1
		case bNone && !aNone:
			return -1
		}
	}
	c := strings.Compare(aKey, bKey)
	if c == 0 {
		switch {
		case aID < bID:
			c = -1
		case aID > bID:
			c = 1
		}
	}
	if req.Desc {
		return -c
	}
	return c
}

// Visible returns one page of the gifts that the viewer can see and that match
// the filter, and how many there are in all. It returns the next cursor, or nil
// on the last page. A reveal moment follows the recipients' zones, so it is not a
// column: the collection is sorted here.
func (s *Service) Visible(ctx context.Context, viewer user.User, f Filter, req page.Request) (gifts []Gift, total int, next *string, err error) {
	seen, args := VisibleTo(viewer)
	where := []string{seen}
	if f.State != nil {
		if *f.State == StateOpen {
			where = append(where, `gifts.given_at IS NULL`)
		} else {
			where = append(where, `gifts.given_at IS NOT NULL`)
		}
	}
	for _, c := range []struct {
		id  *int64
		col string
	}{{f.GiverID, "gifts.giver_id"}, {f.ItemID, "gifts.item_id"}, {f.ListID, "items.list_id"}} {
		if c.id != nil {
			where, args = append(where, c.col+` = ?`), append(args, *c.id)
		}
	}
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, selectSQL+` WHERE `+strings.Join(where, " AND "), args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list gifts: %w", err)
	}
	all, err := s.build(ctx, s.db, viewer, rows)
	if err != nil {
		return nil, 0, nil, err
	}
	if f.OccasionID != nil {
		// A filter never finds a gift by a field that is hidden from the caller.
		all = slices.DeleteFunc(all, func(g Gift) bool { return g.Occasion == nil || g.Occasion.ID != *f.OccasionID })
	}
	if f.RecipientID != nil {
		// Only a given gift has recipients, and a filter never finds a gift by a field
		// that is hidden from the caller.
		all = slices.DeleteFunc(all, func(g Gift) bool {
			return g.State != StateGiven || !g.ShowRecipients || !slices.ContainsFunc(g.Recipients, func(u user.Summary) bool { return u.ID == *f.RecipientID })
		})
	}
	if f.NeedsAttention != nil {
		all = slices.DeleteFunc(all, func(g Gift) bool { return g.NeedsAttention != *f.NeedsAttention })
	}
	total = len(all)

	slices.SortFunc(all, func(a, b Gift) int { return compare(req, sortKey(a, req.Key), a.ID, sortKey(b, req.Key), b.ID) })
	if req.After != nil {
		after := req.After
		start := slices.IndexFunc(all, func(g Gift) bool { return compare(req, sortKey(g, req.Key), g.ID, after.Value, after.ID) > 0 })
		if start < 0 {
			start = len(all)
		}
		all = all[start:]
	}
	all = all[:min(len(all), req.Limit+1)]
	gifts, next = page.Slice(req, all, func(g Gift) page.Position { return page.Position{Value: sortKey(g, req.Key), ID: g.ID} })
	return gifts, total, next, nil
}

func cleanFromLabel(label string) (*string, error) {
	label = strings.TrimSpace(label)
	switch {
	case utf8.RuneCountInString(label) > MaxFromLabelLength:
		return nil, invalid("from_label", "the from label must be at most %d characters", MaxFromLabelLength)
	case label == "":
		return nil, nil
	}
	return &label, nil
}

func cleanNote(note string) (*string, error) {
	note = strings.TrimSpace(note)
	switch {
	case utf8.RuneCountInString(note) > MaxNoteLength:
		return nil, invalid("note", "the note must be at most %d characters", MaxNoteLength)
	case note == "":
		return nil, nil
	}
	return &note, nil
}
