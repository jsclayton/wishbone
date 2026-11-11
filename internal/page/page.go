// Package page holds the paging and sort rules that every collection shares
// (docs/spec.md, API principles, Collections): the sort keys a route allows,
// the cursor that belongs to a sort, and the page size. It imports no HTTP
// package. A domain package takes a [Request] and runs a keyset query with it;
// internal/server turns the errors into problems with a code.
//
// To make a collection, a route:
//
//  1. lists its sort keys, the default first, as [Key] values;
//  2. calls [Parse] with its name, the query's sort, cursor and limit. Parse
//     reads the cursor's value, so a cursor that cannot be read is an error
//     there and never reaches the database;
//  3. asks the database for [Request.Limit]+1 rows after [Request.After], in
//     the order [Request.Desc] gives, with the ID as the last tiebreaker;
//  4. calls [Slice] on the rows to cut the page and get the next cursor.
package page

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	// DefaultLimit is the page size when a request names none.
	DefaultLimit = 100
	// MaxLimit is the largest page a request can ask for.
	MaxLimit = 200
)

var (
	// ErrUnknownSort means the sort key is not one the route allows.
	ErrUnknownSort = errors.New("unknown sort key")
	// ErrCursorMismatch means the cursor was made for another sort.
	ErrCursorMismatch = errors.New("the cursor belongs to a different sort")
	// ErrBadCursor means the cursor is not one the server made.
	ErrBadCursor = errors.New("the cursor is not valid")
)

// Key is a sort key that a route allows. A route lists its keys with the
// default first.
type Key struct {
	Name string
	// Time is true when the key's values are times. A position's value is then
	// RFC 3339 text (see [FormatTime]), and [Parse] refuses a cursor whose value
	// is not.
	Time bool
	// Descending is the key's own order, for a key that reads best from the
	// largest value: sessions sort by `last_used_at` newest first. A `-` in
	// front of the key in the request reverses it.
	Descending bool
}

// FormatTime writes a time as the value of a position of a [Key] with Time set.
func FormatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Position is where a row sits in a sort: the value of the sort key, written
// as text by the domain package, and the ID, the last tiebreaker.
type Position struct {
	Value string
	ID    int64
}

// Request is a parsed page request.
type Request struct {
	// Sort is the sort as the client writes it, with the `-` when it reverses
	// the key's own order. A cursor carries it, so it never fits another sort.
	Sort string
	// Key is the sort key, without the `-`.
	Key string
	// Desc is true when rows come largest first. The ID tiebreaker follows the
	// same direction.
	Desc  bool
	Limit int
	// collection is the name the request was parsed for. A cursor made from the
	// request carries it.
	collection string
	// After is the position of the last row of the previous page, or nil for
	// the first page.
	After *Position
}

// cursor is what the opaque cursor string holds.
type cursor struct {
	// Collection is the name of the collection, so a cursor from one collection
	// never fits another that has a sort key of the same name.
	Collection string `json:"c"`
	Sort       string `json:"s"`
	Value      string `json:"v"`
	ID         int64  `json:"i"`
}

// Parse reads a request's sort, cursor and limit for the collection with the
// given name. An empty sort is the first key. An empty cursor is the first page,
// and a limit of 0 is [DefaultLimit]. It returns [ErrUnknownSort],
// [ErrCursorMismatch] (the cursor is from another collection or sort), or
// [ErrBadCursor] (it is not one the server made, or its value cannot be read).
func Parse(collection string, keys []Key, sort, cur string, limit int) (Request, error) {
	if collection == "" {
		// A cursor carries the name. Without one, two collections with a key of
		// the same name would share cursors.
		return Request{}, errors.New("page: a collection needs a name")
	}
	if len(keys) == 0 {
		return Request{}, errors.New("page: a collection needs a sort key")
	}
	name, reversed := strings.CutPrefix(sort, "-")
	if sort == "" {
		name = keys[0].Name
	}
	var key *Key
	for i := range keys {
		if keys[i].Name == name {
			key = &keys[i]
		}
	}
	if key == nil {
		return Request{}, ErrUnknownSort
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	r := Request{collection: collection, Sort: name, Key: name, Desc: key.Descending != reversed, Limit: min(limit, MaxLimit)}
	if reversed {
		r.Sort = "-" + name
	}
	if cur == "" {
		return r, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return Request{}, ErrBadCursor
	}
	var c cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.ID < 1 {
		return Request{}, ErrBadCursor
	}
	if c.Collection != collection || c.Sort != r.Sort {
		return Request{}, ErrCursorMismatch
	}
	if key.Time {
		if _, err := time.Parse(time.RFC3339Nano, c.Value); err != nil {
			return Request{}, ErrBadCursor
		}
	}
	r.After = &Position{Value: c.Value, ID: c.ID}
	return r, nil
}

// Slice cuts a page from rows that were fetched with a limit of
// [Request.Limit]+1. It returns the page, and the cursor for the next page:
// nil when no row is left. position gives the position of a row.
func Slice[T any](r Request, rows []T, position func(T) Position) (page []T, next *string) {
	if len(rows) <= r.Limit {
		return rows, nil
	}
	page = rows[:r.Limit]
	last := position(page[len(page)-1])
	raw, err := json.Marshal(cursor{Collection: r.collection, Sort: r.Sort, Value: last.Value, ID: last.ID})
	if err != nil {
		return page, nil // a string and a number always marshal
	}
	s := base64.RawURLEncoding.EncodeToString(raw)
	return page, &s
}
