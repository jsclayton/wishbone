// Package passkey owns the passkeys table: the WebAuthn credentials a user
// added so they can sign in without a login link. A passkey grants no powers
// (ADR 0008): admin stays a flag on the user. See the layout notes in package
// user.
package passkey

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jmoiron/sqlx"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"jsclayton.dev/wishbone/internal/page"
	"jsclayton.dev/wishbone/internal/patch"
)

const (
	// ChallengeLifetime is how long a user has to answer a registration or a
	// sign-in.
	ChallengeLifetime = 5 * time.Minute

	// DefaultName is the name of a passkey added with none.
	DefaultName = "Passkey"
	// MaxNameLength is the longest passkey name, in characters.
	MaxNameLength = 100
)

var (
	// ErrNoChallenge means the user has no pending registration: it was never
	// started, already finished, or expired.
	ErrNoChallenge = errors.New("no pending passkey registration")
	// ErrVerification means the browser's answer did not verify.
	ErrVerification = errors.New("passkey registration did not verify")
	// ErrDuplicate means the credential is already registered.
	ErrDuplicate = errors.New("passkey already registered")
	// ErrNotFound means the user has no such passkey.
	ErrNotFound = errors.New("no such passkey")
	// ErrInvalidName means a passkey name is empty or too long.
	ErrInvalidName = errors.New("invalid passkey name")
)

// Passkey is one credential, as the sign-in screen shows it.
type Passkey struct {
	ID         int64
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type row struct {
	ID         int64        `db:"id"`
	Name       string       `db:"name"`
	CreatedAt  time.Time    `db:"created_at"`
	LastUsedAt sql.NullTime `db:"last_used_at"`
}

const columns = `id, name, created_at, last_used_at`

func (r row) passkey() Passkey {
	p := Passkey{ID: r.ID, Name: r.Name, CreatedAt: r.CreatedAt}
	if r.LastUsedAt.Valid {
		t := r.LastUsedAt.Time
		p.LastUsedAt = &t
	}
	return p
}

type pending struct {
	session webauthn.SessionData
	expires time.Time
}

// Service manages passkeys on top of the database.
type Service struct {
	db  *sqlx.DB
	wa  *webauthn.WebAuthn
	now func() time.Time

	mu sync.Mutex
	// challenges holds each user's pending registration. A restart drops them,
	// and the user just starts again.
	challenges map[int64]pending
	// logins holds pending sign-ins by challenge. No user is known yet.
	logins map[string]pending
}

// New returns a Service. publicURL is WISHBONE_PUBLIC_URL: its host is the
// relying party ID and its origin is the only origin accepted.
func New(db *sqlx.DB, now func() time.Time, publicURL string) (*Service, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("passkey: bad public URL %q", publicURL)
	}
	// Browsers write origins and RP IDs in lowercase.
	host := strings.ToLower(u.Host)
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          strings.ToLower(u.Hostname()),
		RPDisplayName: "Wishbone",
		RPOrigins:     []string{strings.ToLower(u.Scheme) + "://" + host},
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	return &Service{db: db, wa: wa, now: func() time.Time { return now().UTC() }, challenges: map[int64]pending{}, logins: map[string]pending{}}, nil
}

// handleLength is the size of a user handle. The spec allows up to 64 bytes.
const handleLength = 32

// user is the Wishbone user as WebAuthn sees them. The handle is 32 random
// bytes kept on the user: opaque to the browser, not the user's ID, and
// Wishbone collects no email address.
type user struct {
	handle      []byte
	name        string
	credentials []webauthn.Credential
}

func (u user) WebAuthnID() []byte {
	return u.handle
}

func (u user) WebAuthnName() string                       { return u.name }
func (u user) WebAuthnDisplayName() string                { return u.name }
func (u user) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func (s *Service) loadUser(ctx context.Context, userID int64) (user, error) {
	u := user{}
	if err := s.db.GetContext(ctx, &u.name, `SELECT name FROM users WHERE id = ?`, userID); err != nil {
		return user{}, fmt.Errorf("load user: %w", err)
	}
	handle, err := s.userHandle(ctx, userID)
	if err != nil {
		return user{}, err
	}
	u.handle = handle
	var ids [][]byte
	if err := s.db.SelectContext(ctx, &ids, `SELECT credential_id FROM passkeys WHERE user_id = ?`, userID); err != nil {
		return user{}, fmt.Errorf("load passkeys: %w", err)
	}
	for _, id := range ids {
		u.credentials = append(u.credentials, webauthn.Credential{ID: id})
	}
	return u, nil
}

