package passkey

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// MaxPendingLogins caps the sign-in challenges held at once. Anyone can start
// a sign-in, so the server drops the oldest challenge past this number.
const MaxPendingLogins = 1000

var (
	// ErrLoginFailed means a passkey sign-in did not work. It says nothing about
	// why, so a caller can show one message for every cause.
	ErrLoginFailed = errors.New("passkey sign-in failed")
	// ErrSignCount means the passkey's sign count did not go up, which can mean
	// a cloned authenticator. It always comes wrapped in ErrLoginFailed.
	ErrSignCount = errors.New("passkey sign count went backwards")
)

// BeginLogin starts a sign-in with no username and returns the JSON for
// navigator.credentials.get. The challenge lasts [ChallengeLifetime] and works
// once. The options name no user and list no credentials.
func (s *Service) BeginLogin(_ context.Context) ([]byte, error) {
	assertion, session, err := s.wa.BeginDiscoverableLogin()
	if err != nil {
		return nil, fmt.Errorf("begin login: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for c, p := range s.logins {
		if !now.Before(p.expires) {
			delete(s.logins, c)
		}
	}
	for len(s.logins) >= MaxPendingLogins {
		var oldest string
		var oldestExpires time.Time
		for c, p := range s.logins {
			if oldest == "" || p.expires.Before(oldestExpires) {
				oldest, oldestExpires = c, p.expires
			}
		}
		delete(s.logins, oldest)
	}
	s.logins[session.Challenge] = pending{session: *session, expires: now.Add(ChallengeLifetime)}
	return json.Marshal(assertion)
}

// FinishLogin verifies the browser's answer, a JSON credential from
// navigator.credentials.get, and returns the ID of the user who owns the
// passkey. It records the use and the new sign count. Every failure wraps
// [ErrLoginFailed].
func (s *Service) FinishLogin(ctx context.Context, response []byte) (int64, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrLoginFailed, err)
	}
	// The challenge in the signed client data picks the pending sign-in.
	s.mu.Lock()
	p, ok := s.logins[parsed.Response.CollectedClientData.Challenge]
	delete(s.logins, parsed.Response.CollectedClientData.Challenge)
	s.mu.Unlock()
	if !ok || !s.now().Before(p.expires) {
		return 0, fmt.Errorf("%w: no pending sign-in", ErrLoginFailed)
	}

	var userID int64
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		id, err := s.userByHandle(ctx, userHandle)
		if err != nil {
			return nil, err
		}
		u, err := s.loadLoginUser(ctx, id)
		if err != nil {
			return nil, err
		}
		userID = id
		return u, nil
	}
	cred, err := s.wa.ValidateDiscoverableLogin(handler, p.session, parsed)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrLoginFailed, err)
	}
	if cred.Authenticator.CloneWarning {
		return 0, fmt.Errorf("%w: %w", ErrLoginFailed, ErrSignCount)
	}

	// The count check repeats in SQL, so two sign-ins with one count can't both pass.
	res, err := s.db.ExecContext(ctx,
		`UPDATE passkeys SET sign_count = ?, last_used_at = ?, backup_state = ?
		 WHERE credential_id = ? AND user_id = ? AND (sign_count < ? OR (sign_count = 0 AND ? = 0))`,
		int64(cred.Authenticator.SignCount), s.now(), cred.Flags.BackupState,
		cred.ID, userID, int64(cred.Authenticator.SignCount), int64(cred.Authenticator.SignCount))
	if err != nil {
		return 0, fmt.Errorf("record passkey use: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, fmt.Errorf("%w: %w", ErrLoginFailed, ErrSignCount)
	}
	return userID, nil
}

// loadLoginUser loads a user with the full credentials a sign-in checks. A
// deactivated user is no user here: their passkeys don't sign them in.
func (s *Service) loadLoginUser(ctx context.Context, userID int64) (user, error) {
	var m struct {
		Name   string `db:"name"`
		Handle []byte `db:"webauthn_handle"`
	}
	if err := s.db.GetContext(ctx, &m, `SELECT name, webauthn_handle FROM users WHERE id = ? AND deactivated_at IS NULL`, userID); err != nil {
		return user{}, fmt.Errorf("load user: %w", err)
	}
	var rows []struct {
		ID             []byte `db:"credential_id"`
		PublicKey      []byte `db:"public_key"`
		AAGUID         []byte `db:"aaguid"`
		SignCount      uint32 `db:"sign_count"`
		BackupEligible bool   `db:"backup_eligible"`
		BackupState    bool   `db:"backup_state"`
	}
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT credential_id, public_key, aaguid, sign_count, backup_eligible, backup_state
		 FROM passkeys WHERE user_id = ?`, userID); err != nil {
		return user{}, fmt.Errorf("load passkeys: %w", err)
	}
	u := user{handle: m.Handle, name: m.Name}
	for _, r := range rows {
		u.credentials = append(u.credentials, webauthn.Credential{
			ID:        r.ID,
			PublicKey: r.PublicKey,
			Flags:     webauthn.CredentialFlags{BackupEligible: r.BackupEligible, BackupState: r.BackupState},
			Authenticator: webauthn.Authenticator{
				AAGUID:    r.AAGUID,
				SignCount: r.SignCount,
			},
		})
	}
	if len(u.credentials) == 0 {
		return user{}, sql.ErrNoRows // handled as unknown credential by the library
	}
	return u, nil
}
