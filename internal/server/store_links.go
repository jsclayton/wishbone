package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/extract/pipeline"
	"jsclayton.dev/wishbone/internal/item"
)

// storeLinkRow is a store link in a collection: the full store link with its
// version.
type storeLinkRow struct {
	StoreLinkBody
	Version string `json:"version" doc:"The same value as the ETag of GET /items/{id}/store-links/{store_link_id} for this caller. Send it in If-Match."`
}

// storeLinkRules has no sort key and no filter: an item has at most 10 store
// links, and the order depends on the caller (see [StoreLinkBody]).
var storeLinkRules = collectionRules{Name: "store-links"}

type storeLinkPath struct {
	ID          string `path:"id" example:"12" doc:"An item's ID."`
	StoreLinkID string `path:"store_link_id" example:"4" doc:"A store link's ID."`
}

type addStoreLinkInput struct {
	ID   string `path:"id" example:"12" doc:"An item's ID."`
	Body struct {
		URL string `json:"url" minLength:"1" maxLength:"2048" doc:"An http or https address. It is normalized, checked against the blocked stores, and read for its price."`
	}
}

// storeLinkChanges are the fields of a store link that a caller who can edit the
// item's list may change with PATCH.
type storeLinkChanges struct {
	PriceMinor Field[int64]  `json:"price_minor,omitempty" nullable:"true" minimum:"0" doc:"The store's price in the currency's minor unit. null clears the price and its currency."`
	Currency   Field[string] `json:"currency,omitempty" nullable:"true" pattern:"^[A-Z]{3}$" doc:"An ISO 4217 code. A price with no currency keeps the link's currency, or is in USD. A currency needs a price."`
}

var storeLinkChangeKeys = []string{"price_minor", "currency"}

type patchStoreLinkInput struct {
	ID          string `path:"id" example:"12" doc:"An item's ID."`
	StoreLinkID string `path:"store_link_id" example:"4" doc:"A store link's ID."`
	IfMatch     string `header:"If-Match" doc:"The ETag (or a row's version) of the store link as you loaded it. If it changed since, the answer is 412 resource_changed and nothing changes."`
	Body        MergePatch[storeLinkChanges]
}

type deleteStoreLinkInput struct {
	ID          string `path:"id" example:"12" doc:"An item's ID."`
	StoreLinkID string `path:"store_link_id" example:"4" doc:"A store link's ID."`
	IfMatch     string `header:"If-Match" doc:"The ETag (or a row's version) of the store link as you loaded it. If it changed since, the answer is 412 resource_changed and nothing is deleted."`
}

// storeLinkVersionGuard compares the If-Match header with the version of the
// store link as the caller sees it (an editor), inside the write's transaction.
func storeLinkVersionGuard(ifMatch string) item.StoreLinkGuard {
	return func(_ context.Context, _ sqlx.ExtContext, current item.StoreLink) error {
		v, err := versionOf(newStoreLinkBody(current, true))
		if err != nil {
			return err
		}
		return checkIfMatch(ifMatch, v)
	}
}

// visibleStoreLink loads the item at a path and finds the store link under it,
// for a caller who must be able to see the item. Either missing, or an item the
// caller can't see, is a 404.
func (s *Server) visibleStoreLink(ctx context.Context, rawItemID, rawLinkID string) (item.Item, item.StoreLink, error) {
	it, err := s.visibleItem(ctx, rawItemID)
	if err != nil {
		return item.Item{}, item.StoreLink{}, err
	}
	id, ok := parseID(rawLinkID)
	if !ok {
		return item.Item{}, item.StoreLink{}, notFound()
	}
	for _, l := range it.StoreLinks {
		if l.ID == id {
			return it, l, nil
		}
	}
	return item.Item{}, item.StoreLink{}, notFound()
}

// editableItem loads the item at a path for a caller who must be able to change
// it. One they see and can't change is a 403.
func (s *Server) editableItem(ctx context.Context, rawID string) (item.Item, error) {
	it, err := s.visibleItem(ctx, rawID)
	if err != nil {
		return item.Item{}, err
	}
	if !it.CanEdit {
		return item.Item{}, s.cannotEdit(ctx, it.ListID, "You can't change the items on this list.")
	}
	return it, nil
}

