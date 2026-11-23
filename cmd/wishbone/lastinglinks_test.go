package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/config"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/store"
)

// lastingURL is a lasting link as the CLI prints it.
var lastingURL = regexp.MustCompile(`^https://wish\.example/login/[2-9a-km-np-z]{4}(-[2-9a-km-np-z]{4}){3}$`)

func runLastingLink(t *testing.T, dir, arg string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := &LoginLinkCmd{
		Config:  config.Config{PublicURL: "https://wish.example", DataDir: dir, RateLimitRedeem: 10},
		User:    arg,
		Lasting: true,
		out:     &out,
		stderr:  &errOut,
	}
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func runLastingLinks(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := (&LastingLinksCmd{DataDir: dir, out: &out}).Run()
	return out.String(), err
}

func runRevokeLastingLink(t *testing.T, dir, arg string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := (&RevokeLastingLinkCmd{DataDir: dir, Link: arg, out: &out}).Run()
	return out.String(), err
}

func TestLoginLinkLastingPrintsALinkThatWorksMoreThanOnce(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "Lovelace", true)

	out, stderr, err := runLastingLink(t, dir, "1")

	require.NoError(t, err)
	url := strings.TrimSpace(out)
	require.Regexp(t, lastingURL, url, "stdout is a bare URL")
	require.Contains(t, stderr, "Lasting link 1 for Ada Lovelace")
	require.Contains(t, stderr, "doesn't expire")
	require.Contains(t, stderr, "wishbone revoke-lasting-link 1")
	svc := loginlink.NewService(st.DB(), time.Now)
	token := strings.TrimPrefix(url, "https://wish.example/login/")
	for range 3 {
		res, err := svc.Redeem(t.Context(), token, 0, "ua")
		require.NoError(t, err)
		require.Equal(t, "Ada", res.User.Name, "the admin may have one")
	}
}

func TestLoginLinkWithoutLastingStillMakesAnOrdinaryLink(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)

	out, _, err := runLoginLink(t, dir, "1")

	require.NoError(t, err)
	require.NotRegexp(t, lastingURL, strings.TrimSpace(out))
	var lasting int
	require.NoError(t, st.DB().GetContext(t.Context(), &lasting, `SELECT count(*) FROM lasting_links`))
	require.Zero(t, lasting)
}

func TestLoginLinkLastingRefusesADeactivatedUser(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	deactivate(t, st, addUser(t, st, "Grace", "", false))

	out, _, err := runLastingLink(t, dir, "2")

	require.Empty(t, out)
	require.ErrorContains(t, err, "Grace is deactivated")
	var links int
	require.NoError(t, st.DB().GetContext(t.Context(), &links, `SELECT count(*) FROM lasting_links`))
	require.Zero(t, links, "no link was made")
}

func TestLastingLinksListsTheLinksThatAreNotRevoked(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "Lovelace", true)
	grace := addUser(t, st, "Grace", "", false)
	for _, id := range []string{"1", "2", "2"} {
		_, _, err := runLastingLink(t, dir, id)
		require.NoError(t, err)
	}
	_, err := runRevokeLastingLink(t, dir, "2")
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(), `UPDATE lasting_links SET created_at = ?`,
		time.Date(2026, 11, 19, 18, 5, 59, 0, time.UTC))
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(), `UPDATE lasting_links SET last_used_at = ? WHERE id = 1`,
		time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC))
	require.NoError(t, err)
	deactivate(t, st, grace)

	out, err := runLastingLinks(t, dir)

	require.NoError(t, err)
	require.Equal(t,
		"1\t1\tAda Lovelace\tmade 2026-11-19 18:05 UTC\tlast used 2026-11-20 09:30 UTC\n"+
			"3\t2\tGrace\tmade 2026-11-19 18:05 UTC\tnever used\tdeactivated\n", out)
}

func TestLastingLinksWithNoLinksPrintsNothing(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)

	out, err := runLastingLinks(t, dir)

	require.NoError(t, err)
	require.Empty(t, out)
}

func TestRevokeLastingLinkSignsOutTheSessionsItStarted(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	out, _, err := runLastingLink(t, dir, "1")
	require.NoError(t, err)
	token := strings.TrimPrefix(strings.TrimSpace(out), "https://wish.example/login/")
	svc := loginlink.NewService(st.DB(), time.Now)
	for range 2 {
		_, err := svc.Redeem(t.Context(), token, 0, "ua")
		require.NoError(t, err)
	}

	out, err = runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, `Revoked lasting link 1 for Ada.
It removed what it left that still signed someone in:
  Ada (1): 2 sessions signed out, 0 passkeys deleted, 0 unused login links ended.
Ada has no passkey. To sign Ada in again, make a login link with 'wishbone login-link 1'.
`, out, "a link made since Wishbone records what its sessions make has nothing else to check")
	_, err = svc.Redeem(t.Context(), token, 0, "ua")
	require.ErrorIs(t, err, loginlink.ErrInvalid)
	var live int
	require.NoError(t, st.DB().GetContext(t.Context(), &live, `SELECT count(*) FROM sessions WHERE revoked_at IS NULL`))
	require.Zero(t, live)
}

