package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

// OptionalUserSummary is a user summary, or null when there is none: the type of
// a field such as an item's `added_by`. huma can't mark an object nullable on a
// field, so the type does it.
type OptionalUserSummary struct{ *UserSummary }

// MarshalJSON writes null for no user.
func (o OptionalUserSummary) MarshalJSON() ([]byte, error) {
	if o.UserSummary == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.UserSummary)
}

// Schema is the schema of a user summary that can also be null.
func (OptionalUserSummary) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[UserSummary](), false, "")
	s.Nullable = true
	return &s
}

// StoreLinkBody is a store link as an item contains it (docs/routes.md, Store
// links): one place where the item can be bought.
type StoreLinkBody struct {
	ID         string     `json:"id" example:"4" doc:"An opaque string. Don't parse it."`
	URL        string     `json:"url" doc:"The normalized address."`
	Store      string     `json:"store" example:"shop.example" doc:"The host of the address, with no www. and no trailing dot."`
	PriceMinor *int64     `json:"price_minor" nullable:"true" doc:"The store's price in the currency's minor unit, or null."`
	Currency   *string    `json:"currency" nullable:"true" doc:"An ISO 4217 code. It is null exactly when there is no price."`
	CapturedAt *time.Time `json:"captured_at" nullable:"true" doc:"When the price was captured, or null when there is no price."`
	ImageURL   *string    `json:"image_url" nullable:"true" doc:"The store's own address of its picture, or null. It is outside Wishbone: a client loads it directly."`
	CreatedAt  time.Time  `json:"created_at"`

	CanEdit   bool `json:"can_edit" doc:"True when the caller can edit the store link: they can edit the item's list."`
	CanDelete bool `json:"can_delete" doc:"True when the caller can delete the store link: same as can_edit."`
}

func newStoreLinkBody(l item.StoreLink, canEdit bool) StoreLinkBody {
	b := StoreLinkBody{ID: formatID(l.ID), URL: l.URL, Store: l.Store, PriceMinor: l.PriceMinor, Currency: l.Currency,
		ImageURL: l.ImageURL, CreatedAt: l.CreatedAt.UTC().Truncate(time.Second), CanEdit: canEdit, CanDelete: canEdit}
	if l.CapturedAt != nil {
		t := l.CapturedAt.UTC().Truncate(time.Second)
		b.CapturedAt = &t
	}
	return b
}

// ItemBody is the full item (docs/routes.md, Item fields).
type ItemBody struct {
	ID            string              `json:"id" example:"12" doc:"An opaque string. Don't parse it."`
	ListID        string              `json:"list_id" example:"7" doc:"The list the item is on. A user who can edit both lists moves the item by changing it."`
	Title         string              `json:"title"`
	Notes         *string             `json:"notes" nullable:"true"`
	PriceMinor    *int64              `json:"price_minor" nullable:"true" doc:"The price in the currency's minor unit, or null. A range starts here."`
	TopPriceMinor *int64              `json:"top_price_minor" nullable:"true" doc:"The top of a price range, or null."`
	Currency      *string             `json:"currency" nullable:"true" doc:"An ISO 4217 code. It is null exactly when there is no price."`
	Quantity      int                 `json:"quantity"`
	IsUnlimited   bool                `json:"is_unlimited"`
	Position      int                 `json:"position" doc:"A 0-based index on the list. Changing it moves the item, and the other items shift."`
	AddedBy       OptionalUserSummary `json:"added_by" doc:"The user who added the item, or null once that user is deleted. It never changes."`
	CreatedAt     time.Time           `json:"created_at"`

	StoreLinks   []StoreLinkBody `json:"store_links" nullable:"false" doc:"At most 10. A caller who can edit the list gets them in the order added. Every other caller gets them by price: within one currency the lowest first and the unpriced last, and with prices in several currencies the order added."`
	Images       []ImageSummary  `json:"images" nullable:"false" doc:"At most 5, in the order they were added."`
	PrimaryImage OptionalImage   `json:"primary_image" doc:"The image shown first. It is one of images, or null when there are none."`

	// StillWanted and MyClaimedQuantity are for a giver. A recipient never gets
	// them.
	StillWanted       Private[int] `json:"still_wanted,omitzero" required:"false" doc:"How many other givers can still claim, or null for an unlimited item. Only a giver sees it: for a recipient the key is absent."`
	MyClaimedQuantity Private[int] `json:"my_claimed_quantity,omitzero" required:"false" doc:"The sum of the caller's own open gifts on the item. Only a giver sees it: for a recipient the key is absent."`

	// The `can_*` fields are the caller's.
	CanEdit   bool `json:"can_edit" doc:"True when the caller can edit the item: they can edit its list."`
	CanDelete bool `json:"can_delete" doc:"True when the caller can delete the item: same as can_edit."`
	CanClaim  bool `json:"can_claim" doc:"True when the caller can claim the item. Always false for a recipient."`

	CanRefreshPrices bool `json:"can_refresh_prices" doc:"True when the caller can refresh the prices of the store links: same as can_edit, and the item has a store link."`
}