var errStoreLinkLimit = Error(http.StatusTooManyRequests, "rate_limited",
	"You have looked up a lot of links and photos. Try again later.")

// readStoreLink reads a link for a new store link: the address it ends at, its
// price, and the store's picture. Without a search pipeline, or when the page
// has nothing, the link has no price. The read counts against the hourly limit
// unless it comes from the cache.
func (s *Server) readStoreLink(ctx context.Context, userID int64, norm string) (item.NewStoreLink, error) {
	link := item.NewStoreLink{URL: norm, Store: extract.StoreFromURL(norm)}
	if s.opts.Search == nil {
		return link, nil
	}
	res, final, err := s.readLink(ctx, userID, norm, "body.url", errStoreLinkLimit)
	if err != nil {
		return link, err
	}
	link.URL, link.Store = final, extract.StoreFromURL(final)
	if minor, code, ok := minorPrice(res.Price, res.Currency); ok {
		link.PriceMinor, link.Currency = &minor, code
	}
	// The picture is best effort: an address that can't be used is dropped.
	if imgs := imageAddresses(res.Images); len(imgs) > 0 {
		if img, err := extract.NormalizeURL(imgs[0]); err == nil {
			link.ImageURL = img
		}
	}
	return link, nil
}

// refreshDeadline is the most time one refresh runs. It is under a typical
// proxy timeout, so a slow store gives partial results instead of a cut-off
// request. A store link not reached keeps its old price.
var refreshDeadline = 75 * time.Second

// refreshOutcome says what a refresh did.
type refreshOutcome struct {
	// reads are the prices it read, for the links that had a readable price.
	reads []item.ReadPrice
	// read is true when at least one page was read, which counted against the
	// hourly limit, whether or not it gave a price.
	read bool
	// limited is true when the hourly limit stopped the refresh.
	limited bool
	// err is a failure that stopped the refresh, such as a database error in the
	// blocked-store check. The reads before it were paid for, so the caller saves them.
	err error
}

// refreshPrices reads the price of each store link of the item again, skipping
// the cache. A link that can't be read, has no readable price, or is in a store
// blocked since it was added keeps its price.
func (s *Server) refreshPrices(ctx context.Context, userID int64, it item.Item) refreshOutcome {
	ctx, cancel := context.WithTimeout(ctx, refreshDeadline)
	defer cancel()
	var out refreshOutcome
	for _, l := range it.StoreLinks {
		if ctx.Err() != nil {
			break
		}
		// A store blocked after the link was added is not fetched. The stored
		// address was normalized when it was saved, and unblockedAddress does it
		// again and asks the blocklist, as every route that takes an address does.
		if _, err := s.unblockedAddress(ctx, l.URL, "body.url"); err != nil {
			var problem *Problem
			if errors.As(err, &problem) {
				continue
			}
			out.err = err
			return out
		}
		res, _, err := s.readLink(ctx, userID, l.URL, "body.url", errStoreLinkLimit, pipeline.BypassCache())
		switch {
		case errors.Is(err, errStoreLinkLimit):
			out.limited = true
			return out
		case err != nil:
			// The page can't be read, or it now redirects to a blocked store. Either
			// way the fetch happened, so it counted.
			out.read = true
			continue
		}
		out.read = true
		currency := res.Currency
		if currency == "" && l.Currency != nil {
			currency = *l.Currency
		}
		if minor, code, ok := minorPrice(res.Price, currency); ok {
			out.reads = append(out.reads, item.ReadPrice{LinkID: l.ID, PriceMinor: minor, Currency: code})
		}
	}
	return out
}

