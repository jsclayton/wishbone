// Package setup lets the operator create the first user, the admin, with a
// one-time setup code from the server log (docs/spec.md, Admin and setup).
// See the layout notes in package user.
package setup

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/user"
)

var (
	// ErrComplete means the instance has a user, so setup is over.
	ErrComplete = errors.New("setup is complete")
	// ErrCodeInvalid means the setup code is not the one in the log.
	ErrCodeInvalid = errors.New("the setup code is not valid")
)

// alphabet leaves out the characters that look alike: 0 and O, 1 and I and L.
const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// codeGroups and groupLength make a code like ABCD-EFGH-JKMN: 12 characters,
// about 59 bits.
const (
	codeGroups  = 3
	groupLength = 4
)

// Service runs setup.
type Service struct {
	db  *sqlx.DB
	now func() time.Time
}

// NewService returns a Service. now is the clock, so tests control time.
func NewService(db *sqlx.DB, now func() time.Time) *Service {
	return &Service{db: db, now: func() time.Time { return now().UTC() }}
}

// IsNeeded reports whether the instance has no users.
func (s *Service) IsNeeded(ctx context.Context) (bool, error) {
	n, err := user.Count(ctx, s.db)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// Prepare runs at server start. While the instance has no users, it makes a new
// setup code, stores its hash in place of any older code, and returns it for the
// log. With users, it removes any code and returns "".
func (s *Service) Prepare(ctx context.Context) (string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM setup_codes`); err != nil {
		return "", fmt.Errorf("remove setup codes: %w", err)
	}
	n, err := user.Count(ctx, tx)
	if err != nil {
		return "", err
	}
	code := ""
	if n == 0 {
		if code, err = newCode(); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO setup_codes (code_hash, created_at) VALUES (?, ?)`, session.Hash(normalize(code)), s.now()); err != nil {
			return "", fmt.Errorf("store setup code: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return code, nil
}

func newCode() (string, error) {
	b := make([]byte, codeGroups*groupLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random code: %w", err)
	}
	var out strings.Builder
	for i, v := range b {
		if i > 0 && i%groupLength == 0 {
			out.WriteByte('-')
		}
		// 256 is not a multiple of 31, so this leans a little to the first
		// letters. That costs under a bit of the code's strength.
		out.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return out.String(), nil
}

// normalize reads a code as the operator typed it: upper case, with no dashes
// or spaces.
func normalize(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, strings.ToUpper(code))
}

// Input is what the first user enters.
type Input struct {
	Name     string
	LastName string
	Birthday user.PartialDate
}

// Result is the outcome of [Service.Complete].
type Result struct {
	User user.User
	// Token is the raw token of the session that setup started.
	Token   string
	Session session.Session
}

// Complete creates the first user as the admin and starts a session for them,
// in one transaction. It returns [ErrComplete] when the instance has a user,
// [user.ErrInvalid] when the input is not valid, and [ErrCodeInvalid] when the
// code is wrong. A failure creates nothing and keeps the code, so a wrong guess
// does not use it up.
func (s *Service) Complete(ctx context.Context, code string, in Input, userAgent string) (Result, error) {
	now := s.now()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	n, err := user.Count(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if n > 0 {
		return Result{}, ErrComplete
	}
	admin, err := user.NewUser{Name: in.Name, LastName: in.LastName, Birthday: in.Birthday, IsAdmin: true}.Validate(now)
	if err != nil {
		return Result{}, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM setup_codes WHERE code_hash = ?`, session.Hash(normalize(code)))
	if err != nil {
		return Result{}, fmt.Errorf("use setup code: %w", err)
	}
	if used, _ := res.RowsAffected(); used == 0 {
		return Result{}, ErrCodeInvalid
	}

	id, err := user.Create(ctx, tx, now, admin)
	if err != nil {
		return Result{}, err
	}
	token, sess, err := session.Create(ctx, tx, now, id, userAgent)
	if err != nil {
		return Result{}, err
	}
	u, err := user.Get(ctx, tx, id)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{User: u, Token: token, Session: sess}, nil
}
