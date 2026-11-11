package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"
)

// cache stores results by normalized URL in the extraction_cache table. A
// search or a seller lookup shares the table and its age limit, under a key
// with a prefix (see search.go). A normalized URL always starts with its
// scheme, so the two can't collide.
type cache struct {
	db  *sqlx.DB
	now func() time.Time
}

func (c *cache) get(ctx context.Context, key string) (Result, bool, error) {
	var res Result
	ok, err := c.read(ctx, key, &res)
	return res, ok, err
}

// put stores res and deletes expired rows.
func (c *cache) put(ctx context.Context, key string, res Result) error {
	res.Cached = false
	return c.write(ctx, key, res)
}

// read decodes the row for key into out, if a fresh one exists.
func (c *cache) read(ctx context.Context, key string, out any) (bool, error) {
	var raw string
	err := c.db.GetContext(ctx, &raw,
		`SELECT result FROM extraction_cache WHERE url = ? AND created_at > ?`, key, c.now().UTC().Add(-CacheTTL))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return false, err
	}
	return true, nil
}

// write stores value and deletes expired rows.
func (c *cache) write(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	tx, err := c.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM extraction_cache WHERE created_at <= ?`, now.Add(-CacheTTL)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO extraction_cache (url, result, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (url) DO UPDATE SET result = excluded.result, created_at = excluded.created_at`,
		key, string(raw), now); err != nil {
		return err
	}
	return tx.Commit()
}
