// Package idempotency owns the idempotency_keys table: the first answer to a
// write that came with an Idempotency-Key header, kept so that a repeat of the
// write gets that answer and does no work again (docs/spec.md, API principles,
// Writes, Retries).
//
// A key belongs to its session. The table's foreign key and its trigger remove
// a session's rows when the session is deleted or revoked, so no code that ends
// a session has to know about this package.
//
// Two things stop an answer from being kept after its session, or what it
// shows, is gone. A session is its ID and its token ([Session]), so the ID
// alone never names a session. Until the migration 20261121013109 the database
// gave the ID of a deleted session to a later one, and a database that is
// restored from an older snapshot still can. And the kept answers have an
// epoch, which the delete of a user raises ([ForgetAll]): a request that began
// in an older epoch keeps nothing.
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

// Session says which session a key belongs to. The ID alone is not enough: a
// later session can have the ID of a session that is gone (see the package
// comment), and the token is what the two never share.
type Session struct {
	ID int64
	// TokenHash is what the database keeps of the session's token
	// (sessions.token_hash).
	TokenHash string
}

// epochKey is the row of instance_meta that holds the epoch of the kept
// answers: a count of how many times every answer was cleared ([ForgetAll]). A
// database with no such row is in epoch 0.
const epochKey = "idempotency_epoch"

// epochSQL reads the epoch, as text.
const epochSQL = `COALESCE((SELECT value FROM instance_meta WHERE key = '` + epochKey + `'), '0')`

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
	session Session
	key     string
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
	// epoch is the epoch of the kept answers when the run began.
	epoch string
}

// Begin starts a request that came with a key. request is a hash of what the
// request asks for: a repeat has the same one.
//
// For the first request with the key, it returns a [Run] and no answer: the
// caller does the work and ends the run. For a repeat inside the [Window], it
// returns the first answer, and the caller does no work. It returns [ErrInUse]
// when the first request is still running, and [ErrReused] when the key came
// before, in the window, with another request.
//
// Only the session's own requests and answers count, by its ID and its token.
// A session that is gone, and whose ID a later session has, gets a [Run] as
// for a first request, and [Run.Keep] keeps nothing for it.
func (s *Service) Begin(ctx context.Context, session Session, key, request string) (*Run, *Answer, error) {
	k := runKey{session: session, key: key}
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

	// The epoch is read before the route does any work, so the run knows of
	// every clear that comes after this point.
	var epoch string
	if err := s.db.GetContext(ctx, &epoch, `SELECT `+epochSQL); err != nil {
		s.release(k)
		return nil, nil, fmt.Errorf("read the epoch of the idempotency keys: %w", err)
	}
	now := s.now()
	var row struct {
		Request string `db:"request_hash"`
		Status  int    `db:"status"`
		Headers string `db:"headers"`
		Body    []byte `db:"body"`
	}
	// The row is the caller's only when the session that has the ID now has the
	// caller's token. A caller that was let in before its session was deleted
	// can hold the ID of a later session. It finds no answer and no reuse here:
	// it gets a Run, and Keep keeps nothing for it.
	err := s.db.GetContext(ctx, &row,
		`SELECT k.request_hash, k.status, k.headers, k.body
		 FROM idempotency_keys k JOIN sessions s ON s.id = k.session_id
		 WHERE k.session_id = ? AND s.token_hash = ? AND k.key = ? AND k.created_at > ?`,
		session.ID, session.TokenHash, key, now.Add(-Window))
	if errors.Is(err, sql.ErrNoRows) {
		return &Run{s: s, key: k, request: request, first: now, epoch: epoch}, nil, nil
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
// request is over. It keeps nothing in two cases, and both are checked in the
// statement that inserts the row, so nothing can commit between the check and
// the insert:
//
//   - The run's session is no longer live: its keys went with it. A later
//     session that has its ID is another session, with another token.
//   - Every answer was cleared after the run began ([ForgetAll]), in this
//     process or in another: the answer was built before the clear.
//
// Keep also deletes the answers whose window is over, as the sweep does (see
// [Service.Sweep]).
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
		 SELECT id, ?, ?, ?, ?, ?, ? FROM sessions
		 WHERE id = ? AND token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND `+epochSQL+` = ?`,
		r.key.key, r.request, r.first, a.Status, string(headers), body,
		r.key.session.ID, r.key.session.TokenHash, now, r.epoch); err != nil {
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

// ForgetAll deletes every kept answer, of every session, in a transaction or on
// a database, and raises the epoch of the kept answers. The delete of a user
// calls it in its transaction: a kept answer is a copy of what its session saw,
// and that can be something of the user. A repeat of a write from before then
// does the work again.
//
// The epoch is what clears the requests that are running, which are in no
// table: a run that began before the clear keeps nothing ([Run.Keep]). It works
// from another process too, such as `wishbone delete-user` while the server
// runs, because it is in the database.
func ForgetAll(ctx context.Context, q sqlx.ExecerContext) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM idempotency_keys`); err != nil {
		return fmt.Errorf("delete every idempotency key: %w", err)
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO instance_meta (key, value) VALUES ('`+epochKey+`', '1')
		 ON CONFLICT (key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT), updated_at = CURRENT_TIMESTAMP`); err != nil {
		return fmt.Errorf("raise the epoch of the idempotency keys: %w", err)
	}
	return nil
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
