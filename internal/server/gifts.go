package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/user"
)

// GiftItemSummary is an item as a gift holds it (docs/routes.md, Summary
// shapes).
type GiftItemSummary struct {
	ID           string        `json:"id" example:"12" doc:"An opaque string. Don't parse it."`
	Title        string        `json:"title"`
	PrimaryImage OptionalImage `json:"primary_image" doc:"The item's primary image, or null when it has none."`
}

// OccasionSummary is an occasion as a gift holds it (docs/routes.md, Summary
// shapes).
type OccasionSummary struct {
	ID   string `json:"id" example:"7" doc:"An opaque string. Don't parse it."`
	Kind string `json:"kind" enum:"birthday,shared,custom"`
	Name string `json:"name"`
}

// OptionalOccasionSummary is an occasion summary, or null when there is none: the
// type of a gift's `occasion`. huma can't mark an object nullable on a field, so
// the type does it (see [OptionalImage]).
type OptionalOccasionSummary struct{ *OccasionSummary }

// MarshalJSON writes null for no occasion.
func (o OptionalOccasionSummary) MarshalJSON() ([]byte, error) {
	if o.OccasionSummary == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.OccasionSummary)
}

// Schema is the schema of an occasion summary that can also be null.
func (OptionalOccasionSummary) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[OccasionSummary](), false, "")
	s.Nullable = true
	return &s
}

// GiftChange is one line of the diff behind a changed flag: a field of the item
// that differs from what the giver picked (docs/routes.md, Gift fields).
type GiftChange struct {
	Field string          `json:"field" enum:"title,notes,quantity,is_unlimited,price,primary_image,store_link_price,store_link_removed" doc:"The item field that changed. price is the item price: price_minor, top_price_minor and currency together. A store link is named by url."`
	URL   *string         `json:"url,omitempty" required:"false" doc:"The store link's address, for store_link_price and store_link_removed."`
	Old   json.RawMessage `json:"old,omitempty" required:"false" doc:"The value when the giver picked the gift: a string, a number or a boolean, or for a price an object with price_minor, top_price_minor and currency. null is no value or no price. Absent for primary_image and store_link_removed."`
	New   json.RawMessage `json:"new,omitempty" required:"false" doc:"The value now, as Old is."`
}

// GiftFlag is a flag on a gift that its giver answers (docs/routes.md, Gift
// fields). Only the giver sees flags.
type GiftFlag struct {
	Code   string              `json:"code" enum:"changed,removed,moved,occasion_moved,no_longer_shared,restored,recipients_changed" doc:"What happened to the gift."`
	Fields []GiftChange        `json:"fields,omitempty" required:"false" doc:"changed only: the item fields that changed since the giver picked the gift."`
	List   OptionalListSummary `json:"list,omitzero" required:"false" doc:"moved only: the list the item moved to, or null when the giver can't see it."`

	By OptionalUserSummary `json:"by,omitzero" required:"false" doc:"restored only: the user who said they didn't get the gift, or null when the giver can't see that user."`

	// OldRecipients and NewRecipients are a recipients_changed flag's users: the
	// recipients the giver last saw, and the ones now. They are nil when the giver
	// can't see the list.
	OldRecipients *[]UserSummary `json:"old_recipients,omitempty" required:"false" nullable:"true" doc:"recipients_changed only: the users the gift was for when the giver last answered, or null when the giver can't see the list."`
	NewRecipients *[]UserSummary `json:"new_recipients,omitempty" required:"false" nullable:"true" doc:"recipients_changed only: the users the gift is for now (never the giver), or null when the giver can't see the list."`
}

// MarshalJSON writes a moved flag's list also when it is null: the giver can't
// see the new list. The other codes have no list.
func (f GiftFlag) MarshalJSON() ([]byte, error) {
	out := struct {
		Code   string               `json:"code"`
		Fields []GiftChange         `json:"fields,omitempty"`
		List   *OptionalListSummary `json:"list,omitempty"`
	}{Code: f.Code, Fields: f.Fields}
	if f.Code == giftflag.Moved {
		out.List = &f.List
	}
	if f.Code == giftflag.Restored {
		// by is null when the giver can't see the user: the flag names nobody.
		return json.Marshal(struct {
			Code string              `json:"code"`
			By   OptionalUserSummary `json:"by"`
		}{f.Code, f.By})
	}
	if f.Code == giftflag.RecipientsChanged {
		// Both are null when the giver can't see the list: the flag names nobody.
		return json.Marshal(struct {
			Code          string         `json:"code"`
			OldRecipients *[]UserSummary `json:"old_recipients"`
			NewRecipients *[]UserSummary `json:"new_recipients"`
		}{f.Code, f.OldRecipients, f.NewRecipients})
	}
	return json.Marshal(out)
}

