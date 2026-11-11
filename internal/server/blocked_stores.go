package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/blocklist"
)

// BlockedStoreBody is the full blocked store (docs/routes.md, Blocked
// stores). Only the admin sees one. It has no list of the items that link to
// the store, because the admin has no access to content.
type BlockedStoreBody struct {
	ID        string    `json:"id" example:"5" doc:"An opaque string. Don't parse it."`
	Store     string    `json:"store" example:"temu.com" doc:"A lowercase host with no www. and no trailing dot. It matches that host and its subdomains."`
	CreatedAt time.Time `json:"created_at"`

	CanDelete bool `json:"can_delete" doc:"True when the caller can allow the store again: the admin."`
}

// blockedStoreRow is a blocked store in a collection: the full resource with
// its version.
type blockedStoreRow struct {
	BlockedStoreBody
	Version string `json:"version" doc:"The same value as the ETag of the create response for this caller. Send it in If-Match."`
}

func newBlockedStoreBody(b blocklist.Store) BlockedStoreBody {
	return BlockedStoreBody{
		ID:        formatID(b.ID),
		Store:     b.Store,
		CreatedAt: b.CreatedAt.UTC().Truncate(time.Second),
		// Only the admin reaches these routes, so whoever sees the store can
		// delete it.
		CanDelete: true,
	}
}

// blockedStoreProblem turns an error of the blocklist package into a problem
// with a code.
func blockedStoreProblem(err error) error {
	switch {
	case errors.Is(err, blocklist.ErrNotFound):
		return notFound()
	case errors.Is(err, blocklist.ErrInvalid):
		return Error(http.StatusUnprocessableEntity, "invalid_input", err.Error(),
			&huma.ErrorDetail{Location: "body.store", Message: err.Error()})
	case errors.Is(err, blocklist.ErrAlreadyBlocked):
		return Error(http.StatusConflict, "already_blocked", "That store is already blocked.")
	}
	return err
}

var blockedStoreRules = collectionRules{Name: "blocked-stores", Sorts: blocklist.SortKeys}

type blockStoreInput struct {
	Body struct {
		Store string `json:"store" maxLength:"2048" example:"temu.com" doc:"A host name such as temu.com, or a web address of the store. The store is named by its host."`
	}
}

type allowStoreInput struct {
	ID      string `path:"id" example:"5" doc:"A blocked store's ID."`
	IfMatch string `header:"If-Match" doc:"The ETag (or a row's version) of the blocked store as you loaded it. If it changed since, the answer is 412 resource_changed and nothing is removed."`
}

func (s *Server) registerBlockedStores(api huma.API) {
	tags := []string{"Blocked stores"}

	listOp := huma.Operation{
		OperationID: "list-blocked-stores",
		Method:      http.MethodGet,
		Path:        "/blocked-stores",
		Summary:     "The blocked stores",
		Description: "Only the admin.",
		Tags:        tags,
		Errors:      []int{http.StatusUnauthorized, http.StatusForbidden},
	}
	blockedStoreRules.apply(&listOp)
	huma.Register(api, listOp, listing(func(ctx context.Context, in *struct{ PageParams }) (*collectionOutput[blockedStoreRow], error) {
		if err := requireAdmin(ctx, "sees the blocked stores"); err != nil {
			return nil, err
		}
		req, err := in.parse(blockedStoreRules)
		if err != nil {
			return nil, err
		}
		stores, total, next, err := s.opts.Blocked.List(ctx, req)
		if err != nil {
			return nil, err
		}
		out := &collectionOutput[blockedStoreRow]{}
		out.Body.Data = make([]blockedStoreRow, len(stores))
		for i, b := range stores {
			body := newBlockedStoreBody(b)
			v, err := versionOf(body)
			if err != nil {
				return nil, err
			}
			out.Body.Data[i] = blockedStoreRow{BlockedStoreBody: body, Version: v}
		}
		out.Body.NextCursor = next
		out.Body.TotalCount = total
		return out, nil
	}))

	huma.Register(api, huma.Operation{
		OperationID: "block-store",
		Method:      http.MethodPost,
		Path:        "/blocked-stores",
		Summary:     "Block a store for the whole instance",
		Description: "Only the admin. Takes a host name or a web address. Links from the store and its subdomains can't be added as store links or searched. " +
			"Existing items keep their links. Blocking a store that is blocked changes nothing, and is a 409 `already_blocked`: a client can treat that as success.",
		Tags:          tags,
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity},
	}, func(ctx context.Context, in *blockStoreInput) (*createdOutput[BlockedStoreBody], error) {
		if err := requireAdmin(ctx, "blocks stores"); err != nil {
			return nil, err
		}
		b, err := s.opts.Blocked.Add(ctx, strings.TrimSpace(in.Body.Store))
		if err != nil {
			return nil, blockedStoreProblem(err)
		}
		return createdOf("/blocked-stores/"+formatID(b.ID), newBlockedStoreBody(b))
	})

	huma.Register(api, huma.Operation{
		OperationID:   "allow-store",
		Method:        http.MethodDelete,
		Path:          "/blocked-stores/{id}",
		Summary:       "Allow a store again",
		Description:   "Only the admin. Takes the store off the blocked list. `If-Match` is optional.",
		Tags:          tags,
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusPreconditionFailed},
	}, func(ctx context.Context, in *allowStoreInput) (*struct{}, error) {
		if err := requireAdmin(ctx, "allows stores again"); err != nil {
			return nil, err
		}
		id, ok := parseID(in.ID)
		if !ok {
			return nil, notFound()
		}
		guard := func(_ context.Context, current blocklist.Store) error {
			v, err := versionOf(newBlockedStoreBody(current))
			if err != nil {
				return err
			}
			return checkIfMatch(in.IfMatch, v)
		}
		if err := s.opts.Blocked.Remove(ctx, id, guard); err != nil {
			return nil, blockedStoreProblem(err)
		}
		return nil, nil
	})
}