// userHandle returns the user's handle and makes it on first use. Two
// registrations at once agree on one handle: only the first write counts.
func (s *Service) userHandle(ctx context.Context, userID int64) ([]byte, error) {
	var handle []byte
	if err := s.db.GetContext(ctx, &handle, `SELECT webauthn_handle FROM users WHERE id = ?`, userID); err != nil {
		return nil, fmt.Errorf("load user handle: %w", err)
	}
	if len(handle) > 0 {
		return handle, nil
	}
	fresh := make([]byte, handleLength)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("random user handle: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET webauthn_handle = ? WHERE id = ? AND webauthn_handle IS NULL`, fresh, userID); err != nil {
		return nil, fmt.Errorf("store user handle: %w", err)
	}
	if err := s.db.GetContext(ctx, &handle, `SELECT webauthn_handle FROM users WHERE id = ?`, userID); err != nil {
		return nil, fmt.Errorf("load user handle: %w", err)
	}
	return handle, nil
}

// userByHandle returns the user a user handle belongs to, or [ErrNotFound].
func (s *Service) userByHandle(ctx context.Context, handle []byte) (int64, error) {
	if len(handle) == 0 {
		return 0, ErrNotFound
	}
	var id int64
	err := s.db.GetContext(ctx, &id, `SELECT id FROM users WHERE webauthn_handle = ?`, handle)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("find user by handle: %w", err)
	}
	return id, nil
}

// BeginRegistration starts adding a passkey and returns the JSON for
// navigator.credentials.create. It replaces the user's earlier pending
// registration. The credential must be discoverable, so sign-in needs no
// username.
func (s *Service) BeginRegistration(ctx context.Context, userID int64) ([]byte, error) {
	u, err := s.loadUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	creation, session, err := s.wa.BeginRegistration(u,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(u.credentials).CredentialDescriptors()),
	)
	if err != nil {
		return nil, fmt.Errorf("begin registration: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, p := range s.challenges {
		if !now.Before(p.expires) {
			delete(s.challenges, id)
		}
	}
	s.challenges[userID] = pending{session: *session, expires: now.Add(ChallengeLifetime)}
	return json.Marshal(creation)
}

// CleanName trims a passkey name and checks it. A blank name is [ErrInvalidName].
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrInvalidName
	}
	return name, nil
}

