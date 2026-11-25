package main

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/store"
)

// Wishbone refuses a control character in a name, but a row from before that
// rule can hold one (docs/spec.md, CLI). So every command that prints a name
// escapes each control character in it, and nothing a user wrote acts on the
// operator's terminal. These tests write such a row straight to the database,
// as an old one would be.

// A name that clears the screen, and a last name with a C1 control.
const (
	rawName     = "Eve\x1b[2J"
	rawLastName = "Mal\u009b31m"
	// shownName is rawName as the CLI prints it.
	shownName = `Eve\x1b[2J`
	// shownFullName is the name and the last name as the CLI prints them.
	shownFullName = `Eve\x1b[2J Mal\u009b31m`
)

// addOldUser adds a user whose name and last name hold control characters, as
// a row from before the rule could.
func addOldUser(t *testing.T, st *store.Store, admin bool) int64 {
	t.Helper()
	id := addUser(t, st, "Eve", "Mallory", admin)
	exec(t, st, `UPDATE users SET name = ?, last_name = ? WHERE id = ?`, rawName, rawLastName, id)
	return id
}

// requireNoControl checks that out holds no control character but the line
// breaks and tabs that the CLI writes itself.
func requireNoControl(t *testing.T, out string) {
	t.Helper()
	i := strings.IndexFunc(out, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
	require.Equal(t, -1, i, "a control character reached the terminal: %q", out)
}

func TestCliTextEscapesEachControlCharacterAndNothingElse(t *testing.T) {
	for raw, shown := range map[string]string{
		"Ada":                       "Ada",
		"José 👨\u200d👩\u200d👧":      "José 👨\u200d👩\u200d👧",
		"Ada\x1b]0;owned\x07":       `Ada\x1b]0;owned\a`,
		"A\nB\tC\rD":                `A\nB\tC\rD`,
		"A\x00B\x7fC\u0085D\u009bE": `A\x00B\x7fC\u0085D\u009bE`,
		`Back\slash "quoted"`:       `Back\slash "quoted"`,
		"Bad \x9b byte":             `Bad \x9b byte`,
		"Good \ufffd":               "Good \ufffd",
	} {
		require.Equal(t, shown, cliText(raw), "%q", raw)
	}
}

// A line break in a name would make the users command print a line for a user
// who doesn't exist.
func TestUsersEscapesAControlCharacterInAName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addOldUser(t, st, false)
	exec(t, st, `UPDATE users SET name = ? WHERE id = 1`, "Ada\n3\tGrace\tadmin")

	out := listUsers(t, dir)

	require.Equal(t, "1\tAda\\n3\\tGrace\\tadmin\tadmin\n2\t"+shownFullName+"\n", out)
	requireNoControl(t, out)
}

func TestLoginLinkEscapesAControlCharacterInAName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	eve := addOldUser(t, st, false)

	_, stderr, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)
	require.Contains(t, stderr, "Lasting link 1 for "+shownFullName+".")
	requireNoControl(t, stderr)

	deactivate(t, st, eve)
	for _, lasting := range []bool{false, true} {
		var err error
		if lasting {
			_, _, err = runLastingLink(t, dir, "2")
		} else {
			_, _, err = runLoginLink(t, dir, "2")
		}
		require.ErrorContains(t, err, shownFullName+" is deactivated")
		requireNoControl(t, err.Error())
	}
}

func TestLastingLinksEscapesAControlCharacterInAName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addOldUser(t, st, false)
	_, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)

	out, err := runLastingLinks(t, dir)

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, "1\t2\t"+shownFullName+"\tmade "), out)
	requireNoControl(t, out)
}

func TestRevokeLastingLinkEscapesAControlCharacterInEachName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	eve := addOldUser(t, st, false)
	out, _, err := runLastingLink(t, dir, "2")
	require.NoError(t, err)
	token := strings.TrimPrefix(strings.TrimSpace(out), "https://wish.example/login/")
	_, err = loginlink.NewService(st.DB(), time.Now).Redeem(t.Context(), token, 0, "ua")
	require.NoError(t, err)
	addPasskey(t, st, eve, 1, "Eve's \x1b[31mphone")
	exec(t, st, `UPDATE lasting_links SET predates_provenance = 1`)

	out, err = runRevokeLastingLink(t, dir, "1")

	require.NoError(t, err)
	for _, want := range []string{
		"Revoked lasting link 1 for " + shownFullName + ".\n",
		"  " + shownFullName + ` (2): 1 session signed out, 1 passkey deleted ("Eve's \x1b[31mphone"), 0 unused login links ended.`,
		shownFullName + " has no passkey. To sign " + shownName + " in again,",
		"A passkey that one of them added for " + shownName + ".",
		"open " + shownName + "'s sign-in screen",
	} {
		require.Contains(t, out, want)
	}
	requireNoControl(t, out)
}

func TestSetAdminEscapesAControlCharacterInEachName(t *testing.T) {
	dir, st := newDB(t)
	addOldUser(t, st, true)
	addUser(t, st, "Grace", "", false)
	child := addUser(t, st, "Emma", "", false)
	exec(t, st, `INSERT INTO user_parents (child_id, parent_id) VALUES (3, 2)`)
	exec(t, st, `UPDATE users SET name = ? WHERE id = 3`, rawName)

	_, err := runSetAdmin(t, dir, "3")
	require.ErrorContains(t, err, shownName+" has parents")
	requireNoControl(t, err.Error())

	exec(t, st, `DELETE FROM user_parents`)
	deactivate(t, st, child)
	_, err = runSetAdmin(t, dir, "3")
	require.ErrorContains(t, err, shownName+" is deactivated")
	requireNoControl(t, err.Error())

	out, err := runSetAdmin(t, dir, "1")
	require.NoError(t, err)
	require.Equal(t, shownFullName+" is already the admin.\n", out)
	requireNoControl(t, out)

	out, err = runSetAdmin(t, dir, "2")
	require.NoError(t, err)
	require.Contains(t, out, "Grace is now the admin. "+shownFullName+" is not the admin any more")
	requireNoControl(t, out)
}

// The operator confirms a delete by typing the name as the CLI shows it.
func TestDeleteUserEscapesAControlCharacterInEachName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addOldUser(t, st, false)
	addUser(t, st, "Emma", "", false)
	exec(t, st, `INSERT INTO user_parents (child_id, parent_id) VALUES (3, 2)`)
	exec(t, st, `UPDATE users SET name = ? WHERE id = 3`, "Emma\x1b[8m")

	out, err := runDeleteUser(t, dir, "2", rawName+" "+rawLastName+"\n", false)
	require.ErrorIs(t, err, errAborted, "the raw name isn't what the CLI showed")
	require.Contains(t, out, "Delete "+shownFullName+" (user 2). This can't be undone.")
	require.Contains(t, out, "  Lists "+shownName+" leaves, because another recipient stays: ")
	require.Contains(t, out, `Type the name "`+shownFullName+`" to delete this user for good: `)
	requireNoControl(t, out)

	out, err = runDeleteUser(t, dir, "2", shownFullName+"\n", false)
	require.NoError(t, err)
	require.Contains(t, out, "\nDeleted "+shownFullName+".\n")
	require.True(t, strings.HasSuffix(out, "3\tEmma\\x1b[8m\n"), out)
	requireNoControl(t, out)

	exec(t, st, `UPDATE users SET name = ? WHERE id = 1`, rawName)
	_, err = runDeleteUser(t, dir, "1", "", true)
	require.ErrorContains(t, err, shownName+" is the admin")
	requireNoControl(t, err.Error())
}
