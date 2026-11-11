package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/config"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

func addUser(t *testing.T, st *store.Store, name, last string, admin bool) int64 {
	t.Helper()
	id, err := user.Create(t.Context(), st.DB(), time.Now(), user.NewUser{
		Name: name, LastName: last, IsAdmin: admin, Birthday: user.PartialDate{Month: 1, Day: 1},
	})
	require.NoError(t, err)
	return id
}

func TestUsersPrintsOneLineForEachUserWithIDOnly(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "Lovelace", true)
	addUser(t, st, "Grace", "", false)
	var out bytes.Buffer

	require.NoError(t, (&UsersCmd{DataDir: dir, out: &out}).Run())

	require.Equal(t, "1\tAda Lovelace\tadmin\n2\tGrace\n", out.String())
}

func TestUsersWithNoUsersPrintsNothing(t *testing.T) {
	dir, _ := newDB(t)
	var out bytes.Buffer

	require.NoError(t, (&UsersCmd{DataDir: dir, out: &out}).Run())

	require.Empty(t, out.String())
}

func TestUsersRefusesWhenAMigrationIsPending(t *testing.T) {
	dir, _ := newPendingDB(t)

	err := (&UsersCmd{DataDir: dir, out: &bytes.Buffer{}}).Run()

	require.ErrorIs(t, err, store.ErrMigrationPending)
	require.ErrorContains(t, err, "start the server")
}

func runLoginLink(t *testing.T, dir, arg string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := &LoginLinkCmd{
		Config: config.Config{PublicURL: "https://wish.example", DataDir: dir, RateLimitRedeem: 10},
		User:   arg,
		out:    &out,
		stderr: &errOut,
	}
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func TestLoginLinkPrintsALinkThatSignsTheUserIn(t *testing.T) {
	dir, st := newDB(t)
	id := addUser(t, st, "Ada", "", true)

	out, stderr, err := runLoginLink(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, int64(1), id)
	require.Contains(t, stderr, "7 days")
	url := strings.TrimSpace(out)
	require.True(t, strings.HasPrefix(url, "https://wish.example/login/"), url)
	svc := loginlink.NewService(st.DB(), time.Now)
	res, err := svc.Redeem(t.Context(), strings.TrimPrefix(url, "https://wish.example/login/"), 0, "ua")
	require.NoError(t, err)
	require.Equal(t, "Ada", res.User.Name)
}

func TestLoginLinkTakesAnIDAndNotAName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)

	out, _, err := runLoginLink(t, dir, "Ada")

	require.Empty(t, out)
	require.ErrorContains(t, err, "wishbone users")
}

func TestLoginLinkForAnUnknownUserFails(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)

	out, _, err := runLoginLink(t, dir, "99")

	require.Empty(t, out)
	require.ErrorContains(t, err, "no user with ID 99")
}

func TestLoginLinkRefusesWhenAMigrationIsPending(t *testing.T) {
	dir, _ := newPendingDB(t)

	out, _, err := runLoginLink(t, dir, "1")

	require.Empty(t, out)
	require.ErrorIs(t, err, store.ErrMigrationPending)
}

// deactivate pauses a user straight in the database, as the admin's route does.
func deactivate(t *testing.T, st *store.Store, id int64) {
	t.Helper()
	_, err := user.NewService(st.DB(), time.Now).Deactivate(t.Context(), id)
	require.NoError(t, err)
}

func TestUsersMarksADeactivatedUser(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "Lovelace", true)
	grace := addUser(t, st, "Grace", "", false)
	deactivate(t, st, grace)
	var out bytes.Buffer

	require.NoError(t, (&UsersCmd{DataDir: dir, out: &out}).Run())

	require.Equal(t, "1\tAda Lovelace\tadmin\n2\tGrace\tdeactivated\n", out.String())
}

func TestLoginLinkRefusesADeactivatedUser(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	grace := addUser(t, st, "Grace", "", false)
	deactivate(t, st, grace)

	out, _, err := runLoginLink(t, dir, "2")

	require.Empty(t, out)
	require.ErrorContains(t, err, "Grace is deactivated")
	var links int
	require.NoError(t, st.DB().GetContext(t.Context(), &links, `SELECT count(*) FROM login_links`))
	require.Zero(t, links, "no link was made")
}
