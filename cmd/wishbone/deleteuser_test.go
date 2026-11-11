package main

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/store"
)

func runDeleteUser(t *testing.T, dir, arg, stdin string, yes bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := (&DeleteUserCmd{DataDir: dir, User: arg, Yes: yes, in: strings.NewReader(stdin), out: &out}).Run()
	return out.String(), err
}

func exec(t *testing.T, st *store.Store, query string, args ...any) {
	t.Helper()
	_, err := st.DB().ExecContext(t.Context(), query, args...)
	require.NoError(t, err, query)
}

func count(t *testing.T, st *store.Store, query string, args ...any) (n int) {
	t.Helper()
	require.NoError(t, st.DB().GetContext(t.Context(), &n, query, args...))
	return n
}

func TestDeleteUserDeletesTheUserByIDAndSaysSo(t *testing.T) {
	dir, st := newDB(t)
	ada := addUser(t, st, "Ada", "Lovelace", true)
	grace := addUser(t, st, "Grace", "Hopper", false)
	for _, id := range []int64{ada, grace} {
		_, _, err := session.Create(t.Context(), st.DB(), time.Now(), id, "browser")
		require.NoError(t, err)
	}

	out, err := runDeleteUser(t, dir, "2", "", true)

	require.NoError(t, err)
	require.Contains(t, out, "Deleted Grace Hopper.")
	require.Equal(t, "1\tAda Lovelace\tadmin\n", listUsers(t, dir))
	require.Zero(t, count(t, st, `SELECT count(*) FROM sessions WHERE user_id = ?`, grace), "her session goes with her")
	require.Equal(t, 1, count(t, st, `SELECT count(*) FROM sessions WHERE user_id = ?`, ada), "nobody else's does")
}

func TestDeleteUserTakesAnIDOfAUserWhoExists(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "", false)

	for _, arg := range []string{"Grace", "0", "-1", "99", ""} {
		out, err := runDeleteUser(t, dir, arg, "", true)
		require.Error(t, err, arg)
		require.Empty(t, out, arg)
	}
	_, err := runDeleteUser(t, dir, "Grace", "", true)
	require.ErrorContains(t, err, "wishbone users")
	_, err = runDeleteUser(t, dir, "99", "", true)
	require.ErrorContains(t, err, "no user with ID 99")
	require.Equal(t, "1\tAda\tadmin\n2\tGrace\n", listUsers(t, dir))
}

func TestDeleteUserRefusesTheAdminBeforeItAsksAnything(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "", false)

	out, err := runDeleteUser(t, dir, "1", "", false)

	require.ErrorContains(t, err, "Ada is the admin")
	require.ErrorContains(t, err, "wishbone set-admin")
	require.NotContains(t, out, "Type the name", "it doesn't ask")
	require.Equal(t, "1\tAda\tadmin\n2\tGrace\n", listUsers(t, dir))
}

func TestDeleteUserAsksFirstAndAbortsOnAnythingButTheName(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "Hopper", false)

	out, err := runDeleteUser(t, dir, "2", "no\n", false)
	require.ErrorIs(t, err, errAborted)
	require.Contains(t, out, "Delete Grace Hopper (user 2). This can't be undone.")
	require.Contains(t, out, `Type the name "Grace Hopper" to delete this user for good: `)

	_, err = runDeleteUser(t, dir, "2", "", false) // closed stdin
	require.ErrorIs(t, err, errAborted)
	require.Equal(t, "1\tAda\tadmin\n2\tGrace Hopper\n", listUsers(t, dir))

	_, err = runDeleteUser(t, dir, "2", "Grace Hopper\n", false)
	require.NoError(t, err)
	require.Equal(t, "1\tAda\tadmin\n", listUsers(t, dir))
}

func TestDeleteUserPrintsEachChildLeftWithNoParent(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Dad", "", false)
	addUser(t, st, "Mom", "", false)
	addUser(t, st, "Emma", "Hollis", false)
	addUser(t, st, "Bob", "", false)
	exec(t, st, `INSERT INTO user_parents (child_id, parent_id) VALUES (4, 2), (5, 2), (5, 3)`)

	out, err := runDeleteUser(t, dir, "2", "", true)

	require.NoError(t, err)
	// Bob keeps Mom. Emma has nobody, and the admin can link a new parent.
	require.Contains(t, out, "Left without a parent")
	require.Contains(t, out, "the admin can link a new parent")
	require.Contains(t, out, "4\tEmma Hollis\n")
	require.NotContains(t, out, "5\tBob")
	// It is the last thing on the screen, so the operator sees it.
	require.True(t, strings.HasSuffix(strings.TrimSpace(out), "4\tEmma Hollis"), out)
}

func TestDeleteUserSaysNothingAboutParentsWhenNoChildIsLeftWithout(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	addUser(t, st, "Grace", "", false)

	out, err := runDeleteUser(t, dir, "2", "", true)

	require.NoError(t, err)
	require.NotContains(t, out, "without a parent")
}

func TestDeleteUserRefusesWhenAMigrationIsPending(t *testing.T) {
	dir, _ := newPendingDB(t)

	out, err := runDeleteUser(t, dir, "1", "", true)

	require.ErrorIs(t, err, store.ErrMigrationPending)
	require.ErrorContains(t, err, "start the server")
	require.Empty(t, out)
}

