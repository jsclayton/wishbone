package loginlink

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
//
// Only A to Z count as upper case. Unicode case folding would take a letter
// from outside the alphabet as one inside it, such as the Kelvin sign (U+212A)
// as k, so it works byte by byte, and any byte that isn't ASCII refuses the
// token.
func lastingCanonical(token string) (canonical string, ok bool) {
	c := []byte(strings.ReplaceAll(token, "-", ""))
	if len(c) != lastingLength {
		return "", false
	}
	for i, b := range c {
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
			c[i] = b
		}
		if strings.IndexByte(LastingAlphabet, b) < 0 {
			return "", false
		}
	}
	return string(c), true
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
	// PredatesProvenance is true for a link made before passkeys and login
	// links recorded the lasting link whose session made them. What its
	// sessions made before then records nothing, so a revoke can't find it.
	PredatesProvenance bool `db:"predates_provenance"`
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
		`SELECT id, user_id, created_at, last_used_at, predates_provenance FROM lasting_links WHERE revoked_at IS NULL ORDER BY id`); err != nil {
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

// Removed is what a revoke removed from one user: the passkeys and login links
// that the link's sessions made for them, and their sessions that record the
// link.
type Removed struct {
	UserID int64
	// SessionsEnded is how many of the user's live sessions it ended.
	SessionsEnded int
	// PasskeysDeleted are the names of the passkeys it deleted, by ID.
	PasskeysDeleted []string
	// LoginLinksEnded is how many unused login links for the user it ended.
	LoginLinksEnded int
	// PasskeysLeft is how many passkeys the user has after the revoke.
	PasskeysLeft int
}

type deletedPasskey struct {
	ID     int64  `db:"id"`
	UserID int64  `db:"user_id"`
	Name   string `db:"name"`
}

// Revoked is the outcome of [Service.RevokeLasting].
type Revoked struct {
	Link Lasting
	// Users is what the revoke removed, for each user it removed something
	// from, by user ID.
	Users []Removed
}

// RevokeLasting revokes a lasting link, and in the same commit removes what it
// left that still signs someone in (docs/spec.md, Access, Lasting login links):
// it ends every live session that records the link, deletes every passkey that
// records it, and ends every unused login link that records it. A session, a
// passkey or a login link records the link when one of the link's sessions
// made it, at any distance, for any user. A link that leaked stops working at
// once, and leaves nothing behind. Everything else stays. It returns
// [ErrNotFound] when there is no such link that isn't revoked.
func (s *Service) RevokeLasting(ctx context.Context, id int64) (Revoked, error) {
	now := s.now()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Revoked{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var out Revoked
	err = tx.GetContext(ctx, &out.Link,
		`UPDATE lasting_links SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL
		 RETURNING id, user_id, created_at, last_used_at, predates_provenance`, now, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Revoked{}, ErrNotFound
	}
	if err != nil {
		return Revoked{}, fmt.Errorf("revoke lasting login link: %w", err)
	}
	out.Link.CreatedAt = out.Link.CreatedAt.UTC()
	if at := out.Link.LastUsedAt; at != nil {
		u := at.UTC()
		out.Link.LastUsedAt = &u
	}

	byUser := map[int64]*Removed{}
	of := func(userID int64) *Removed {
		if byUser[userID] == nil {
			byUser[userID] = &Removed{UserID: userID}
		}
		return byUser[userID]
	}
	ended, err := session.RevokeStartedBy(ctx, tx, now, id)
	if err != nil {
		return Revoked{}, err
	}
	for userID, n := range ended {
		of(userID).SessionsEnded = n
	}
	var passkeys []deletedPasskey
	if err := tx.SelectContext(ctx, &passkeys,
		`DELETE FROM passkeys WHERE lasting_link_id = ? RETURNING id, user_id, name`, id); err != nil {
		return Revoked{}, fmt.Errorf("delete the passkeys of a lasting link: %w", err)
	}
	// RETURNING gives the rows in no set order.
	slices.SortFunc(passkeys, func(a, b deletedPasskey) int { return cmp.Compare(a.ID, b.ID) })
	for _, p := range passkeys {
		r := of(p.UserID)
		r.PasskeysDeleted = append(r.PasskeysDeleted, p.Name)
	}
	// An unused login link ends as an expired one does, so it stays a row that
	// says when it was made, and a redeemed one keeps its history.
	var links []int64
	if err := tx.SelectContext(ctx, &links,
		`UPDATE login_links SET expires_at = ?
		 WHERE lasting_link_id = ? AND redeemed_at IS NULL AND expires_at > ?
		 RETURNING user_id`, now, id, now); err != nil {
		return Revoked{}, fmt.Errorf("end the login links of a lasting link: %w", err)
	}
	for _, userID := range links {
		of(userID).LoginLinksEnded++
	}

	for _, r := range byUser {
		if err := tx.GetContext(ctx, &r.PasskeysLeft, `SELECT count(*) FROM passkeys WHERE user_id = ?`, r.UserID); err != nil {
			return Revoked{}, fmt.Errorf("count passkeys: %w", err)
		}
		out.Users = append(out.Users, *r)
	}
	slices.SortFunc(out.Users, func(a, b Removed) int { return cmp.Compare(a.UserID, b.UserID) })
	if err := tx.Commit(); err != nil {
		return Revoked{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}