// itemRow is an item in a collection: the full item with its version.
type itemRow struct {
	ItemBody
	Version string `json:"version" doc:"The same value as the ETag of GET /items/{id} for this caller. Send it in If-Match."`
}

func newItemBody(it item.Item) ItemBody {
	b := ItemBody{
		ID:            formatID(it.ID),
		ListID:        formatID(it.ListID),
		Title:         it.Title,
		Notes:         it.Notes,
		PriceMinor:    it.PriceMinor,
		TopPriceMinor: it.TopPriceMinor,
		Currency:      it.Currency,
		Quantity:      it.Quantity,
		IsUnlimited:   it.IsUnlimited,
		Position:      it.Position,
		CreatedAt:     it.CreatedAt.UTC().Truncate(time.Second),

		CanEdit:          it.CanEdit,
		CanDelete:        it.CanEdit,
		CanRefreshPrices: it.CanEdit && len(it.StoreLinks) > 0,
	}
	if it.AddedBy != nil {
		s := userSummaries([]user.Summary{*it.AddedBy})[0]
		b.AddedBy = OptionalUserSummary{&s}
	}
	b.StoreLinks = make([]StoreLinkBody, len(it.StoreLinks))
	for i, l := range it.StoreLinks {
		b.StoreLinks[i] = newStoreLinkBody(l, it.CanEdit)
	}
	b.Images = make([]ImageSummary, len(it.Images))
	for i, img := range it.Images {
		b.Images[i] = newImageSummary(img)
	}
	b.PrimaryImage = optionalImage(it.PrimaryImage)
	// The claim state is a giver's: a recipient gets no key, and can_claim is false.
	if c := it.Claim; c != nil {
		b.StillWanted = Shown(c.StillWanted)
		mine := c.MyClaimedQuantity
		b.MyClaimedQuantity = Shown(&mine)
		b.CanClaim = c.CanClaim
	}
	return b
}

func newItemRows(items []item.Item) ([]itemRow, error) {
	rows := make([]itemRow, len(items))
	for i, it := range items {
		body := newItemBody(it)
		v, err := versionOf(body)
		if err != nil {
			return nil, err
		}
		rows[i] = itemRow{ItemBody: body, Version: v}
	}
	return rows, nil
}

// itemProblem turns an error of the item or list package into a problem with a
// code.
func itemProblem(err error) error {
	var field *item.FieldError
	switch {
	case errors.Is(err, item.ErrNotFound), errors.Is(err, list.ErrNotFound), errors.Is(err, item.ErrStoreLinkNotFound):
		return notFound()
	case errors.Is(err, list.ErrNotEditable):
		return forbidden("You can't change the items on this list.")
	case errors.Is(err, list.ErrRecipientDeactivated):
		return userDeactivated()
	case errors.Is(err, item.ErrTargetNotFound):
		return referenceNotFound("body.list_id", "That list doesn't exist, or you can't see it.")
	case errors.Is(err, item.ErrTooManyStoreLinks):
		return Error(http.StatusUnprocessableEntity, "too_many_store_links", "An item can have at most 10 store links.",
			&huma.ErrorDetail{Location: "body.store_links", Message: "has more than 10 store links"})
	case errors.Is(err, item.ErrImageAttached):
		return Error(http.StatusConflict, "image_attached", "That image is already attached to something else. Upload it again to use it here.")
	case errors.Is(err, item.ErrTooManyImages):
		return Error(http.StatusUnprocessableEntity, "too_many_images", "An item can have at most 5 images.",
			&huma.ErrorDetail{Location: "body.image_ids", Message: "has more than 5 images"})
	case errors.Is(err, item.ErrCannotEditTarget):
		return Error(http.StatusForbidden, "cannot_edit_target_list", "You can't edit the list to move the item to.")
	case errors.As(err, &field):
		return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(),
			&huma.ErrorDetail{Location: "body." + field.Field, Message: err.Error()})
	}
	return err
}

