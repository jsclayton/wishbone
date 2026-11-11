// Package blocklist is the instance's list of blocked stores (docs/spec.md,
// Server-wide settings). Only the admin edits it. A link from a blocked store,
// or from a subdomain of one, can't be added as a store link or searched.
//
// Other packages ask one question: [Service.Match] says whether a URL is from
// a blocked store.
package blocklist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/extract"
	"jsclayton.dev/wishbone/internal/page"
)

var (
	// ErrInvalid means the input is not a store: a web address or a host name.
	// Its message is safe to show.
	ErrInvalid = errors.New("enter a store's web address, such as temu.com")
	// ErrAlreadyBlocked means the store is already blocked.
	ErrAlreadyBlocked = errors.New("the store is already blocked")
	// ErrNotFound means no blocked store has the ID.
	ErrNotFound = errors.New("no such blocked store")
)

// SortKeys are the sort keys of a collection of blocked stores
// (docs/routes.md, Blocked stores). The first is the default.
var SortKeys = []page.Key{{Name: "store"}}

// Store is one blocked store.
type Store struct {
	ID int64 `db:"id"`
	// Store is a lowercase host such as temu.com, with no "www." and no
	// trailing dot. It matches that host and its subdomains.
	Store     string    `db:"store"`
	CreatedAt time.Time `db:"created_at"`
}

// Normalize returns the store that a hand-typed entry names: the host,
// lowercase, without "www." or a trailing dot. It takes a bare host or a whole
// link. It returns [ErrInvalid] for anything else.
func Normalize(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || strings.ContainsAny(input, " \t\r\n") {
		return "", ErrInvalid
	}
	norm, err := extract.NormalizeURL(input)
	if err != nil {
		return "", ErrInvalid
	}
	store := extract.StoreFromURL(norm)
	if !strings.Contains(store, ".") || strings.HasPrefix(store, ".") || strings.HasSuffix(store, ".") || strings.Contains(store, "..") {
		return "", ErrInvalid
	}
	return store, nil
}

// hostChain returns host and each parent of it that still has a dot:
// "a.b.temu.com" gives a.b.temu.com, b.temu.com, temu.com.
func hostChain(host string) []string {
	var out []string
	for strings.Contains(host, ".") {
		out = append(out, host)
		_, host, _ = strings.Cut(host, ".")
	}
	return out
}

// Service is the blocked stores of one database.
type Service struct {
	db  *sqlx.DB
	now func() time.Time
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// Match reports whether rawURL is on a blocked store or a subdomain of one.
// store is the blocked store as listed, such as temu.com for m.temu.com. The
// most specific listed store wins. A string that isn't a web address is never
// blocked. A link with a cached reading is checked at the address the reading
// ended at too, so a short link that redirects to a blocked store is caught
// with no fetch.
func (s *Service) Match(ctx context.Context, rawURL string) (store string, blocked bool, err error) {
	norm, nerr := extract.NormalizeURL(rawURL)
	if nerr != nil {
		return "", false, nil
	}
	if store, blocked, err = s.matchHost(ctx, norm); err != nil || blocked {
		return store, blocked, err
	}
	final, err := s.cachedFinal(ctx, norm)
	if err != nil || final == "" || final == norm {
		return "", false, err
	}
	if final, nerr = extract.NormalizeURL(final); nerr != nil {
		return "", false, nil
	}
	return s.matchHost(ctx, final)
}

// matchHost is [Service.Match] on the host of a normalized link alone.
func (s *Service) matchHost(ctx context.Context, norm string) (store string, blocked bool, err error) {
	chain := hostChain(extract.StoreFromURL(norm))
	if len(chain) == 0 {
		return "", false, nil
	}
	query, args, err := sqlx.In(`SELECT store FROM blocked_stores WHERE store IN (?) ORDER BY length(store) DESC LIMIT 1`, chain)
	if err != nil {
		return "", false, err
	}
	err = s.db.GetContext(ctx, &store, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("match blocked store: %w", err)
	}
	return store, true, nil
}

// cachedFinal returns the address a cached reading of the normalized link
// ended at, after redirects, or "" when there is no reading. The extraction
// pipeline keys its cache by the normalized link and stores its result as
// JSON with that address in "URL". An expired reading still counts: it is
// only a hint, and it costs nothing.
func (s *Service) cachedFinal(ctx context.Context, norm string) (string, error) {
	var final sql.NullString
	err := s.db.GetContext(ctx, &final, `SELECT CASE WHEN json_valid(result) THEN json_extract(result, '$.URL') END FROM extraction_cache WHERE url = ?`, norm)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read cached reading: %w", err)
	}
	return final.String, nil
}

// Get loads a blocked store. It returns [ErrNotFound] when there is none.
func (s *Service) Get(ctx context.Context, id int64) (Store, error) {
	return get(ctx, s.db, id)
}

func get(ctx context.Context, q sqlx.QueryerContext, id int64) (Store, error) {
	var b Store
	err := sqlx.GetContext(ctx, q, &b, `SELECT id, store, created_at FROM blocked_stores WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Store{}, ErrNotFound
	}
	if err != nil {
		return Store{}, fmt.Errorf("get blocked store: %w", err)
	}
	return b, nil
}

// List returns one page of the blocked stores, and how many there are in all.
func (s *Service) List(ctx context.Context, req page.Request) (stores []Store, total int, next *string, err error) {
	if req.Key != "store" {
		return nil, 0, nil, fmt.Errorf("list blocked stores: no sort key %q", req.Key)
	}
	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM blocked_stores`); err != nil {
		return nil, 0, nil, fmt.Errorf("count blocked stores: %w", err)
	}
	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query := `SELECT id, store, created_at FROM blocked_stores`
	var args []any
	if req.After != nil {
		query += ` WHERE (store ` + cmp + ` ? OR (store = ? AND id ` + cmp + ` ?))`
		args = append(args, req.After.Value, req.After.Value, req.After.ID)
	}
	query += ` ORDER BY store ` + dir + `, id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []Store
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list blocked stores: %w", err)
	}
	stores, next = page.Slice(req, rows, func(b Store) page.Position {
		return page.Position{Value: b.Store, ID: b.ID}
	})
	return stores, total, next, nil
}

// Add blocks the store that input names. It returns [ErrInvalid] when the input
// names no store, and [ErrAlreadyBlocked] when the store is blocked.
func (s *Service) Add(ctx context.Context, input string) (Store, error) {
	store, err := Normalize(input)
	if err != nil {
		return Store{}, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO blocked_stores (store, created_at) VALUES (?, ?) ON CONFLICT (store) DO NOTHING`, store, s.now())
	if err != nil {
		return Store{}, fmt.Errorf("block store: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return Store{}, err
	} else if n == 0 {
		return Store{}, ErrAlreadyBlocked
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Store{}, err
	}
	return s.Get(ctx, id)
}

// Guard runs inside the transaction of a delete, before it, with the store as
// it is. An error from it stops the delete (an If-Match check goes there).
type Guard func(ctx context.Context, current Store) error

// Remove takes a store off the list. It returns [ErrNotFound] when there is no
// such store. guard, if there is one, runs in the same transaction.
func (s *Service) Remove(ctx context.Context, id int64, guard Guard) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := get(ctx, tx, id)
	if err != nil {
		return err
	}
	if guard != nil {
		if err := guard(ctx, current); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM blocked_stores WHERE id = ?`, id); err != nil {
		return fmt.Errorf("allow store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