// OptionalListSummary is a list summary, or null when there is none: the type of
// the list in a moved flag. huma can't mark an object nullable on a field, so the
// type does it (see [OptionalImage]).
type OptionalListSummary struct{ *ListSummary }

// MarshalJSON writes null for no list.
func (l OptionalListSummary) MarshalJSON() ([]byte, error) {
	if l.ListSummary == nil {
		return []byte("null"), nil
	}
	return json.Marshal(l.ListSummary)
}

// Schema is the schema of a list summary that can also be null.
func (OptionalListSummary) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[ListSummary](), false, "")
	s.Nullable = true
	return &s
}

// newGiftFlags turns the flags of a gift into the flags of its body.
func newGiftFlags(flags []gift.Flag) []GiftFlag {
	out := make([]GiftFlag, len(flags))
	for i, f := range flags {
		flag := GiftFlag{Code: f.Code}
		for _, c := range f.Changes {
			change := GiftChange{Field: c.Field, Old: c.Old, New: c.New}
			if c.Label != "" {
				label := c.Label
				change.URL = &label
			}
			if c.Field == giftflag.FieldPrimaryImage || c.Field == giftflag.FieldStoreLinkRemoved {
				change.Old, change.New = nil, nil
			}
			flag.Fields = append(flag.Fields, change)
		}
		if f.List != nil {
			flag.List = OptionalListSummary{&ListSummary{ID: formatID(f.List.ID), Name: f.List.Name}}
		}
		if f.By != nil {
			by := userSummaries([]user.Summary{*f.By})[0]
			flag.By = OptionalUserSummary{&by}
		}
		if f.ShowRecipients {
			oldRecipients, newRecipients := userSummaries(f.OldRecipients), userSummaries(f.NewRecipients)
			flag.OldRecipients, flag.NewRecipients = &oldRecipients, &newRecipients
		}
		out[i] = flag
	}
	return out
}