func TestRevokeLastingLinkThatStartedNothingSaysSo(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "Hopper", false)
	_, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)

	out, err := runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, `Revoked lasting link 1 for Grace Hopper.
No session that it started was live, and its sessions made no passkey or login link that still worked.
`, out)
}

// addPasskey adds a passkey for a user, as a session that records the lasting
// link would, or no link for 0.
func addPasskey(t *testing.T, st *store.Store, userID, linkID int64, name string) {
	t.Helper()
	var link any
	if linkID != 0 {
		link = linkID
	}
	_, err := st.DB().ExecContext(t.Context(),
		`INSERT INTO passkeys (user_id, credential_id, public_key, name, created_at, lasting_link_id) VALUES (?, randomblob(16), x'00', ?, ?, ?)`,
		userID, name, time.Now(), link)
	require.NoError(t, err)
}

// The revoke says what it removed, user by user, and names each user it
// touched who has no passkey left.
func TestRevokeLastingLinkSaysWhatItRemovedByUser(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	mom := addUser(t, st, "Mom", "Smith", false)
	emma := addUser(t, st, "Emma", "", false)
	out, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)
	token := strings.TrimPrefix(strings.TrimSpace(out), "https://wish.example/login/")
	svc := loginlink.NewService(st.DB(), time.Now)
	fromLink, err := svc.Redeem(t.Context(), token, 0, "ua")
	require.NoError(t, err)
	linkID := fromLink.Session.LastingLinkID
	addPasskey(t, st, mom, linkID, "Leaked")
	addPasskey(t, st, mom, 0, "Mom's own")
	forEmma, _, err := svc.Create(t.Context(), emma, linkID)
	require.NoError(t, err)
	_, _, err = svc.Create(t.Context(), emma, linkID)
	require.NoError(t, err)
	emmaSession, err := svc.Redeem(t.Context(), forEmma, 0, "ua")
	require.NoError(t, err)
	require.Equal(t, linkID, emmaSession.Session.LastingLinkID)
	addPasskey(t, st, emma, linkID, "Emma's \x1b[31mphone")

	out, err = runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, `Revoked lasting link 1 for Mom Smith.
It removed what it left that still signed someone in:
  Mom Smith (2): 1 session signed out, 1 passkey deleted ("Leaked"), 0 unused login links ended.
  Emma (3): 1 session signed out, 1 passkey deleted ("Emma's \x1b[31mphone"), 1 unused login link ended.
Emma has no passkey. To sign Emma in again, make a login link with 'wishbone login-link 3'.
`, out)
}

// A user whom a leaked session signed in with a login link, and who never added
// a passkey, has no way in once the revoke ends that session. So the revoke
// names that user too, though it deleted no passkey of theirs. A user it
// touched who still has a passkey isn't named.
func TestRevokeLastingLinkNamesAUserItSignedOutWhoHasNoPasskey(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	mom := addUser(t, st, "Mom", "Smith", false)
	emma := addUser(t, st, "Emma", "", false)
	out, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)
	token := strings.TrimPrefix(strings.TrimSpace(out), "https://wish.example/login/")
	svc := loginlink.NewService(st.DB(), time.Now)
	fromLink, err := svc.Redeem(t.Context(), token, 0, "ua")
	require.NoError(t, err)
	linkID := fromLink.Session.LastingLinkID
	addPasskey(t, st, mom, 0, "Mom's own")
	forEmma, _, err := svc.Create(t.Context(), emma, linkID)
	require.NoError(t, err)
	_, err = svc.Redeem(t.Context(), forEmma, 0, "ua")
	require.NoError(t, err)

	out, err = runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, `Revoked lasting link 1 for Mom Smith.
It removed what it left that still signed someone in:
  Mom Smith (2): 1 session signed out, 0 passkeys deleted, 0 unused login links ended.
  Emma (3): 1 session signed out, 0 passkeys deleted, 0 unused login links ended.
Emma has no passkey. To sign Emma in again, make a login link with 'wishbone login-link 3'.
`, out)
}

