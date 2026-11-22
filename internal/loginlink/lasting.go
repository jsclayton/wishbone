package loginlink

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"jsclayton.dev/wishbone/internal/session"
)

// A lasting login link works any number of times and doesn't expire: it works
// until the operator revokes it (docs/spec.md, Access, Lasting login links).
// Only the CLI makes one. Its token is short enough to type, so it has an
// alphabet and a form of its own.

const (
	// LastingAlphabet is the 32 characters of a lasting link's token: the
	// digits and the lower-case letters, without 0, 1, l and o, which look like
	// each other or like O and I.
	LastingAlphabet = "23456789abcdefghijkmnpqrstuvwxyz"
	// lastingLength is the length of a token's canonical form. 16 characters of
	// 5 bits each are 80 bits.
	lastingLength = 16
	// lastingGroup is the length of each group in the shown form.
	lastingGroup = 4
)

// newLastingToken returns a new random token in its shown form, four groups of
// four with hyphens (such as k7mq-3xtd-p9wh-r2fn), and the hash of its canonical
// form.
func newLastingToken() (shown, hash string, err error) {
	b := make([]byte, lastingLength)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("random token: %w", err)
	}
	var canonical strings.Builder
	for _, c := range b {
		// 256 is a multiple of 32, so each character is as likely as any other.
		canonical.WriteByte(LastingAlphabet[c%byte(len(LastingAlphabet))])
	}
	c := canonical.String()
	return showLasting(c), session.Hash(c), nil
}

// showLasting puts the hyphens into a canonical token.
func showLasting(canonical string) string {
	groups := make([]string, 0, lastingLength/lastingGroup)
	for i := 0; i < len(canonical); i += lastingGroup {
		groups = append(groups, canonical[i:i+lastingGroup])
	}
	return strings.Join(groups, "-")
}

// lastingCanonical returns the canonical form of a token that has the shape of
// a lasting link's: 16 characters of [LastingAlphabet] in any case, with or
// without hyphens. The hyphens are only shown, so it drops them, wherever they
// are. ok is false for any other token, such as an ordinary login link's, which
// is 43 characters of base64url.
func lastingCanonical(token string) (canonical string, ok bool) {
	c := strings.ToLower(strings.ReplaceAll(token, "-", ""))
	if len(c) != lastingLength {
		return "", false
	}
	for i := range len(c) {
		if strings.IndexByte(LastingAlphabet, c[i]) < 0 {
			return "", false
		}
	}
	return c, true
}

// usableLasting is the one definition of a lasting link that works, for Check
// and Redeem alike. It works until it is revoked, while its user is active. A
// refused redeem changes nothing, so when the admin reactivates the user, the
// link works again.
const usableLasting = `revoked_at IS NULL AND user_id IN (SELECT id FROM users WHERE deactivated_at IS NULL)`

// Lasting is a lasting link as the operator lists it. It never holds the token.
type Lasting struct {
	ID         int64      `db:"id"`
	UserID     int64      `db:"user_id"`
	CreatedAt  time.Time  `db:"created_at"`
	LastUsedAt *time.Time `db:"last_used_at"`
}

// ErrNotFound means there is no lasting link with that ID that isn't revoked.
var ErrNotFound = errors.New("no such lasting login link")

// CreateLasting makes a lasting link for a user and returns its token in the
// shown form, which exists nowhere else. It does not check that the user exists
// or is active: the database refuses a link for no user, and a deactivated
// user's link doesn't work until they are reactivated.
func (s *Service) CreateLasting(ctx context.Context, userID int64) (token string, link Lasting, err error) {
	token, hash, err := newLastingToken()
	if err != nil {
		return "", Lasting{}, err
	}
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO lasting_links (user_id, token_hash, created_at) VALUES (?, ?, ?)`, userID, hash, now)
	if err != nil {
		return "", Lasting{}, fmt.Errorf("insert lasting login link: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", Lasting{}, fmt.Errorf("insert lasting login link: %w", err)
	}
	return token, Lasting{ID: id, UserID: userID, CreatedAt: now}, nil
}

// ListLasting returns every lasting link that isn't revoked, oldest first. It
// lists the links of a deactivated user too: they work again when the user is
// reactivated.
func (s *Service) ListLasting(ctx context.Context) ([]Lasting, error) {
	var links []Lasting
	if err := s.db.SelectContext(ctx, &links,
		`SELECT id, user_id, created_at, last_used_at FROM lasting_links WHERE revoked_at IS NULL ORDER BY id`); err != nil {
		return nil, fmt.Errorf("list lasting login links: %w", err)
	}
	for i := range links {
		links[i].CreatedAt = links[i].CreatedAt.UTC()
		if at := links[i].LastUsedAt; at != nil {
			u := at.UTC()
			links[i].LastUsedAt = &u
		}
	}
	return links, nil
}

// RevokeLasting revokes a lasting link, and ends every session that it
// started, in one commit. A link that leaked stops working at once. The user's
// other sessions stay. It returns the link and how many sessions it ended, or
// [ErrNotFound] when there is no such link that isn't revoked.
func (s *Service) RevokeLasting(ctx context.Context, id int64) (link Lasting, ended int, err error) {
	now := s.now()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Lasting{}, 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.GetContext(ctx, &link,
		`UPDATE lasting_links SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL
		 RETURNING id, user_id, created_at, last_used_at`, now, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Lasting{}, 0, ErrNotFound
	}
	if err != nil {
		return Lasting{}, 0, fmt.Errorf("revoke lasting login link: %w", err)
	}
	if ended, err = session.RevokeStartedBy(ctx, tx, now, id); err != nil {
		return Lasting{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return Lasting{}, 0, fmt.Errorf("commit: %w", err)
	}
	return link, ended, nil
}