var itemRules = collectionRules{Name: "items", Sorts: item.SortKeys}

type listItemsInput struct {
	ID string `path:"id" example:"7" doc:"A list's ID."`
	PageParams
}

type createItemInput struct {
	ID   string `path:"id" example:"7" doc:"A list's ID."`
	Body struct {
		Title         string        `json:"title" required:"false" minLength:"1" maxLength:"300" doc:"Required, unless from_gift_id gives it."`
		Notes         *string       `json:"notes,omitempty" required:"false" nullable:"true" maxLength:"2000"`
		PriceMinor    Field[int64]  `json:"price_minor,omitempty" required:"false" nullable:"true" minimum:"0" doc:"The price in the currency's minor unit. Left out or null, an item with no price takes the price of its first priced store link, so a client can send a search result on unchanged. To leave an item without a price, clear it after the add: a price the user clears stays cleared."`
		TopPriceMinor Field[int64]  `json:"top_price_minor,omitempty" required:"false" nullable:"true" minimum:"0" doc:"The top of a price range. It needs a price, and can't be below it."`
		Currency      Field[string] `json:"currency,omitempty" required:"false" nullable:"true" pattern:"^[A-Z]{3}$" doc:"An ISO 4217 code. A price with no currency is in USD. A currency needs a price."`
		Quantity      int           `json:"quantity,omitempty" required:"false" default:"1" minimum:"1"`
		IsUnlimited   bool          `json:"is_unlimited,omitempty" required:"false"`

		StoreLinks     []newStoreLink `json:"store_links,omitempty" required:"false" doc:"At most 10, else 422 too_many_store_links. A link from a blocked store is a 422 store_blocked. An item with no price takes the price of its first priced store link."`
		ImageIDs       []string       `json:"image_ids,omitempty" required:"false" doc:"At most 5 uploaded images (POST /images), in order, else 422 too_many_images. An image that another resource holds is a 409 image_attached."`
		PrimaryImageID *string        `json:"primary_image_id,omitempty" required:"false" nullable:"true" doc:"One of image_ids. The first image by default."`

		FromGiftID *string `json:"from_gift_id,omitempty" required:"false" nullable:"true" example:"15" doc:"Add again: a given gift whose title, image (as a new image) and price the new item copies. Fields of the body win over the copy: a title, any of the price fields (price_minor, top_price_minor and currency come from the body together or from the gift together), and image_ids (an empty array means no image). The caller must be one of the gift's recipients or a parent of one. The gift's giver is a 403 reference_not_allowed, a parent whose child receives the gift while deactivated is a 403 user_deactivated, and anyone else a 422 reference_not_found."`
	}
}

// newStoreLink is a store link to add with a new item.
type newStoreLink struct {
	URL        string  `json:"url" minLength:"1" maxLength:"2048" doc:"An http or https address. It is normalized, and checked against the blocked stores."`
	PriceMinor *int64  `json:"price_minor,omitempty" required:"false" nullable:"true" minimum:"0" doc:"The store's price in the currency's minor unit."`
	Currency   *string `json:"currency,omitempty" required:"false" nullable:"true" pattern:"^[A-Z]{3}$" doc:"An ISO 4217 code. A price with no currency is in USD. A currency needs a price."`
	ImageURL   *string `json:"image_url,omitempty" required:"false" nullable:"true" maxLength:"2048" doc:"The store's own address of its picture. One that is not a web address is dropped."`
}

type itemInput struct {
	ID string `path:"id" example:"12" doc:"An item's ID."`
}

