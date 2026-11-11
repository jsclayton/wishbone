// Package loginlink owns the login_links table: a link that signs a user in
// once (docs/spec.md, API principles, Sign-in). A link is a random token in
// a URL, and the database knows it only by its hash. See the layout notes in
// package user.
package loginlink

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/user"
)

// Lifetime is how long a login link works.
const Lifetime = 7 * 24 * time.Hour

// ErrInvalid means the link is unknown, used, or expired. The three look the
// same, so a caller learns nothing about why.
var ErrInvalid = errors.New("invalid or expired login link")

// Service makes, checks, and redeems login links.
type Service struct {
	db  *sqlx.DB
	now func() time.Time
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// Create makes a link for a user and returns the raw token, which exists
// nowhere else, and when the link expires. It does not check that the user
// exists: the database refuses a link for no user.
func (s *Service) Create(ctx context.Context, userID int64) (token string, expiresAt time.Time, err error) {
	token, hash, err := session.NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	expiresAt = now.Add(Lifetime)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO login_links (user_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		userID, hash, now, expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("insert login link: %w", err)
	}
	return token, expiresAt, nil
}

// usable is the one definition of a link that still works, for Check and
// Redeem alike. It takes one argument, the current time. A link of a deactivated
// user doesn't work, and a refused redeem doesn't use it up: when the admin
// reactivates the user, a link that hasn't expired works again.
const usable = `redeemed_at IS NULL AND expires_at > ? AND user_id IN (SELECT id FROM users WHERE deactivated_at IS NULL)`

// Check is what a login link's page may know before the link is redeemed. It
// must never hold anything that names the link's user.
type Check struct {
	ExpiresAt time.Time
	// ForCurrentUser is true when the link is for the user of the caller's
	// session.
	ForCurrentUser bool
}

// Check reports on a link without changing anything: the link stays unused.
// currentUserID is the user of the caller's session, or 0 when there is none. It
// returns [ErrInvalid] for every link that [Service.Redeem] would refuse.
func (s *Service) Check(ctx context.Context, token string, currentUserID int64) (Check, error) {
	var userID int64
	var c Check
	err := s.db.QueryRowxContext(ctx,
		`SELECT user_id, expires_at FROM login_links WHERE token_hash = ? AND `+usable,
		session.Hash(token), s.now()).Scan(&userID, &c.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Check{}, ErrInvalid
	}
	if err != nil {
		return Check{}, fmt.Errorf("check login link: %w", err)
	}
	c.ExpiresAt = c.ExpiresAt.UTC()
	c.ForCurrentUser = currentUserID != 0 && currentUserID == userID
	return c, nil
}

// Result is the outcome of [Service.Redeem].
type Result struct {
	User user.User
	// Token is the raw token of the new session.
	Token   string
	Session session.Session
	// Replaced is true when the caller's current session was revoked.
	Replaced bool
	// Switched is true when that session was another user's.
	Switched bool
}

// Redeem uses a link once and starts a session for its user. currentSessionID
// is the caller's session to replace, or 0 for none: it is revoked, in the same
// commit. A failed redemption leaves it alone. It returns [ErrInvalid] for a
// link that does not work.
func (s *Service) Redeem(ctx context.Context, token string, currentSessionID int64, userAgent string) (Result, error) {
	now := s.now()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID int64
	err = tx.GetContext(ctx, &userID,
		`UPDATE login_links SET redeemed_at = ? WHERE token_hash = ? AND `+usable+` RETURNING user_id`,
		now, session.Hash(token), now)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, ErrInvalid
	}
	if err != nil {
		return Result{}, fmt.Errorf("redeem login link: %w", err)
	}

	var res Result
	if currentSessionID != 0 {
		previous, ok, err := session.RevokeLive(ctx, tx, now, currentSessionID)
		if err != nil {
			return Result{}, err
		}
		res.Replaced = ok
		res.Switched = ok && previous != userID
	}
	res.Token, res.Session, err = session.Create(ctx, tx, now, userID, userAgent)
	if err != nil {
		return Result{}, err
	}
	if res.User, err = user.Get(ctx, tx, userID); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}
