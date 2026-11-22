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
	require.Equal(t, `Revoked lasting link 1 for Ada, and signed out the 2 sessions it started.

The revoke leaves what those sessions made, and it still works:
  - A passkey that one of them added for Ada. It works until it is deleted.
  - A login link that one of them made for any user, because Ada is the admin. It works once, for up to 7 days, and the session it starts stays.
To check, sign in to Wishbone as the admin and open Ada's sign-in screen, /users/1/sign-in.
Delete each passkey and revoke each session that you don't know. Do the same for every other user.
No CLI command does this, and nothing lists a login link that isn't used yet, so look again in 7 days.
`, out)
	_, err = svc.Redeem(t.Context(), token, 0, "ua")
	require.ErrorIs(t, err, loginlink.ErrInvalid)
	var live int
	require.NoError(t, st.DB().GetContext(t.Context(), &live, `SELECT count(*) FROM sessions WHERE revoked_at IS NULL`))
	require.Zero(t, live)
}

// A user who isn't the admin can make a login link only for themselves and
// their children, so the check after a revoke covers only those.
func TestRevokeLastingLinkSaysWhatElseToCheck(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "Hopper", false)
	_, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)

	out, err := runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, "Revoked lasting link 1 for Grace Hopper, and signed out the 0 sessions it started.\n"), out)
	for _, want := range []string{
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
