package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/user"
	"jsclayton.dev/wishbone/internal/userdelete"
)

// DeleteUserCmd hard-deletes a user. It is the only way to do it: the web app can
// only deactivate. The rules are in docs/spec.md, Deactivation and deletion,
// Deletion. It needs only the data dir.
type DeleteUserCmd struct {
	DataDir string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`
	User    string `arg:"" help:"The ID of the user to delete. Run 'wishbone users' to find it."`
	Yes     bool   `name:"yes" short:"y" help:"Don't ask for confirmation."`

	in  io.Reader // stdin unless a test sets it
	out io.Writer // stdout unless a test sets it
}

var errAborted = errors.New("aborted: nothing was deleted")

func (d *DeleteUserCmd) Run() error {
	ctx := context.Background()
	out := orStdout(d.out)
	in := d.in
	if in == nil {
		in = os.Stdin
	}
	id, err := strconv.ParseInt(d.User, 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("%q is not a user ID: this command takes an ID, and 'wishbone users' lists them", d.User)
	}
	st, err := openMigrated(ctx, d.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	svc := watchedDeleter(st.DB())
	plan, err := svc.Plan(ctx, id)
	if errors.Is(err, user.ErrNotFound) {
		return fmt.Errorf("no user with ID %d: 'wishbone users' lists them", id)
	}
	if errors.Is(err, userdelete.ErrAdmin) {
		// The instance always has an admin, so setup can't run again.
		u, getErr := user.NewService(st.DB(), time.Now).Get(ctx, id)
		if getErr != nil {
			return getErr
		}
		return fmt.Errorf("%s is the admin, so can't be deleted: move the role to another user with 'wishbone set-admin' first", displayName(u))
	}
	if err != nil {
		return err
	}
	name := displayName(plan.User)
	printPlan(out, name, plan)

	if !d.Yes {
		if _, err := fmt.Fprintf(out, "\nType the name %q to delete this user for good: ", name); err != nil {
			return err
		}
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(answer) != name {
			return errAborted
		}
	}

	res, err := svc.Delete(ctx, id)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "\nDeleted %s.\n", displayName(res.User)); err != nil {
		return err
	}
	removed, cleanErr := removeImageFiles(ctx, st.DB(), d.DataDir)
	if cleanErr == nil {
		if _, err := fmt.Fprintf(out, "Image files removed: %d\n", removed); err != nil {
			return err
		}
	}
	// What needs fixing comes last, so it is the last thing on screen.
	if err := printOrphans(out, res.Orphans); err != nil {
		return err
	}
	if cleanErr != nil {
		return fmt.Errorf("user deleted, but removing the image files failed: %w", cleanErr)
	}
	return nil
}

func printPlan(w io.Writer, name string, p userdelete.Plan) {
	pf := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }
	pf("Delete %s (user %d). This can't be undone.\n\n", name, p.User.ID)
	pf("  Lists %s leaves, because another recipient stays: %d\n", p.User.Name, p.ListsLeft)
	pf("  Lists deleted, because %s is the only recipient (the primary list too): %d\n", p.User.Name, p.ListsDeleted)
	pf("  Open gifts that %s gives, released: %d\n", p.User.Name, p.GiftsReleased)
	pf("  Open gifts of other givers on the deleted lists, deleted: %d\n", p.GiftsDeleted)
	pf("  Gifts %s gave, kept in Received: %d\n", p.User.Name, p.GivenKept)
	pf("  Gifts given only to %s, deleted: %d\n", p.User.Name, p.GivenDeleted)
	pf("  Sessions, login links and passkeys removed: %d, %d, %d\n", p.Sessions, p.LoginLinks, p.Passkeys)
	if len(p.Orphans) > 0 {
		pf("  Children left without a parent: %d\n", len(p.Orphans))
	}
}

// printOrphans lists the children who have no parent after the delete, one line
// each with the ID and the name. A child with no parent is a valid state, but
// nobody edits their lists until the admin links a new parent.
func printOrphans(w io.Writer, orphans []user.User) error {
	if len(orphans) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "Left without a parent (the admin can link a new parent in the app):\n"); err != nil {
		return err
	}
	for _, c := range orphans {
		if _, err := fmt.Fprintf(w, "%d\t%s\n", c.ID, displayName(c)); err != nil {
			return err
		}
	}
	return nil
}

// removeImageFiles runs after the commit, with the sweep the server runs: it
// deletes the images that nothing holds any more, and their files. A failure here
// leaves the user deleted, so it is reported and not undone: the server's own
// sweep repeats the step.
func removeImageFiles(ctx context.Context, db *sqlx.DB, dataDir string) (int, error) {
	files, err := imagestore.New(filepath.Join(dataDir, "images"), imagestore.Options{})
	if err != nil {
		return 0, fmt.Errorf("open the image store: %w", err)
	}
	return image.NewService(db, files, time.Now).Sweep(ctx)
}