// GiftBody is the full gift (docs/routes.md, Gift fields), as the caller sees
// it: its giver sees every field of an open gift, and a given gift's recipients
// and their parents see the gift without the giver's own fields (see
// [seesGiverFields]).
type GiftBody struct {
	ID       string          `json:"id" example:"15" doc:"An opaque string. Don't parse it."`
	State    string          `json:"state" enum:"open,given" doc:"open until the gift is given at reveal."`
	Quantity int             `json:"quantity"`
	Item     GiftItemSummary `json:"item" doc:"The item the gift is of. A giver keeps the gift when the item is deleted, but nobody else sees the item then."`
	List     ListSummary     `json:"list" doc:"The item's list."`

	Title         string        `json:"title" doc:"The item's title now. A deleted item keeps its fields, so they show what it was when it was deleted."`
	Image         OptionalImage `json:"image" doc:"The item's primary image now, or null."`
	PriceMinor    *int64        `json:"price_minor" nullable:"true" doc:"The item's price now, in the currency's minor unit, or null."`
	TopPriceMinor *int64        `json:"top_price_minor" nullable:"true" doc:"The top of the item's price range, or null."`
	Currency      *string       `json:"currency" nullable:"true" doc:"An ISO 4217 code. It is null exactly when there is no price."`

	Recipients Private[[]UserSummary] `json:"recipients,omitzero" required:"false" doc:"The users the gift is for, never the giver: the list's recipients now for an open gift, and the recipients at reveal for a given one. Absent when the giver can't see the list (at reveal, for a given gift)."`

	Giver     OptionalUserSummary `json:"giver" doc:"The giver, or null once they are deleted."`
	GiverName string              `json:"giver_name" doc:"The giver's name as it was."`

	FromLabel    *string                 `json:"from_label" nullable:"true" doc:"A short text that says who the gift is from, when that isn't only the giver. At most 60 characters."`
	Occasion     OptionalOccasionSummary `json:"occasion" doc:"The gift's occasion. null when it isn't an occasion of a recipient the giver can see, or one the giver can't see: only the reveal date shows then."`
	OccasionName *string                 `json:"occasion_name" nullable:"true" doc:"The occasion's name, or null when occasion is."`
	OccasionDate string                  `json:"occasion_date" format:"date" doc:"YYYY-MM-DD: the date the occasion fell on when the giver picked it."`
	RevealAt     time.Time               `json:"reveal_at" doc:"When the gift is given: 00:00 on the day after the occasion, in the zone of the last recipient to reach it. Every recipient counts, also one the giver can't see."`
	GivenAt      *time.Time              `json:"given_at" nullable:"true" doc:"When the gift was given, or null while it is open."`

	IsBought       PrivateNonNull[bool]      `json:"is_bought,omitzero" required:"false" doc:"The giver's own mark. Only the giver sees it: a recipient and their parent get no key."`
	Note           Private[string]           `json:"note,omitzero" required:"false" doc:"The giver's private note, or null. Only the giver sees it. At most 2000 characters."`
	Flags          Private[[]GiftFlag]       `json:"flags,omitzero" required:"false" doc:"What happened to the gift that its giver should answer, oldest first. Only the giver of an open gift sees flags. The giver clears them with POST /gifts/{id}/acknowledge."`
	NeedsAttention PrivateNonNull[bool]      `json:"needs_attention,omitzero" required:"false" doc:"True for an open gift that isn't bought and is given within 14 days, and for an open gift flagged changed or removed. Only the giver of an open gift sees it."`
	CreatedAt      PrivateNonNull[time.Time] `json:"created_at,omitzero" required:"false" doc:"When the giver claimed the item. Only the giver sees it."`

	// The `can_*` fields are the caller's.
	CanEdit          bool `json:"can_edit" doc:"True when the caller is the giver and the gift is open."`
	CanDelete        bool `json:"can_delete" doc:"True when the caller can unclaim the gift: same as can_edit."`
	CanRaiseQuantity bool `json:"can_raise_quantity" doc:"True when the caller can edit the gift, isn't a recipient of the list, and more are still wanted or the item is unlimited."`
	CanAcknowledge   bool `json:"can_acknowledge" doc:"True when the caller can edit the gift and it has flags."`
	CanRestore       bool `json:"can_restore" doc:"True when the caller is a recipient of a given gift, or their parent: POST /gifts/{id}/restore says that it wasn't received."`
	CanAddAgain      bool `json:"can_add_again" doc:"The same as can_restore: POST /lists/{id}/items with from_gift_id adds the gift to a list again."`
}

// giftRow is a gift in the gifts collection: the full gift with its version.
type giftRow struct {
	GiftBody
	Version string `json:"version" doc:"The same value as the ETag of GET /gifts/{id} for this caller. Send it in If-Match."`
}

func newGiftBody(g gift.Gift) GiftBody {
	b := GiftBody{
		ID:       formatID(g.ID),
		State:    string(g.State),
		Quantity: g.Quantity,
		Item:     GiftItemSummary{ID: formatID(g.ItemID), Title: g.Title, PrimaryImage: optionalImage(g.Image)},
		List:     ListSummary{ID: formatID(g.List.ID), Name: g.List.Name},

		Title:         g.Title,
		Image:         optionalImage(g.Image),
		PriceMinor:    g.PriceMinor,
		TopPriceMinor: g.TopPriceMinor,
		Currency:      g.Currency,

		GiverName: g.GiverName,
		FromLabel: g.FromLabel,

		OccasionName: g.OccasionName,
		OccasionDate: g.OccasionDate.String(),
		RevealAt:     g.RevealAt.UTC(),

		CanEdit:          g.CanEdit,
		CanDelete:        g.CanEdit,
		CanRaiseQuantity: g.CanRaiseQuantity,
		CanAcknowledge:   g.CanAcknowledge,
		CanRestore:       g.CanRestore,
		CanAddAgain:      g.CanRestore,
	}
	if g.GivenAt != nil {
		t := g.GivenAt.UTC().Truncate(time.Second)
		b.GivenAt = &t
	}
	if seesGiverFields(g) {
		created := g.CreatedAt.UTC().Truncate(time.Second)
		b.IsBought, b.Note, b.CreatedAt = ShownValue(g.IsBought), Shown(g.Note), ShownValue(created)
		if g.State == gift.StateOpen {
			flags := newGiftFlags(g.Flags)
			b.Flags, b.NeedsAttention = Shown(&flags), ShownValue(g.NeedsAttention)
		}
	}
	if g.ShowRecipients {
		recipients := userSummaries(g.Recipients)
		b.Recipients = Shown(&recipients)
	}
	if g.Giver != nil {
		s := userSummaries([]user.Summary{*g.Giver})[0]
		b.Giver = OptionalUserSummary{&s}
	}
	if g.Occasion != nil {
		b.Occasion = OptionalOccasionSummary{&OccasionSummary{ID: formatID(g.Occasion.ID), Kind: string(g.Occasion.Kind), Name: g.Occasion.Name}}
	}
	return b
}