// A link made before Wishbone recorded what its sessions make can have left
// passkeys and login links that record nothing, so the revoke still says what
// else to check.
func TestRevokeLastingLinkSaysWhatElseToCheckForAnOlderLink(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "Hopper", false)
	_, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(), `UPDATE lasting_links SET predates_provenance = 1`)
	require.NoError(t, err)

	out, err := runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, "Revoked lasting link 1 for Grace Hopper.\n"), out)
	for _, want := range []string{
		"This link is older than Wishbone's record of what a lasting link's sessions make.",
		"A passkey that one of them added for Grace.",
		"A login link that one of them made for Grace or for one of Grace's children.",
		"open Grace's sign-in screen, /users/2/sign-in.",
		"Do the same for Grace's children.",
		"look again in 7 days",
	} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "admin.")
}

// For the admin's link, the check covers every user, because the admin makes
// login links for anyone.
func TestRevokeLastingLinkOfTheAdminSaysToCheckEveryUser(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	_, _, err := runLastingLink(t, dir, "1")
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(), `UPDATE lasting_links SET predates_provenance = 1`)
	require.NoError(t, err)

	out, err := runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, `Revoked lasting link 1 for Ada.
No session that it started was live, and its sessions made no passkey or login link that still worked.

This link is older than Wishbone's record of what a lasting link's sessions make.
So the revoke leaves what those sessions made before the upgrade, and it still works:
  - A passkey that one of them added for Ada. It works until it is deleted.
  - A login link that one of them made for any user, because Ada is the admin. It works once, for up to 7 days, and the session it starts stays.
To check, sign in to Wishbone as the admin and open Ada's sign-in screen, /users/1/sign-in.
Delete each passkey and revoke each session that you don't know. Do the same for every other user.
No CLI command does this, and nothing lists a login link that isn't used yet, so look again in 7 days.
`, out)
}

// A database from before the migration: the link and its session are there,
// and so is a passkey that its session added, which records nothing. The
// revoke ends the session, leaves the passkey, and says what else to check.
func TestRevokeLastingLinkLeavesRowsFromBeforeTheMigration(t *testing.T) {
	dir, st := newPendingDB(t)
	require.NoError(t, st.MigrateTo(t.Context(), 20261122043018))
	grace := addUser(t, st, "Grace", "", true)
	svc := loginlink.NewService(st.DB(), time.Now)
	token, _, err := svc.CreateLasting(t.Context(), grace)
	require.NoError(t, err)
	_, err = svc.Redeem(t.Context(), token, 0, "ua")
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(),
		`INSERT INTO passkeys (user_id, credential_id, public_key, name, created_at) VALUES (?, x'01', x'00', 'Old', ?)`, grace, time.Now())
	require.NoError(t, err)
	_, err = st.DB().ExecContext(t.Context(),
		`INSERT INTO login_links (user_id, token_hash, created_at, expires_at) VALUES (?, 'old', ?, ?)`, grace, time.Now().UTC(), time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, st.Migrate(t.Context()))

	out, err := runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, `Revoked lasting link 1 for Grace.
It removed what it left that still signed someone in:
  Grace (1): 1 session signed out, 0 passkeys deleted, 0 unused login links ended.

This link is older than Wishbone's record of what a lasting link's sessions make.
`), out)
	var left struct {
		Passkeys   int `db:"passkeys"`
		LoginLinks int `db:"login_links"`
	}
	require.NoError(t, st.DB().GetContext(t.Context(), &left, `SELECT
		(SELECT count(*) FROM passkeys) AS passkeys,
		(SELECT count(*) FROM login_links WHERE expires_at > ?) AS login_links`, time.Now().UTC()))
	require.Equal(t, 1, left.Passkeys, "a passkey from before records no link, so it stays")
	require.Equal(t, 1, left.LoginLinks, "and so does a login link")
}

func TestRevokeLastingLinkRefusesAnUnknownOrRevokedLink(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	_, _, err := runLastingLink(t, dir, "1")
	require.NoError(t, err)
	_, err = runRevokeLastingLink(t, dir, "1")
	require.NoError(t, err)

	for arg, want := range map[string]string{
		"1": "no lasting link with ID 1", "9": "no lasting link with ID 9", "ada": "wishbone lasting-links", "0": "wishbone lasting-links",
	} {
		out, err := runRevokeLastingLink(t, dir, arg)
		require.Empty(t, out, arg)
		require.ErrorContains(t, err, want, arg)
	}
}

func TestTheLastingLinkCommandsRefuseWhenAMigrationIsPending(t *testing.T) {
	dir, _ := newPendingDB(t)

	_, _, err := runLastingLink(t, dir, "1")
	require.ErrorIs(t, err, store.ErrMigrationPending)
	_, err = runLastingLinks(t, dir)
	require.ErrorIs(t, err, store.ErrMigrationPending)
	_, err = runRevokeLastingLink(t, dir, "1")
	require.ErrorIs(t, err, store.ErrMigrationPending)
}
