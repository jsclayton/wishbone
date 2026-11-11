// Package session owns the sessions table: a signed-in browser or app. A
// session is a random token that the caller keeps (in the cookie, or as a
// bearer token) and that the database knows only by its hash. See the layout
// notes in package user.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/page"
)

const (
	// Lifetime is how long a session lasts after its last use. Each use moves
	// the end forward by this much.
	Lifetime = 365 * 24 * time.Hour

	// CookieRenewInterval is the least time between two renewals of a session's
	// cookie. A renewal that arrives late can overwrite the cookie of a newer
	// session, so renewals are rare.
	CookieRenewInterval = 24 * time.Hour
)

// ErrNone means the token is missing, unknown, expired, or revoked.
var ErrNone = errors.New("no valid session")

// Session is a live session.
type Session struct {
	ID        int64
	UserID    int64
	ExpiresAt time.Time
}

// NewToken returns a new random token and its hash, for a session or a login
// link.
func NewToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("random token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, Hash(token), nil
}

// Hash is what the database keeps of a token (or of any secret).
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create starts a session for a user and returns the raw token, which exists
// nowhere else. It takes a transaction or a database, so a caller can start a
// session in the same commit as the change that earned it.
func Create(ctx context.Context, q sqlx.ExtContext, now time.Time, userID int64, userAgent string) (token string, s Session, err error) {
	token, hash, err := NewToken()
	if err != nil {
		return "", Session{}, err
	}
	expires := now.Add(Lifetime)
	res, err := q.ExecContext(ctx,
		`INSERT INTO sessions (user_id, token_hash, user_agent, created_at, last_used_at, expires_at, cookie_sent_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, hash, userAgent, now, now, expires, now)
	if err != nil {
		return "", Session{}, fmt.Errorf("insert session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", Session{}, fmt.Errorf("insert session: %w", err)
	}
	return token, Session{ID: id, UserID: userID, ExpiresAt: expires}, nil
}

// Service finds and ends sessions.
type Service struct {
	db  *sqlx.DB
	now func() time.Time
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// Authenticate finds the live session behind a token and moves its end forward.
// It returns [ErrNone] for a token that is empty or not live.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNone
	}
	now := s.now()
	out := Session{ExpiresAt: now.Add(Lifetime)}
	err := s.db.QueryRowxContext(ctx,
		`UPDATE sessions SET last_used_at = ?, expires_at = ?
		 WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
		 RETURNING id, user_id`, now, out.ExpiresAt, Hash(token), now).Scan(&out.ID, &out.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNone
	}
	if err != nil {
		return Session{}, fmt.Errorf("touch session: %w", err)
	}
	return out, nil
}

// ErrUserDeactivated means the user is deactivated, so they can't sign in.
var ErrUserDeactivated = errors.New("the user is deactivated")

// SignIn starts a session for a user who has just proved who they are, for
// example with a passkey. replaceID is the caller's current session to revoke in
// the same commit, or 0 for none. It returns the raw token, the session, and
// whether a live session was revoked. It returns [ErrUserDeactivated], and starts
// nothing, for a user who is deactivated: a deactivation that commits between the
// proof and the session wins.
func (s *Service) SignIn(ctx context.Context, userID, replaceID int64, userAgent string) (token string, sess Session, replaced bool, err error) {
	now := s.now()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", Session{}, false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var active bool
	if err := tx.GetContext(ctx, &active, `SELECT deactivated_at IS NULL FROM users WHERE id = ?`, userID); err != nil {
		return "", Session{}, false, fmt.Errorf("find user: %w", err)
	}
	if !active {
		return "", Session{}, false, ErrUserDeactivated
	}
	if replaceID != 0 {
		if _, replaced, err = RevokeLive(ctx, tx, now, replaceID); err != nil {
			return "", Session{}, false, err
		}
	}
	token, sess, err = Create(ctx, tx, now, userID, userAgent)
	if err != nil {
		return "", Session{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return "", Session{}, false, fmt.Errorf("commit: %w", err)
	}
	return token, sess, replaced, nil
}

// ClaimCookieRenewal reports whether the caller won the right to send the
// session's cookie again. A conditional UPDATE is atomic, so of two requests at
// once only one wins. It wins at most once per [CookieRenewInterval].
func (s *Service) ClaimCookieRenewal(ctx context.Context, id int64) (bool, error) {
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET cookie_sent_at = ?
		 WHERE id = ? AND cookie_sent_at <= ? AND revoked_at IS NULL AND expires_at > ?`,
		now, id, now.Add(-CookieRenewInterval), now)
	if err != nil {
		return false, fmt.Errorf("claim cookie renewal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim cookie renewal: %w", err)
	}
	return n > 0, nil
}