// FinishRegistration verifies the browser's answer, a JSON credential from
// navigator.credentials.create, and stores the passkey. A blank name becomes
// [DefaultName]. A pending registration works once.
func (s *Service) FinishRegistration(ctx context.Context, userID int64, name string, response []byte) (Passkey, error) {
	if strings.TrimSpace(name) == "" {
		name = DefaultName
	}
	name, err := CleanName(name)
	if err != nil {
		return Passkey{}, err
	}

	s.mu.Lock()
	p, ok := s.challenges[userID]
	delete(s.challenges, userID)
	s.mu.Unlock()
	if !ok || !s.now().Before(p.expires) {
		return Passkey{}, ErrNoChallenge
	}

	u, err := s.loadUser(ctx, userID)
	if err != nil {
		return Passkey{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(response))
	if err != nil {
		return Passkey{}, fmt.Errorf("finish registration: %w", err)
	}
	cred, err := s.wa.FinishRegistration(u, p.session, req)
	if err != nil {
		return Passkey{}, fmt.Errorf("%w: %w", ErrVerification, err)
	}

	transports := make([]string, len(cred.Transport))
	for i, t := range cred.Transport {
		transports[i] = string(t)
	}
	tj, _ := json.Marshal(transports)
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO passkeys (user_id, credential_id, public_key, attestation_type, aaguid, sign_count,
		                       transports, backup_eligible, backup_state, name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, cred.ID, cred.PublicKey, cred.AttestationType, cred.Authenticator.AAGUID,
		cred.Authenticator.SignCount, string(tj), cred.Flags.BackupEligible, cred.Flags.BackupState, name, now)
	if isUnique(err) {
		return Passkey{}, ErrDuplicate
	}
	if err != nil {
		return Passkey{}, fmt.Errorf("store passkey: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Passkey{}, fmt.Errorf("store passkey: %w", err)
	}
	return Passkey{ID: id, Name: name, CreatedAt: now}, nil
}

func isUnique(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// SortKeys are the sort keys of a user's passkeys (docs/routes.md,
// Passkeys). The first is the default: the oldest passkey first.
var SortKeys = []page.Key{{Name: "created_at", Time: true}}

// Page returns one page of a user's passkeys, and how many passkeys the user
// has. It returns the next cursor, or nil on the last page.
func (s *Service) Page(ctx context.Context, userID int64, req page.Request) (passkeys []Passkey, total int, next *string, err error) {
	if err := s.db.GetContext(ctx, &total, `SELECT count(*) FROM passkeys WHERE user_id = ?`, userID); err != nil {
		return nil, 0, nil, fmt.Errorf("count passkeys: %w", err)
	}

	dir, cmp := "ASC", ">"
	if req.Desc {
		dir, cmp = "DESC", "<"
	}
	query := `SELECT ` + columns + ` FROM passkeys WHERE user_id = ?`
	args := []any{userID}
	if req.After != nil {
		at, err := time.Parse(time.RFC3339Nano, req.After.Value)
		if err != nil {
			return nil, 0, nil, page.ErrBadCursor
		}
		query += ` AND (created_at ` + cmp + ` ? OR (created_at = ? AND id ` + cmp + ` ?))`
		args = append(args, at, at, req.After.ID)
	}
	query += ` ORDER BY created_at ` + dir + `, id ` + dir + ` LIMIT ?`
	args = append(args, req.Limit+1)

	var rows []row
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, nil, fmt.Errorf("list passkeys: %w", err)
	}
	all := make([]Passkey, len(rows))
	for i, r := range rows {
		all[i] = r.passkey()
	}
	passkeys, next = page.Slice(req, all, func(p Passkey) page.Position {
		return page.Position{Value: page.FormatTime(p.CreatedAt), ID: p.ID}
	})
	return passkeys, total, next, nil
}

// Changes is a merge patch to a passkey: the fields that the user may edit.
type Changes struct {
	Name patch.Optional[string]
}

// Update changes one of a user's passkeys and returns the passkey as it is now.
// It returns [ErrNotFound] when the user has no such passkey and
// [ErrInvalidName] for a blank, long, or null name. guard, if there is one, sees
// the passkey as it is, inside the same transaction as the change: an error from
// it stops the change. An If-Match check goes there.
func (s *Service) Update(ctx context.Context, userID, id int64, c Changes, guard func(Passkey) error) (Passkey, error) {
	var name string
	if c.Name.Set {
		if c.Name.Value == nil {
			return Passkey{}, ErrInvalidName
		}
		var err error
		if name, err = CleanName(*c.Name.Value); err != nil {
			return Passkey{}, err
		}
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Passkey{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := find(ctx, tx, userID, id)
	if err != nil {
		return Passkey{}, err
	}
	if guard != nil {
		if err := guard(current); err != nil {
			return Passkey{}, err
		}
	}
	if c.Name.Set {
		if _, err := tx.ExecContext(ctx, `UPDATE passkeys SET name = ? WHERE id = ?`, name, id); err != nil {
			return Passkey{}, fmt.Errorf("rename passkey: %w", err)
		}
		current.Name = name
	}
	if err := tx.Commit(); err != nil {
		return Passkey{}, fmt.Errorf("commit: %w", err)
	}
	return current, nil
}

// Remove deletes one of a user's passkeys. It returns [ErrNotFound] when the
// user has no such passkey. guard works as it does for [Service.Update], and gets
// the transaction as q, so it can check that the caller may still act for the
// user.
func (s *Service) Remove(ctx context.Context, userID, id int64, guard func(ctx context.Context, q sqlx.QueryerContext, current Passkey) error) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := find(ctx, tx, userID, id)
	if err != nil {
		return err
	}
	if guard != nil {
		if err := guard(ctx, tx, current); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM passkeys WHERE id = ?`, id); err != nil {
		return fmt.Errorf("remove passkey: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func find(ctx context.Context, q sqlx.ExtContext, userID, id int64) (Passkey, error) {
	var r row
	err := sqlx.GetContext(ctx, q, &r, `SELECT `+columns+` FROM passkeys WHERE id = ? AND user_id = ?`, id, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Passkey{}, ErrNotFound
	}
	if err != nil {
		return Passkey{}, fmt.Errorf("find passkey: %w", err)
	}
	return r.passkey(), nil
}