// seesGiverFields says whether the caller sees the fields that are the giver's:
// the bought mark, the private note, and when the gift was claimed. Only the
// giver sees them, and the flags and needs_attention only while the gift is open
// (docs/routes.md, Gift fields). A recipient and their parent never do.
func seesGiverFields(g gift.Gift) bool { return g.IsGiver }

func newGiftRows(gifts []gift.Gift) ([]giftRow, error) {
	rows := make([]giftRow, len(gifts))
	for i, g := range gifts {
		body := newGiftBody(g)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = giftRow{GiftBody: body, Version: v}
	}
	return rows, nil
}

// giftProblem turns an error of the gift package into a problem with a code.
func giftProblem(err error) error {
	var field *gift.FieldError
	switch {
	case errors.Is(err, gift.ErrNotFound):
		return notFound()
	case errors.Is(err, gift.ErrNotEditable):
		return forbidden("This gift is given, so nobody changes it.")
	case errors.Is(err, gift.ErrNotRestorable):
		return forbidden("Only the people a gift was given to, and their parents, can say that they didn't get it.")
	case errors.Is(err, gift.ErrGiven):
		return Error(http.StatusForbidden, "gift_given", "This gift is given, so it can't be unclaimed.")
	case errors.Is(err, gift.ErrRecipientCannotClaim):
		return Error(http.StatusForbidden, "recipient_cannot_claim", "You can't claim an item on a list that you are a recipient of.")
	case errors.Is(err, gift.ErrUserDeactivated):
		return userDeactivated()
	case errors.Is(err, gift.ErrFullyClaimed):
		return Error(http.StatusConflict, "item_fully_claimed", "Someone else just claimed some of this, so not that many are still wanted.")
	case errors.Is(err, gift.ErrAlreadyClaimed):
		return Error(http.StatusConflict, "already_claimed", "You already claimed this item for that occasion. Change that gift instead.")
	case errors.Is(err, gift.ErrOccasionNotAllowed):
		return Error(http.StatusUnprocessableEntity, "occasion_not_allowed", "That is not one of the recipients' upcoming occasions that you can pick.",
			&huma.ErrorDetail{Location: "body.occasion_id", Message: "is not an occasion you can pick for this list"})
	case errors.Is(err, gift.ErrOccasionLocked):
		return Error(http.StatusUnprocessableEntity, "occasion_locked", "You can see none of the other recipients of this list, so the occasion is picked for you.",
			&huma.ErrorDetail{Location: "body.occasion_id", Message: "can't be chosen: you can see none of the other recipients"})
	case errors.Is(err, gift.ErrQuantityRaiseNotAllowed):
		return Error(http.StatusForbidden, "quantity_raise_not_allowed", "You are a recipient of this list, so you can lower the quantity of this gift but not raise it.")
	case errors.As(err, &field):
		return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(),
			&huma.ErrorDetail{Location: "body." + field.Field, Message: err.Error()})
	}
	return err
}

var giftRules = collectionRules{Name: "gifts", Sorts: gift.SortKeys, Filters: []string{"state", "giver_id", "recipient_id", "item_id", "list_id", "occasion_id", "needs_attention"}}