// itemChanges are the fields of an item that a caller who can edit its list may
// change with PATCH. The keys of the other fields in an item's body are refused
// with 403 field_not_writable.
type itemChanges struct {
	Title         Field[string] `json:"title,omitempty" minLength:"1" maxLength:"300" doc:"It can't be null."`
	Notes         Field[string] `json:"notes,omitempty" nullable:"true" maxLength:"2000" doc:"null or an empty string clears it."`
	PriceMinor    Field[int64]  `json:"price_minor,omitempty" nullable:"true" minimum:"0" doc:"null clears the price. Clear the top price with it."`
	TopPriceMinor Field[int64]  `json:"top_price_minor,omitempty" nullable:"true" minimum:"0" doc:"null makes the price a single amount again."`
	Currency      Field[string] `json:"currency,omitempty" nullable:"true" pattern:"^[A-Z]{3}$" doc:"An ISO 4217 code. Clearing the price clears the currency, and a price with no currency is in USD."`
	Quantity      Field[int]    `json:"quantity,omitempty" minimum:"1" doc:"It can't be null."`
	IsUnlimited   Field[bool]   `json:"is_unlimited,omitempty" doc:"It can't be null."`
	Position      Field[int]    `json:"position,omitempty" minimum:"0" doc:"Moves the item to this index on its list, and the other items shift. An index past the end is the end. It can't be null."`
	ListID        Field[string] `json:"list_id,omitempty" doc:"Moves the item to the end of another list. The caller must be able to edit both lists. A list the caller can't see is a 422 reference_not_found, and one they see and can't edit is a 403 cannot_edit_target_list."`

	ImageIDs       Field[[]string] `json:"image_ids,omitempty" doc:"Replaces the whole array of the item's images, in the order given (images, in the order they were added). At most 5, else 422 too_many_images. An image left out is removed from the item, and the server deletes it. An image that another resource holds is a 409 image_attached, and one the caller can't see is a 422 reference_not_found. It can't be null: send an empty array to remove every image."`
	PrimaryImageID Field[string]   `json:"primary_image_id,omitempty" nullable:"true" doc:"The ID of the primary image. It must be one of the item's images after the change, else 422 invalid_input. null makes the first image the primary image. Changing it doesn't change the order of images. Without it, the primary image stays, and when it is removed the first remaining image takes its place."`
}

var itemChangeKeys = []string{"title", "notes", "price_minor", "top_price_minor", "currency", "quantity", "is_unlimited", "position", "list_id", "image_ids", "primary_image_id"}

func (c itemChanges) changes() (item.Changes, error) {
	out := item.Changes{
		Title: c.Title.Optional, Notes: c.Notes.Optional, PriceMinor: c.PriceMinor.Optional, TopPriceMinor: c.TopPriceMinor.Optional,
		Currency: c.Currency.Optional, Quantity: c.Quantity.Optional, IsUnlimited: c.IsUnlimited.Optional, Position: c.Position.Optional,
	}
	if c.ImageIDs.Set && c.ImageIDs.Value != nil {
		ids := make([]int64, len(*c.ImageIDs.Value))
		for i, raw := range *c.ImageIDs.Value {
			id, ok := parseID(raw)
			if !ok {
				// A string that is no ID names no image the caller can see.
				return out, referenceNotFound(fmt.Sprintf("body.image_ids.%d", i), "That image doesn't exist, or you can't see it.")
			}
			ids[i] = id
		}
		out.ImageIDs = patch.Optional[[]int64]{Set: true, Value: &ids}
	} else {
		out.ImageIDs = patch.Optional[[]int64]{Set: c.ImageIDs.Set}
	}
	out.PrimaryImageID.Set = c.PrimaryImageID.Set
	if raw := c.PrimaryImageID.Value; raw != nil {
		id, ok := parseID(*raw)
		if !ok {
			return out, Error(http.StatusUnprocessableEntity, "invalid_input", "The primary image must be one of the item's images.",
				&huma.ErrorDetail{Location: "body.primary_image_id", Message: "is not one of the item's images"})
		}
		out.PrimaryImageID.Value = &id
	}
	out.ListID.Set = c.ListID.Set
	if raw := c.ListID.Value; raw != nil {
		id, ok := parseID(*raw)
		if !ok {
			return out, referenceNotFound("body.list_id", "That list doesn't exist, or you can't see it.")
		}
		out.ListID.Value = &id
	}
	return out, nil
}

