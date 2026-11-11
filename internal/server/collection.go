package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"jsclayton.dev/wishbone/internal/page"
)

// Collection is the envelope that every collection returns (docs/spec.md,
// API principles, Collections). T is the row: a full resource with its
// `version` (see [versionOf]).
type Collection[T any] struct {
	Data       []T     `json:"data" nullable:"false" doc:"Full resources. The route says which, and in what order."`
	NextCursor *string `json:"next_cursor" nullable:"true" doc:"An opaque string, or null on the last page. Send it back as ?cursor=. Always follow it: a collection can start to page later."`
	TotalCount int     `json:"total_count" doc:"How many resources the caller can see in the whole collection, not only on this page."`
}

// collectionOutput is the output of a route that returns a collection.
type collectionOutput[T any] struct {
	Body Collection[T]
}

// PageParams are the query parameters that every collection takes. A route
// embeds them in its input. The route's own filters are named in its
// [collectionRules]. `sort` is checked there too, and read from the input by
// [PageParams.parse].
type PageParams struct {
	Cursor string `query:"cursor" doc:"The next_cursor of the previous page. It belongs to its sort: sending it with another sort is a 422 cursor_mismatch."`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" doc:"How many resources a page holds, from 1 to 200. The default is 100."`
	Sort   string `query:"sort" doc:"A sort key. A - in front of it reverses the order. The default is the route's first key. The ID is always the last tiebreaker."`
}

// parse reads the page request. The sort was already checked by the route's
// [collectionRules], so only a cursor can fail here.
func (p PageParams) parse(rules collectionRules) (page.Request, error) {
	req, err := page.Parse(rules.Name, rules.Sorts, p.Sort, p.Cursor, p.Limit)
	if err != nil {
		return req, pageProblem(err, p.Sort, rules.Sorts)
	}
	return req, nil
}

// pageProblem turns an error of the page package into a problem with a code.
// Any other error comes back as it is.
func pageProblem(err error, sort string, keys []page.Key) error {
	switch {
	case errors.Is(err, page.ErrCursorMismatch):
		return Error(http.StatusUnprocessableEntity, "cursor_mismatch", "That cursor is from a different collection or sort. Start again from the first page.")
	case errors.Is(err, page.ErrBadCursor):
		return Error(http.StatusUnprocessableEntity, "invalid_input", "That is not a cursor this server made.",
			&huma.ErrorDetail{Location: "query.cursor", Message: "not a cursor"})
	case errors.Is(err, page.ErrUnknownSort):
		return unknownSort(sort, keys)
	}
	return err
}

// listing wraps the handler of a collection route, so a page error that a
// domain package returns (a cursor it cannot read) is a 422 like the ones that
// parse returns, and never a 500. Every collection route registers its handler
// through it.
func listing[I, O any](h func(context.Context, *I) (*O, error)) func(context.Context, *I) (*O, error) {
	return func(ctx context.Context, in *I) (*O, error) {
		out, err := h(ctx, in)
		if errors.Is(err, page.ErrBadCursor) || errors.Is(err, page.ErrCursorMismatch) {
			return nil, pageProblem(err, "", nil)
		}
		return out, err
	}
}

// OptionalParam is a query parameter that tells "not sent" from its zero value,
// for a filter such as `?can_edit=false`. huma has no pointer parameters, so a
// route's input uses this type, and reads Value only when IsSet is true.
type OptionalParam[T any] struct {
	Value T
	IsSet bool
}

// Receiver is where huma parses the parameter.
func (o *OptionalParam[T]) Receiver() reflect.Value { return reflect.ValueOf(o).Elem().Field(0) }

// OnParamSet records whether the request sent the parameter.
func (o *OptionalParam[T]) OnParamSet(isSet bool, _ any) { o.IsSet = isSet }

// Schema is the schema of T: the parameter is a T in the document.
func (OptionalParam[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), false, "")
	return &s
}

// Pointer is the value, or nil when the request left the parameter out.
func (o OptionalParam[T]) Pointer() *T {
	if !o.IsSet {
		return nil
	}
	return &o.Value
}

// collectionRules are the filters and sort keys that a collection route knows
// (docs/routes.md gives them for each route). Anything else in the query is a
// 422, which a client sees as soon as it asks.
type collectionRules struct {
	// Name names the collection. A cursor carries it, so it never fits another.
	Name string
	// Sorts are the sort keys, the default first.
	Sorts []page.Key
	// Filters are the names of the query parameters that filter the route:
	// the fields it matches, and the filters that are not fields.
	Filters []string
}

// apply makes the operation check its query with the rules, and describe them.
// Call it on the operation before huma.Register.
func (c collectionRules) apply(op *huma.Operation) {
	if c.Name == "" {
		// A cursor carries the name, so two collections without one could share
		// cursors. Refuse it when the route is registered, not on a request.
		panic("server: a collection needs a name")
	}
	op.Middlewares = append(op.Middlewares, c.middleware())
	op.Errors = append(op.Errors, http.StatusUnprocessableEntity)

	keys := make([]string, len(c.Sorts))
	for i, k := range c.Sorts {
		keys[i] = "`" + k.Name + "`"
	}
	filters := "none"
	if len(c.Filters) > 0 {
		quoted := make([]string, len(c.Filters))
		for i, f := range c.Filters {
			quoted[i] = "`" + f + "`"
		}
		filters = strings.Join(quoted, ", ")
	}
	op.Description += fmt.Sprintf(" Sort keys: %s (the default is the first). Filters: %s.", strings.Join(keys, ", "), filters)
}

func unknownSort(got string, keys []page.Key) *Problem {
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = k.Name
	}
	return Error(http.StatusUnprocessableEntity, "unknown_sort",
		fmt.Sprintf("This collection cannot sort by %q. Sort keys: %s.", got, strings.Join(names, ", ")),
		&huma.ErrorDetail{Location: "query.sort", Message: "not a sort key", Value: got})
}

func (c collectionRules) middleware() func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		u := ctx.URL()
		query := u.Query()
		var unknown []string
		for name := range query {
			switch name {
			case "cursor", "limit", "sort":
			default:
				if !slices.Contains(c.Filters, name) {
					unknown = append(unknown, name)
				}
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			writeProblemOn(ctx, Error(http.StatusUnprocessableEntity, "unknown_filter",
				fmt.Sprintf("This collection has no filter %q.", unknown[0]),
				&huma.ErrorDetail{Location: "query." + unknown[0], Message: "not a filter of this collection"}))
			return
		}
		if query.Has("sort") {
			name := strings.TrimPrefix(query.Get("sort"), "-")
			if !slices.ContainsFunc(c.Sorts, func(k page.Key) bool { return k.Name == name }) {
				writeProblemOn(ctx, unknownSort(query.Get("sort"), c.Sorts))
				return
			}
		}
		next(ctx)
	}
}

// writeProblemOn answers from a middleware with a problem that has its own code.
func writeProblemOn(ctx huma.Context, p *Problem) {
	ctx.SetHeader("Content-Type", "application/problem+json")
	ctx.SetStatus(p.Status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(p)
}