// The command uses the services that keep the open gifts right, as the server
// does: a user who leaves a shared list flags the open gifts there.
func TestDeleteUserFlagsAndSettlesTheOpenGiftsLikeTheServerDoes(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	dad := addUser(t, st, "Dad", "", false)
	mom := addUser(t, st, "Mom", "", false)
	sue := addUser(t, st, "Sue", "", false)
	exec(t, st, `INSERT INTO groups (id, name, created_at) VALUES (1, 'Family', ?)`, time.Now())
	exec(t, st, `INSERT INTO group_users (group_id, user_id) VALUES (1, ?), (1, ?), (1, ?)`, dad, mom, sue)
	exec(t, st, `INSERT INTO lists (id, name, created_at) VALUES (10, 'Our house', ?)`, time.Now())
	exec(t, st, `INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (10, ?, ?), (10, ?, ?)`, mom, time.Now(), dad, time.Now())
	exec(t, st, `INSERT INTO items (id, list_id, title, position, created_at) VALUES (20, 10, 'Grill', 0, ?)`, time.Now())
	dadsBirthday := count(t, st, `SELECT id FROM occasions WHERE user_id = ? AND kind = 'birthday'`, dad)
	exec(t, st, `INSERT INTO gifts (id, item_id, giver_id, quantity, occasion_id, occasion_date, created_at) VALUES (30, 20, ?, 1, ?, '2099-01-01', ?)`, sue, dadsBirthday, time.Now())

	_, err := runDeleteUser(t, dir, "2", "", true)

	require.NoError(t, err)
	require.Equal(t, 1, count(t, st, `SELECT count(*) FROM gifts WHERE id = 30`), "the gift stays, for Mom")
	flags, err := giftflag.Load(t.Context(), st.DB(), []int64{30})
	require.NoError(t, err)
	var codes []string
	for _, f := range flags[30] {
		codes = append(codes, f.Code)
	}
	require.ElementsMatch(t, []string{"recipients_changed", "occasion_moved"}, codes)
	for _, f := range flags[30] {
		if f.Code == "recipients_changed" {
			require.Equal(t, []int64{mom}, f.OldRecipients, "Dad is dropped")
			require.Equal(t, []int64{mom}, f.NewRecipients)
		}
	}
}

// The files of the images that go are removed after the commit, as the server's
// sweep would do on its next run.
func TestDeleteUserRemovesTheFilesOfTheImagesThatGo(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	grace := addUser(t, st, "Grace", "", false)
	files, err := imagestore.New(filepath.Join(dir, "images"), imagestore.Options{})
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	saved, err := files.Put(&buf)
	require.NoError(t, err)
	exec(t, st, `INSERT INTO images (id, key, width, height, uploaded_by, created_at) VALUES (1, ?, 8, 8, ?, ?)`, saved.Key, grace, time.Now())
	exec(t, st, `UPDATE users SET avatar_image_id = 1 WHERE id = ?`, grace)
	path := filepath.Join(dir, "images", saved.Key[:2], saved.Key+".jpg")
	require.FileExists(t, path)

	out, err := runDeleteUser(t, dir, "2", "", true)

	require.NoError(t, err)
	require.NoFileExists(t, path)
	require.Zero(t, count(t, st, `SELECT count(*) FROM images`))
	require.Contains(t, out, "Image files removed: 1\n")
}

func TestDeleteUserShowsWhatItWillDoBeforeItAsks(t *testing.T) {
	dir, st := newDB(t)
	addUser(t, st, "Ada", "", true)
	dad := addUser(t, st, "Dad", "", false)
	mom := addUser(t, st, "Mom", "", false)
	sue := addUser(t, st, "Sue", "", false)
	exec(t, st, `INSERT INTO lists (id, name, created_at) VALUES (10, 'Our house', ?), (11, 'Tools', ?)`, time.Now(), time.Now())
	exec(t, st, `INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (10, ?, ?), (10, ?, ?), (11, ?, ?)`, mom, time.Now(), dad, time.Now(), dad, time.Now())
	exec(t, st, `INSERT INTO items (id, list_id, title, position, created_at) VALUES (20, 11, 'Saw', 0, ?), (21, 10, 'Grill', 0, ?)`, time.Now(), time.Now())
	exec(t, st, `INSERT INTO gifts (item_id, giver_id, quantity, occasion_date, created_at) VALUES (20, ?, 1, '2099-01-01', ?), (21, ?, 1, '2099-01-01', ?)`, sue, time.Now(), dad, time.Now())

	out, err := runDeleteUser(t, dir, "2", "no\n", false)

	require.ErrorIs(t, err, errAborted)
	for _, line := range []string{
		"Delete Dad (user 2). This can't be undone.",
		"Lists Dad leaves, because another recipient stays: 1\n",
		"Lists deleted, because Dad is the only recipient (the primary list too): 2\n",
		"Open gifts that Dad gives, released: 1\n",
		"Open gifts of other givers on the deleted lists, deleted: 1\n",
		"Gifts Dad gave, kept in Received: 0\n",
		"Gifts given only to Dad, deleted: 0\n",
		"Sessions, login links and passkeys removed: 0, 0, 0\n",
	} {
		require.Contains(t, out, line)
	}
}