type patchItemInput struct {
	ID      string `path:"id" example:"12" doc:"An item's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the item as you loaded it. If the item changed since, the answer is 412 resource_changed and nothing changes."`
	Body    MergePatch[itemChanges]
}

type deleteItemInput struct {
	ID      string `path:"id" example:"12" doc:"An item's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the item as you loaded it. If the item changed since, the answer is 412 resource_changed and nothing is deleted."`
}

// itemVersionGuard compares the If-Match header with the version of the item as
// the caller sees it, inside the write's transaction.
func itemVersionGuard(ifMatch string) item.Guard {
	return func(_ context.Context, _ sqlx.ExtContext, current item.Item) error {
		v, err := versionOf(newItemBody(current))
		if err != nil {
			return err
		}
		return checkIfMatch(ifMatch, v)
	}
}

// itemChangeGuard is the guard of a change: the If-Match check, and then, inside
// the same transaction, the check that the caller may attach each image the
// change names. An image the item holds already passes.
func itemChangeGuard(caller user.User, ifMatch string, c item.Changes) item.Guard {
	versionGuard := itemVersionGuard(ifMatch)
	return func(ctx context.Context, q sqlx.ExtContext, current item.Item) error {
		if err := versionGuard(ctx, q, current); err != nil {
			return err
		}
		if c.ImageIDs.Value == nil {
			return nil
		}
		for i, id := range *c.ImageIDs.Value {
			err := attachableImage(ctx, q, caller, id, fmt.Sprintf("body.image_ids.%d", i), func(held image.Attachment) bool {
				return held.Kind == image.KindItem && held.ID == current.ID
			})
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// visibleItem loads the item at a path, for a caller who must be able to see
// it. An item the caller can't see looks the same as one that doesn't exist.
func (s *Server) visibleItem(ctx context.Context, rawID string) (item.Item, error) {
	caller, _ := CallerFrom(ctx)
	id, ok := parseID(rawID)
	if !ok {
		return item.Item{}, notFound()
	}
	it, err := s.opts.Items.GetVisible(ctx, caller.User, id)
	if err != nil {
		return item.Item{}, itemProblem(err)
	}
	return it, nil
}

// newStoreLinks cleans the store links of a new item. Each address is
// normalized and parsed before the blocked stores are asked, and an address that
// can't be parsed is a 422, never a pass (see [Server.unblockedAddress]).
func (s *Server) newStoreLinks(ctx context.Context, in []newStoreLink) ([]item.NewStoreLink, error) {
	out := make([]item.NewStoreLink, len(in))
	for i, l := range in {
		norm, err := s.unblockedAddress(ctx, l.URL, fmt.Sprintf("body.store_links.%d.url", i))
		if err != nil {
			return nil, err
		}
		n := item.NewStoreLink{URL: norm, Store: extract.StoreFromURL(norm), PriceMinor: l.PriceMinor}
		if l.Currency != nil {
			n.Currency = *l.Currency
		}
		// The picture's address comes from the reader, not the user, and a
		// picture is best effort: one that can't be used is dropped.
		if l.ImageURL != nil && strings.Contains(*l.ImageURL, "://") {
			if img, err := extract.NormalizeURL(*l.ImageURL); err == nil {
				n.ImageURL = img
			}
		}
		out[i] = n
	}
	return out, nil
}

// addAgain fills a new item with what it copies from the given gift that
// from_gift_id names (docs/routes.md, Item routes): the title, the price, and
// the image, which is copied as a new image inside the add's transaction. A field
// that the body gave wins: bodyPrice says the body gave a price field, and
// bodyImages says it gave image_ids. The images the body names are checked when
// the item is written and stop the copy, and an empty array stops it too: it
// means no image. A gift the caller can't see is a 422 reference_not_found,
// whether it doesn't exist or is not given, and the answer says nothing of its
// item. The copy the add makes is left in *copied, so the route can delete its
// file when the add fails.
func (s *Server) addAgain(ctx context.Context, caller user.User, raw string, bodyPrice, bodyImages bool, n *item.New, copied **image.Image) error {
	notFound := referenceNotFound("body.from_gift_id", "That gift doesn't exist, or you can't see it.")
	id, ok := parseID(raw)
	if !ok {
		return notFound
	}
	src, err := s.opts.Gifts.AddAgainSource(ctx, caller, id)
	switch {
	case errors.Is(err, gift.ErrNotFound):
		return notFound
	case errors.Is(err, gift.ErrAddAgainNotAllowed):
		return Error(http.StatusForbidden, "reference_not_allowed", "You gave this gift, so you can't add it again. Only the people it was given to, and their parents, can.")
	case errors.Is(err, gift.ErrUserDeactivated):
		return userDeactivated()
	case err != nil:
		return err
	}
	if n.Title == "" {
		n.Title = src.Title
	}
	if !bodyPrice {
		n.PriceMinor, n.TopPriceMinor = src.PriceMinor, src.TopPriceMinor
		if src.Currency != nil {
			n.Currency = *src.Currency
		}
	}
	if src.ImageID != nil && !bodyImages {
		held := *src.ImageID
		n.ImageFrom = func(ctx context.Context, q sqlx.ExtContext) (int64, bool, error) {
			img, err := s.opts.Images.Copy(ctx, q, caller.ID, held)
			if errors.Is(err, image.ErrNotFound) {
				// The file is gone: the item has no image, as a reveal leaves a gift with none.
				return 0, false, nil
			}
			if err != nil {
				return 0, false, err
			}
			*copied = &img
			return img.ID, true, nil
		}
	}
	return nil
}

// newImages reads the image IDs of a new item, and returns the guard that checks,
// inside the write's transaction, that the caller may attach each of them.
func (s *Server) newImages(caller user.User, rawIDs []string, rawPrimary *string) (ids []int64, primary *int64, guard item.AddGuard, err error) {
	for _, raw := range rawIDs {
		id, ok := parseID(raw)
		if !ok {
			// A string that is no ID names no image the caller can see.
			id = 0
		}
		ids = append(ids, id)
	}
	if rawPrimary != nil {
		id, ok := parseID(*rawPrimary)
		if !ok {
			return nil, nil, nil, Error(http.StatusUnprocessableEntity, "invalid_input", "The primary image must be one of the item's images.",
				&huma.ErrorDetail{Location: "body.primary_image_id", Message: "is not one of image_ids"})
		}
		primary = &id
	}
	guard = func(ctx context.Context, q sqlx.ExtContext) error {
		for i, id := range ids {
			// A new item holds nothing yet, so any holder is another resource.
			err := attachableImage(ctx, q, caller, id, fmt.Sprintf("body.image_ids.%d", i), func(image.Attachment) bool { return false })
			if err != nil {
				return err
			}
		}
		return nil
	}
	return ids, primary, guard, nil
}

func (s *Server) registerItems(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-items",
		Method:      http.MethodGet,
		Path:        "/lists/{id}/items",
		Summary:     "The items on a list",
		Description: "The items of a list the caller can see, in the order the users who can edit the list set. `price` sorts by the low end of a range, by currency code and then by amount, and an item with no price is last in both directions.",
		Tags:        []string{"Items"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}
	itemRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *listItemsInput) (*collectionOutput[itemRow], error) {
		caller, _ := CallerFrom(ctx)
		req, err := in.parse(itemRules)
		if err != nil {
			return nil, err
		}
		listID, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		// Items.Visible loads the list once, and answers list.ErrNotFound for one
		// the caller can't see.
		items, total, next, err := s.opts.Items.Visible(ctx, caller.User, listID, req)
		if err != nil {
			return nil, itemProblem(err)
		}
		rows, err := newItemRows(items)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[itemRow]{}
		out.Body.Data = rows
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID:   "create-item",
		Method:        http.MethodPost,
		Path:          "/lists/{id}/items",
		Summary:       "Add an item to a list",
		Description:   "A user who can edit the list adds an item. It goes at the end of the list, and `added_by` is the caller. There is no duplicate check. A caller who can edit the list only as the parent of a deactivated recipient gets a 403 `user_deactivated`.",
		Tags:          []string{"Items"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *createItemInput) (*createdOutput[ItemBody], error) {
		caller, _ := CallerFrom(ctx)
		l, err := s.visibleList(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !l.CanEdit {
			return nil, s.cannotEdit(ctx, l.ID, "You can't add items to this list.")
		}
		// The limits come before any of the work.
		if len(in.Body.StoreLinks) > item.MaxStoreLinks {
			return nil, itemProblem(item.ErrTooManyStoreLinks)
		}
		if len(in.Body.ImageIDs) > item.MaxImages {
			return nil, itemProblem(item.ErrTooManyImages)
		}
		n := item.New{Title: in.Body.Title, PriceMinor: in.Body.PriceMinor.Value, TopPriceMinor: in.Body.TopPriceMinor.Value,
			Quantity: in.Body.Quantity, IsUnlimited: in.Body.IsUnlimited}
		if in.Body.Notes != nil {
			n.Notes = *in.Body.Notes
		}
		if in.Body.Currency.Value != nil {
			n.Currency = *in.Body.Currency.Value
		}
		var copied *image.Image
		if raw := in.Body.FromGiftID; raw != nil {
			bodyPrice := in.Body.PriceMinor.Set || in.Body.TopPriceMinor.Set || in.Body.Currency.Set
			if err := s.addAgain(ctx, caller.User, *raw, bodyPrice, in.Body.ImageIDs != nil, &n, &copied); err != nil {
				return nil, err
			}
		}
		var guard item.AddGuard
		if n.StoreLinks, err = s.newStoreLinks(ctx, in.Body.StoreLinks); err != nil {
			return nil, err
		}
		if n.ImageIDs, n.PrimaryImageID, guard, err = s.newImages(caller.User, in.Body.ImageIDs, in.Body.PrimaryImageID); err != nil {
			return nil, err
		}
		it, err := s.opts.Items.Add(ctx, caller.User, l.ID, n, guard)
		if err != nil {
			if copied != nil {
				// The copy's row went with the rollback, so its file is an orphan.
				_ = s.opts.Images.Discard(*copied)
			}
			return nil, itemProblem(err)
		}
		return createdOf("/items/"+formatID(it.ID), newItemBody(it))
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-item",
		Method:      http.MethodGet,
		Path:        "/items/{id}",
		Summary:     "An item",
		Description: "An item on a list the caller can't see is a 404.",
		Tags:        []string{"Items"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *itemInput) (*resourceOutput[ItemBody], error) {
		it, err := s.visibleItem(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newItemBody(it))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-item",
		Method:      http.MethodPatch,
		Path:        "/items/{id}",
		Summary:     "Change, reorder or move an item",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. " +
			"A user who can edit the item's list changes it, whoever added it. A new `position` moves the item and the others shift. A new `list_id` moves the item to the end of another list. " +
			"A field the caller sees but can't change is a 403 `field_not_writable`. `If-Match` is optional.",
		Tags:   []string{"Items"},
		Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchItemInput) (*resourceOutput[ItemBody], error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleItem(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !current.CanEdit {
			return nil, s.cannotEdit(ctx, current.ListID, "You can't change the items on this list.")
		}
		visible, err := keysOf(newItemBody(current))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(itemChangeKeys, visible); err != nil {
			return nil, err
		}
		changes, err := in.Body.Changes.changes()
		if err != nil {
			return nil, err
		}
		updated, err := s.opts.Items.Update(ctx, caller.User, current.ID, changes, itemChangeGuard(caller.User, in.IfMatch, changes))
		if err != nil {
			return nil, itemProblem(err)
		}
		return resourceOf(newItemBody(updated))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-item",
		Method:        http.MethodDelete,
		Path:          "/items/{id}",
		Summary:       "Delete an item",
		Description:   "A user who can edit the item's list deletes it, and the items after it close the gap. A parent of a deactivated recipient gets a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:          []string{"Items"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteItemInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		current, err := s.visibleItem(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if !current.CanEdit {
			return nil, s.cannotEdit(ctx, current.ListID, "You can't change the items on this list.")
		}
		if err := s.opts.Items.Delete(ctx, caller.User, current.ID, itemVersionGuard(in.IfMatch)); err != nil {
			return nil, itemProblem(err)
		}
		return nil, nil
	})

	acceptMergePatch(api, "/items/{id}")
}