func (s *Server) registerStoreLinks(api huma.API) {
	listOp := huma.Operation{
		OperationID: "list-store-links",
		Method:      http.MethodGet,
		Path:        "/items/{id}/store-links",
		Summary:     "The store links of an item",
		Description: "The places where the item can be bought. A caller who can edit the item's list gets them in the order added. Every other caller gets them by price: within one currency the lowest first and the unpriced last, and with prices in several currencies the order added. An item has at most 10, so there is one page. There are no sort keys and no filters.",
		Tags:        []string{"Store links"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity},
		Middlewares: huma.Middlewares{storeLinkRules.middleware()},
	}
	huma.Register(api, listOp, listing(func(ctx context.Context, in *itemInput) (*collectionOutput[storeLinkRow], error) {
		it, err := s.visibleItem(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		rows := make([]storeLinkRow, len(it.StoreLinks))
		for i, l := range it.StoreLinks {
			body := newStoreLinkBody(l, it.CanEdit)
			v, err := versionOf(body)
			if err != nil {
				return nil, err
			}
			rows[i] = storeLinkRow{StoreLinkBody: body, Version: v}
		}
		out := &collectionOutput[storeLinkRow]{}
		out.Body.Data = rows
		out.Body.TotalCount = len(rows)
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID: "get-store-link",
		Method:      http.MethodGet,
		Path:        "/items/{id}/store-links/{store_link_id}",
		Summary:     "A store link",
		Description: "A store link of an item on a list the caller can't see is a 404.",
		Tags:        []string{"Store links"},
		Errors:      []int{http.StatusUnauthorized, http.StatusNotFound},
	}, func(ctx context.Context, in *storeLinkPath) (*resourceOutput[StoreLinkBody], error) {
		it, l, err := s.visibleStoreLink(ctx, in.ID, in.StoreLinkID)
		if err != nil {
			return nil, err
		}
		return resourceOf(newStoreLinkBody(l, it.CanEdit))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "create-store-link",
		Method:        http.MethodPost,
		Path:          "/items/{id}/store-links",
		Summary:       "Add a store link to an item",
		Description:   "A user who can edit the item's list adds a store link. The server reads the page for its price and the store's picture, and the read counts against the caller's hourly limit unless it comes from the 24-hour cache. Over the limit is a 429 `rate_limited` and nothing is added. A page that can't be read gives a store link with no price. A link from a blocked store is a 422 `store_blocked`, before the read, and after it for a link that redirects there (that one counts). An item with no price that never had one takes the price of the store link. A caller who can edit the list only as the parent of a deactivated recipient gets a 403 `user_deactivated`.",
		Tags:          []string{"Store links"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *addStoreLinkInput) (*createdOutput[StoreLinkBody], error) {
		caller, _ := CallerFrom(ctx)
		it, err := s.editableItem(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		// The limit comes before any of the work.
		if len(it.StoreLinks) >= item.MaxStoreLinks {
			return nil, itemProblem(item.ErrTooManyStoreLinks)
		}
		norm, err := s.unblockedAddress(ctx, in.Body.URL, "body.url")
		if err != nil {
			return nil, err
		}
		n, err := s.readStoreLink(ctx, caller.User.ID, norm)
		if err != nil {
			return nil, err
		}
		l, err := s.opts.Items.AddStoreLink(ctx, caller.User, it.ID, n)
		if err != nil {
			return nil, itemProblem(err)
		}
		return createdOf("/items/"+formatID(it.ID)+"/store-links/"+formatID(l.ID), newStoreLinkBody(l, true))
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-store-link",
		Method:      http.MethodPatch,
		Path:        "/items/{id}/store-links/{store_link_id}",
		Summary:     "Set, change or clear the price of a store link",
		Description: "JSON Merge Patch (RFC 7396), as `application/merge-patch+json` or `application/json`. A user who can edit the item's list sets the price and currency by hand. The captured date becomes now when the change sets a price or clears one. A later refresh replaces a typed price. An item with no price that never had one takes the first price a store link gets. A caller who can edit the list only as the parent of a deactivated recipient gets a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:        []string{"Store links"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *patchStoreLinkInput) (*resourceOutput[StoreLinkBody], error) {
		caller, _ := CallerFrom(ctx)
		it, l, err := s.visibleStoreLink(ctx, in.ID, in.StoreLinkID)
		if err != nil {
			return nil, err
		}
		if !it.CanEdit {
			return nil, s.cannotEdit(ctx, it.ListID, "You can't change the items on this list.")
		}
		visible, err := keysOf(newStoreLinkBody(l, it.CanEdit))
		if err != nil {
			return nil, err
		}
		if err := in.Body.Refuse(storeLinkChangeKeys, visible); err != nil {
			return nil, err
		}
		c := item.StoreLinkChanges{PriceMinor: in.Body.Changes.PriceMinor.Optional, Currency: in.Body.Changes.Currency.Optional}
		updated, err := s.opts.Items.ChangeStoreLink(ctx, caller.User, it.ID, l.ID, c, storeLinkVersionGuard(in.IfMatch))
		if err != nil {
			return nil, itemProblem(err)
		}
		return resourceOf(newStoreLinkBody(updated, true))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-store-link",
		Method:        http.MethodDelete,
		Path:          "/items/{id}/store-links/{store_link_id}",
		Summary:       "Delete a store link",
		Description:   "A user who can edit the item's list deletes it. The item stays, with the price it has, even with no store links. A parent of a deactivated recipient gets a 403 `user_deactivated`. `If-Match` is optional.",
		Tags:          []string{"Store links"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *deleteStoreLinkInput) (*struct{}, error) {
		caller, _ := CallerFrom(ctx)
		it, l, err := s.visibleStoreLink(ctx, in.ID, in.StoreLinkID)
		if err != nil {
			return nil, err
		}
		if !it.CanEdit {
			return nil, s.cannotEdit(ctx, it.ListID, "You can't change the items on this list.")
		}
		if err := s.opts.Items.DeleteStoreLink(ctx, caller.User, it.ID, l.ID, storeLinkVersionGuard(in.IfMatch)); err != nil {
			return nil, itemProblem(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "refresh-prices",
		Method:      http.MethodPost,
		Path:        "/items/{id}/refresh-prices",
		Summary:     "Refresh the prices of an item's store links",
		Description: "A user who can edit the item's list reads each store link's page again, skipping the cache, and saves the new price with the time it was captured. Each read counts against the caller's hourly limit. It changes only the store links' prices, never the item price, the title or the images. A store link that can't be read, or has no readable price, keeps its price and date. The answer does not say which links were read, and `captured_at` has whole-second precision, so a client can tell a changed price from the item, but not a read that found the same price from a read that failed. When the hourly limit stops the refresh, the prices read before it are saved and the answer is the item; when it stops it before the first page is read (a read that fails or finds no price counts as a read), the answer is a 429 `rate_limited`. A parent of a deactivated recipient gets a 403 `user_deactivated`. Returns the item.",
		Tags:        []string{"Items"},
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests},
	}, func(ctx context.Context, in *itemInput) (*resourceOutput[ItemBody], error) {
		caller, _ := CallerFrom(ctx)
		it, err := s.editableItem(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if s.opts.Search == nil || len(it.StoreLinks) == 0 {
			return resourceOf(newItemBody(it))
		}
		out := s.refreshPrices(ctx, caller.User.ID, it)
		// Over the limit before the first read: nothing was read, nothing changes.
		if out.limited && !out.read {
			return nil, errStoreLinkLimit
		}
		if out.err != nil {
			// Reads that were paid for are saved. Only a refresh that read nothing fails.
			if !out.read {
				return nil, out.err
			}
			s.opts.Logger.Error("refresh stopped early", slog.Any("error", out.err))
		}
		// Save even if the request ended just now: the prices were paid for.
		saved, err := s.opts.Items.SavePrices(context.WithoutCancel(ctx), caller.User, it.ID, out.reads)
		if err != nil {
			return nil, itemProblem(err)
		}
		return resourceOf(newItemBody(saved))
	})

	acceptMergePatch(api, "/items/{id}/store-links/{store_link_id}")
}
