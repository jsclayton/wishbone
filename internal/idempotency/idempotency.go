// Package idempotency owns the idempotency_keys table: the first answer to a
// write that came with an Idempotency-Key header, kept so that a repeat of the
// write gets that answer and does no work again (docs/spec.md, API principles,
// Writes, Retries).
//
// A key belongs to its session. The table's foreign key and its trigger remove
// a session's rows when the session is deleted or revoked, so no code that ends
// a session has to know about this package.
package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

const (
	// Window is how long a key is remembered, from its first request.
	Window = 24 * time.Hour

	// MaxKeyLength is the longest key, in bytes.
	MaxKeyLength = 255

	// SweepEvery is how often the server runs the sweep.
	SweepEvery = time.Hour
)

var (
	// ErrInUse means the first request with the key is still running.
	ErrInUse = errors.New("the first request with this idempotency key is still running")

	// ErrReused means the key came before with another request.
	ErrReused = errors.New("this idempotency key was used for another request")
)

// Answer is the answer to a write, in the parts that a repeat gets again.
type Answer struct {
	Status int
	// Header holds only the headers that are kept. The caller chooses them.
	Header http.Header
	Body   []byte
}

// Service remembers the answers. One Service serves the whole process: it keeps
// the requests that are running in memory, so a restart forgets them, and a
// request that the restart cut off does not block its key.
type Service struct {
	db  *sqlx.DB
	now func() time.Time

	mu      sync.Mutex
	running map[runKey]string // the request hash of each key that is running
}

type runKey struct {
	sessionID int64
	key       string
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }, running: map[runKey]string{}}
}

// Run is the first request with a key, while it runs. The caller ends it with
// [Run.End], exactly once. Before that, it can keep the answer with [Run.Keep].
type Run struct {
	s       *Service
	key     runKey
	request string
	first   time.Time
}

// Begin starts a request that came with a key. request is a hash of what the
// request asks for: a repeat has the same one.
//
// For the first request with the key, it returns a [Run] and no answer: the
// caller does the work and ends the run. For a repeat inside the [Window], it
// returns the first answer, and the caller does no work. It returns [ErrInUse]
// when the first request is still running, and [ErrReused] when the key came
// before, in the window, with another request.
func (s *Service) Begin(ctx context.Context, sessionID int64, key, request string) (*Run, *Answer, error) {
	k := runKey{sessionID: sessionID, key: key}
	s.mu.Lock()
	running, ok := s.running[k]
	if !ok {
		s.running[k] = request
	}
	s.mu.Unlock()
	switch {
	case ok && running != request:
		return nil, nil, ErrReused
	case ok:
		return nil, nil, ErrInUse
	}

	now := s.now()
	var row struct {
		Request string `db:"request_hash"`
		Status  int    `db:"status"`
		Headers string `db:"headers"`
		Body    []byte `db:"body"`
	}
	err := s.db.GetContext(ctx, &row,
		`SELECT request_hash, status, headers, body FROM idempotency_keys
		 WHERE session_id = ? AND key = ? AND created_at > ?`, sessionID, key, now.Add(-Window))
	if errors.Is(err, sql.ErrNoRows) {
		return &Run{s: s, key: k, request: request, first: now}, nil, nil
	}
	s.release(k)
	if err != nil {
		return nil, nil, fmt.Errorf("find idempotency key: %w", err)
	}
	if row.Request != request {
		return nil, nil, ErrReused
	}
	answer := &Answer{Status: row.Status, Body: row.Body}
	if err := json.Unmarshal([]byte(row.Headers), &answer.Header); err != nil {
		return nil, nil, fmt.Errorf("read the headers of an idempotency key: %w", err)
	}
	return nil, answer, nil
}

func (s *Service) release(k runKey) {
	s.mu.Lock()
	delete(s.running, k)
	s.mu.Unlock()
}

// Keep remembers the answer of the run, until the [Window] after the first
// request is over. A session that is no longer live keeps nothing: its keys
// went with it. Keep also deletes the answers whose window is over, as the
// sweep does (see [Service.Sweep]).
func (r *Run) Keep(ctx context.Context, a Answer) error {
	headers, err := json.Marshal(a.Header)
	if err != nil {
		return fmt.Errorf("write the headers of an idempotency key: %w", err)
	}
	tx, err := r.s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := r.s.now()
	if _, err := deleteOld(ctx, tx, now); err != nil {
		return err
	}
	// The body is a BLOB, also when it is empty: a nil slice would be a NULL.
	body := a.Body
	if body == nil {
		body = []byte{}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO idempotency_keys (session_id, key, request_hash, created_at, status, headers, body)
		 SELECT id, ?, ?, ?, ?, ?, ? FROM sessions WHERE id = ? AND revoked_at IS NULL AND expires_at > ?`,
		r.key.key, r.request, r.first, a.Status, string(headers), body, r.key.sessionID, now); err != nil {
		return fmt.Errorf("insert idempotency key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// End ends the run. If the run kept no answer, the next request with the key
// is a first request again.
func (r *Run) End() {
	r.s.release(r.key)
}

// deleteOld deletes the answers whose window is over at now, and returns how
// many they were. It is the one statement that says which answers are old.
// [Service.Begin] never returns one of them, deleted or not.
func deleteOld(ctx context.Context, q sqlx.ExecerContext, now time.Time) (int, error) {
	res, err := q.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at <= ?`, now.Add(-Window))
	if err != nil {
		return 0, fmt.Errorf("delete old idempotency keys: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete old idempotency keys: %w", err)
	}
	return int(n), nil
}

// Sweep deletes every answer whose [Window] is over, and returns how many it
// deleted. A forgotten key is then gone from the database too, and from the
// snapshots after it, also when no client sends a key again.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	return deleteOld(ctx, s.db, s.now())
}

// Schedule runs [Service.Sweep] now and then every [SweepEvery] until ctx ends.
// The first run, at start, deletes what got old while the server was down. A
// failed run is logged and tried again on the next tick. logger may be nil.
func (s *Service) Schedule(ctx context.Context, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	tick := time.NewTicker(SweepEvery)
	defer tick.Stop()
	for {
		switch n, err := s.Sweep(ctx); {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Error("idempotency key sweep failed", slog.Any("error", err))
		case n > 0:
			logger.Debug("deleted old idempotency keys", slog.Int("count", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
