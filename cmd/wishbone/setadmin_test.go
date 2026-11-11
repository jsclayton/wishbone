package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/store"
)

func runSetAdmin(t *testing.T, dir, arg string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := (&SetAdminCmd{DataDir: dir, User: arg, out: &out}).Run()
	return out.String(), err
}

func listUsers(t *testing.T, dir string) string {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, (&UsersCmd{DataDir: dir, out: &out}).Run())
	return out.String()
}

func TestSetAdminMovesTheRoleAndTheOldAdminKeepsTheirSessions(t *testing.T) {
	dir, st := newDB(t)
	ada := addUser(t, st, "Ada", "Lovelace", true)
	addUser(t, st, "Grace", "", false)
	token, _, err := session.Create(t.Context(), st.DB(), time.Now(), ada, "browser")
	require.NoError(t, err)

	out, err := runSetAdmin(t, dir, "2")

	require.NoError(t, err)
	require.Equal(t, "Grace is now the admin. Ada Lovelace is not the admin any more and keeps their sessions.\n", out)
	require.Equal(t, "1\tAda Lovelace\n2\tGrace\tadmin\n", listUsers(t, dir))
	_, err = session.NewService(st.DB(), time.Now).Authenticate(t.Context(), token)
	require.NoError(t, err, "the old admin is still signed in")
}

func TestSetAdminOnTheAdminChangesNothing(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "", false)

	out, err := runSetAdmin(t, dir, "1")

	require.NoError(t, err)
	require.Equal(t, "Ada is already the admin.\n", out)
	require.Equal(t, "1\tAda\tadmin\n2\tGrace\n", listUsers(t, dir))
}

func TestSetAdminTakesAnIDOfAUserWhoExists(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)

	for _, arg := range []string{"Ada", "0", "-1", "99", ""} {
		out, err := runSetAdmin(t, dir, arg)
		require.Error(t, err, arg)
		require.Empty(t, out, arg)
	}
	require.ErrorContains(t, func() error { _, err := runSetAdmin(t, dir, "Ada"); return err }(), "wishbone users")
	require.Equal(t, "1\tAda\tadmin\n", listUsers(t, dir))
}

func TestSetAdminRefusesAUserWhoHasParents(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Mom", "", false)
	addUser(t, st, "Emma", "", false)
	_, err := st.DB().ExecContext(t.Context(), `INSERT INTO user_parents (child_id, parent_id) VALUES (3, 2)`)
	require.NoError(t, err)

	out, err := runSetAdmin(t, dir, "3")

	require.ErrorContains(t, err, "Emma has parents, so can't be the admin")
	require.Empty(t, out)
	require.Equal(t, "1\tAda\tadmin\n2\tMom\n3\tEmma\n", listUsers(t, dir), "nothing changed")

	out, err = runSetAdmin(t, dir, "2")
	require.NoError(t, err, "a parent can be the admin: only a user who has parents can't")
	require.Contains(t, out, "Mom is now the admin")
}

func TestSetAdminRefusesWhenAMigrationIsPending(t *testing.T) {
	dir, _ := newPendingDB(t)

	_, err := runSetAdmin(t, dir, "1")

	require.ErrorIs(t, err, store.ErrMigrationPending)
}

func TestSetAdminRefusesADeactivatedUser(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	grace := addUser(t, st, "Grace", "", false)
	deactivate(t, st, grace)

	out, err := runSetAdmin(t, dir, "2")

	require.ErrorContains(t, err, "Grace is deactivated, so can't be the admin")
	require.Empty(t, out)
	require.Equal(t, "1\tAda\tadmin\n2\tGrace\tdeactivated\n", listUsers(t, dir), "nothing changed")
}