type listGiftsInput struct {
	PageParams
	State       string `query:"state" enum:"open,given" doc:"Only the gifts in this state. Empty is no filter."`
	GiverID     string `query:"giver_id" doc:"Only the gifts that this user gives. me is the caller. Empty is no filter."`
	RecipientID string `query:"recipient_id" doc:"Only the given gifts that have this user among their recipients, as the caller sees them. me is the caller. Empty is no filter. Received is recipient_id=me."`
	ItemID      string `query:"item_id" doc:"Only the gifts of this item. Empty is no filter."`
	ListID      string `query:"list_id" doc:"Only the gifts of the items on this list. Empty is no filter."`
	OccasionID  string `query:"occasion_id" doc:"Only the gifts for this occasion. A gift whose occasion is hidden from the caller never matches. Empty is no filter."`

	NeedsAttention OptionalParam[bool] `query:"needs_attention" doc:"true keeps the gifts that need attention, false the ones that don't."`
}

type createGiftInput struct {
	ID   string `path:"id" example:"12" doc:"An item's ID."`
	Body struct {
		Quantity   int     `json:"quantity,omitempty" required:"false" default:"1" minimum:"1" doc:"How many the giver will give. At most as many as are still wanted, else 409 item_fully_claimed."`
		OccasionID *string `json:"occasion_id,omitempty" required:"false" nullable:"true" example:"7" doc:"One of GET /occasions?list_id=. Left out, the server picks the first of them: the soonest occasion that the giver may pick. Any other occasion is a 422 occasion_not_allowed."`
		FromLabel  *string `json:"from_label,omitempty" required:"false" nullable:"true" maxLength:"60" doc:"Who the gift is from, when that isn't only the giver."`
		Note       *string `json:"note,omitempty" required:"false" nullable:"true" maxLength:"2000" doc:"The giver's private note."`
		IsBought   bool    `json:"is_bought,omitempty" required:"false" doc:"The giver's own mark that they bought it."`
	}
}

type giftInput struct {
	ID string `path:"id" example:"15" doc:"A gift's ID."`
}

// giftChanges are the fields of a gift that its giver may change with PATCH. The
// keys of the other fields in a gift's body are refused with 403
// field_not_writable.
type giftChanges struct {
	Quantity   Field[int]    `json:"quantity,omitempty" minimum:"1" doc:"It can't be null. A raise needs that more are still wanted, else 409 item_fully_claimed, and a recipient of the list never raises (403 quantity_raise_not_allowed)."`
	OccasionID Field[string] `json:"occasion_id,omitempty" doc:"One of GET /occasions?list_id=, else 422 occasion_not_allowed. A giver who can see none of the other recipients can't pick, whichever occasion they name: 422 occasion_locked. It can't be null. When the giver already has an open gift on the item for that occasion, the two merge: that gift takes this one in, the answer is the gift that stays, and this gift's address is a 404 from then on."`
	FromLabel  Field[string] `json:"from_label,omitempty" nullable:"true" maxLength:"60" doc:"null or an empty string clears it."`
	Note       Field[string] `json:"note,omitempty" nullable:"true" maxLength:"2000" doc:"null or an empty string clears it."`
	IsBought   Field[bool]   `json:"is_bought,omitempty" doc:"It can't be null."`
}

var giftChangeKeys = []string{"quantity", "occasion_id", "from_label", "note", "is_bought"}

func (c giftChanges) changes() (gift.Changes, error) {
	out := gift.Changes{Quantity: c.Quantity.Optional, FromLabel: c.FromLabel.Optional, Note: c.Note.Optional, IsBought: c.IsBought.Optional}
	out.OccasionID.Set = c.OccasionID.Set
	if raw := c.OccasionID.Value; raw != nil {
		id, ok := parseID(*raw)
		if !ok {
			// A string that is no ID names no occasion the giver can pick.
			return out, giftProblem(gift.ErrOccasionNotAllowed)
		}
		out.OccasionID.Value = &id
	}
	return out, nil
}

type patchGiftInput struct {
	ID      string `path:"id" example:"15" doc:"A gift's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the gift as you loaded it. If the gift changed since, the answer is 412 resource_changed and nothing changes."`
	Body    MergePatch[giftChanges]
}

type acknowledgeGiftInput struct {
	ID   string `path:"id" example:"15" doc:"A gift's ID."`
	Body *struct {
		Codes []string `json:"codes,omitempty" required:"false" doc:"The flags to clear: changed, removed, moved, occasion_moved, no_longer_shared, restored or recipients_changed. Left out, every flag is cleared. An empty array clears none. A code the gift doesn't have is skipped, and a code that is no flag's is a 422 invalid_input."`
	}
}