// RevokeLive revokes a live session by its ID, in a transaction or on a
// database, and returns the user it belonged to. ok is false when the session
// is not live: unknown, revoked, or expired.
func RevokeLive(ctx context.Context, q sqlx.ExtContext, now time.Time, id int64) (userID int64, ok bool, err error) {
	err = sqlx.GetContext(ctx, q, &userID,
		`UPDATE sessions SET revoked_at = ?
		 WHERE id = ? AND revoked_at IS NULL AND expires_at > ?
		 RETURNING user_id`, now, id, now)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("revoke session: %w", err)
	}
	return userID, true, nil
}

// RevokeAll revokes every live session of a user, in a transaction or on a
// database. A user with none is done.
func RevokeAll(ctx context.Context, q sqlx.ExtContext, now time.Time, userID int64) error {
	if _, err := q.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?`,
		now, userID, now); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

// Info is a live session as the sessions list shows it.
type Info struct {
	ID         int64
	UserAgent  string
	LastUsedAt time.Time
	CreatedAt  time.Time
}

type infoRow struct {
	ID         int64     `db:"id"`
	UserAgent  string    `db:"user_agent"`
	LastUsedAt time.Time `db:"last_used_at"`
	CreatedAt  time.Time `db:"created_at"`
}

// SortKeys are the sort keys of a user's sessions (docs/routes.md,
// Sessions). A session list reads best from the most recent use, so
// `last_used_at` is newest first and `-last_used_at` is oldest first.
var SortKeys = []page.Key{{Name: "last_used_at", Descending: true, Time: true}}

// Page returns one page of a user's live sessions, and how many live sessions
// the user has. It returns the next cursor, or nil on the last page.
func (s *Service) Page(ctx context.Context, userID int64, req page.Request) (infos []Info, total int, next *string, err error) {
	now := s.now()
	const live = `user_id = ? AND revoked_at IS NULL AND expires_at > ?`
	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM sessions WHERE `+live, userID, now); err != nil {
		return nil, 0, nil, fmt.Errorf("count sessions: %w", err)
	}

	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query := `SELECT id, user_agent, last_used_at, created_at FROM sessions WHERE ` + live
	args := []any{userID, now}
	if req.After != nil {
		at, err := time.Parse(time.RFC3339Nano, req.After.Value)
		if err != nil {
			return nil, 0, nil, page.ErrBadCursor
		}
		query += ` AND (last_used_at ` + cmp + ` ? OR (last_used_at = ? AND id ` + cmp + ` ?))`
		args = append(args, at, at, req.After.ID)
	}
	query += ` ORDER BY last_used_at ` + dir + `, id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []infoRow
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list sessions: %w", err)
	}
	all := make([]Info, len(rows))
	for i, r := range rows {
		all[i] = Info(r)
	}
	infos, next = page.Slice(req, all, func(i Info) page.Position {
		return page.Position{Value: page.FormatTime(i.LastUsedAt), ID: i.ID}
	})
	return infos, total, next, nil
}

// Revoke ends one of a user's sessions. It returns [ErrNone] when the session
// is not a live session of that user. guard, if there is one, sees the session
// as it is, inside the same transaction as the revoke: an error from it stops
// the revoke. An If-Match check goes there, and so does the check that the
// caller may still act for the user. q is the transaction: the guard reads
// other rows through it.
func (s *Service) Revoke(ctx context.Context, userID, sessionID int64, guard func(ctx context.Context, q sqlx.QueryerContext, current Info) error) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := s.now()
	var r infoRow
	err = sqlx.GetContext(ctx, tx, &r,
		`SELECT id, user_agent, last_used_at, created_at FROM sessions
		 WHERE id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ?`, sessionID, userID, now)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNone
	}
	if err != nil {
		return fmt.Errorf("find session: %w", err)
	}
	if guard != nil {
		if err := guard(ctx, tx, Info(r)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE id = ?`, now, sessionID); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