type deleteGiftInput struct {
	ID      string `path:"id" example:"15" doc:"A gift's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the gift as you loaded it. If the gift changed since, the answer is 412 resource_changed and nothing is deleted."`
}

// giftVersionGuard compares the If-Match header with the version of the gift as
// the caller sees it, inside the write's transaction.
func giftVersionGuard(ifMatch string) gift.Guard {
	return func(_ context.Context, _ sqlx.ExtContext, current gift.Gift) error {
		v, err := versionOf(newGiftBody(current))
		if err != nil {
			return err
		}
		return checkIfMatch(ifMatch, v)
	}
}

// visibleGift loads the gift at a path, for a caller who must be able to see it.
// A gift the caller can't see looks the same as one that doesn't exist.
func (s *Server) visibleGift(ctx context.Context, rawID string) (gift.Gift, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return gift.Gift{}, notFound()
	}
	g, err := s.opts.Gifts.GetVisible(ctx, caller.User, id)
	if err != nil {
		return gift.Gift{}, giftProblem(err)
	}
	return g, nil
}

func (s *Server) registerGifts(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-gifts",
		Method:      http.MethodGet,
		Path:        "/gifts",
		Summary:     "The gifts the caller can see",
		Description: "The gifts the caller gives, and the given gifts for the caller and the caller's children. Until a gift is given, only its giver sees it. Giving is `?giver_id=me&state=open`. Given is `?giver_id=me&state=given&sort=-given_at`. Received is `?recipient_id=me&sort=-given_at`. Sorted by `reveal_at` (soonest first). A gift with no `given_at` is last in both directions of `given_at`.",
		Tags:        []string{"Gifts"},
		Errors:      []int{http.StatusUnauthorized},
	}
	giftRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listGiftsInput) (*collectionOutput[giftRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(giftRules)
		if err != nil {
			return nil, err
		}
		var f gift.Filter
		if in.State != "" {
			state := gift.State(in.State)
			f.State = &state
		}
		// An ID that can't be a resource's names nothing, whose gifts are none.
		if in.GiverID == "me" {
			f.GiverID = &caller.User.ID
		} else if in.GiverID != "" {
			id, _ := parseID(in.GiverID)
			f.GiverID = &id
		}
		if in.RecipientID == "me" {
			f.RecipientID = &caller.User.ID
		} else if in.RecipientID != "" {
			id, _ := parseID(in.RecipientID)
			f.RecipientID = &id
		}
		for _, c := range []struct {
			raw string
			dst **int64
		}{{in.ItemID, &f.ItemID}, {in.ListID, &f.ListID}, {in.OccasionID, &f.OccasionID}} {
			if c.raw != "" {
				id, _ := parseID(c.raw)
				*c.dst = &id
			}
		}
		f.NeedsAttention = in.NeedsAttention.Pointer()
		gifts, total, next, err := s.opts.Gifts.Visible(ctx, caller.User, f, req)
		if err != nil {
			return nil, err
		}
		rows, err := newGiftRows(gifts)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[giftRow]{}
		out.Body.Data = rows
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "create-gift",
		Method:        http.MethodPost,
		Path:          "/items/{id}/gifts",
		Summary:       "Claim an item",
		Description:   "A giver claims some of an item for an occasion, which starts a gift. A giver is a user who can see the item's list and isn't one of its recipients: a recipient is a 403 `recipient_cannot_claim`. When `occasion_id` is left out, the server picks the first occasion of `GET /occasions?list_id=`. Two givers who claim the last one at once are serialized: one wins, and the other gets 409 `item_fully_claimed`. A giver can claim an item for each occasion once (409 `already_claimed`).",
		Tags:          []string{"Gifts"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createGiftInput) (*createdOutput[GiftBody], error) {
		caller, _ := CallerFrom(ctx)
		itemID, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		n := gift.New{Quantity: in.Body.Quantity, IsBought: in.Body.IsBought}
		if raw := in.Body.OccasionID; raw != nil {
			id, ok := parseID(*raw)
			if !ok {
				return nil, giftProblem(gift.ErrOccasionNotAllowed)
			}
			n.OccasionID = &id
		}
		if in.Body.FromLabel != nil {
			n.FromLabel = *in.Body.FromLabel
		}
		if in.Body.Note != nil {
			n.Note = *in.Body.Note
		}
		g, err := s.opts.Gifts.Claim(ctx, caller.User, itemID, n)
		if err != nil {
			return nil, giftProblem(err)
		}
		return createdOf("/gifts/"+formatID(g.ID), newGiftBody(g))
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-gift",
		Method:      http.MethodGet,
		Path:        "/gifts/{id}",
		Summary:     "A gift",
		Description: "Until a gift is given, only its giver sees it. For everyone else it is a 404. Once it is given, its recipients and their parents see it too, without the giver's private note, bought mark, flags, and creation time.",
		Tags:        []string{"Gifts"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *giftInput) (*resourceOutput[GiftBody], error) {
		g, err := s.visibleGift(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newGiftBody(g))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-gift",
		Method:      http.MethodPatch,
		Path:        "/gifts/{id}",
		Summary:     "Change a gift",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. The giver changes an open gift. A field the caller sees but can't change is a 403 `field_not_writable`. When the new occasion is one that the giver already has an open gift on the same item for, the two merge: the answer is 200 with the gift that stays, and this gift's address is a 404 from then on. `If-Match` is optional.",
		Tags:        []string{"Gifts"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchGiftInput) (*resourceOutput[GiftBody], error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleGift(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !current.CanEdit {
			return nil, giftProblem(gift.ErrNotEditable)
		}
		visible, err := keysOf(newGiftBody(current))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(giftChangeKeys, visible); err != nil {
			return nil, err
		}
		changes, err := in.Body.Changes.changes()
		if err != nil {
			return nil, err
		}
		updated, err := s.opts.Gifts.Update(ctx, caller.User, current.ID, changes, giftVersionGuard(in.IfMatch))
		if err != nil {
			return nil, giftProblem(err)
		}
		return resourceOf(newGiftBody(updated))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-gift",
		Method:        http.MethodDelete,
		Path:          "/gifts/{id}",
		Summary:       "Unclaim a gift",
		Description:   "The giver unclaims an open gift, and the quantity goes back to all givers. A given gift is a 403 `gift_given`. `If-Match` is optional.",
		Tags:          []string{"Gifts"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteGiftInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleGift(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if err := s.opts.Gifts.Delete(ctx, caller.User, current.ID, giftVersionGuard(in.IfMatch)); err != nil {
			return nil, giftProblem(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "acknowledge-gift",
		Method:      http.MethodPost,
		Path:        "/gifts/{id}/acknowledge",
		Summary:     "Clear the flags on a gift",
		Description: "The giver answers the flags on an open gift. `codes` names the flags to clear, and every flag is cleared by default. Keeping a gift whose item was deleted is acknowledging `removed`. A code the gift doesn't have is skipped, and a code that is no flag's is a 422. A given gift is a 403.",
		Tags:        []string{"Gifts"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *acknowledgeGiftInput) (*resourceOutput[GiftBody], error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleGift(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var codes []string
		if in.Body != nil {
			codes = in.Body.Codes
		}
		g, err := s.opts.Gifts.Acknowledge(ctx, caller.User, current.ID, codes)
		if err != nil {
			return nil, giftProblem(err)
		}
		return resourceOf(newGiftBody(g))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "restore-gift",
		Method:        http.MethodPost,
		Path:          "/gifts/{id}/restore",
		Summary:       "I didn't get this",
		Description:   "A recipient of a given gift, or their parent, says that it wasn't received. The gift opens again for its giver, who keeps it or unclaims it, and it is hidden again from everyone else: the answer is 204 with no body, and the gift's address is a 404 for the caller from then on. The quantity goes back on the item. When the giver was deleted or is deactivated, the gift is deleted instead, and the quantity goes back unclaimed. A gift that is open, or one the caller gave and doesn't receive, is a 403. A parent whose child receives the gift while deactivated sees it and gets a 403 `user_deactivated`.",
		Tags:          []string{"Gifts"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, func(ctx context.Context, in *giftInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		id, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		if err := s.opts.Gifts.Restore(ctx, caller.User, id); err != nil {
			return nil, giftProblem(err)
		}
		return nil, nil
	})

	acceptMergePatch(api, "/gifts/{id}")
}
