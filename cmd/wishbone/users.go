package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"jsclayton.dev/wishbone/internal/config"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

// openMigrated opens the database for a command other than serve and backup.
// Only serve applies migrations, so this refuses a database that has any
// pending (docs/spec.md, Migration, Start order).
func openMigrated(ctx context.Context, dataDir string) (*store.Store, error) {
	dbPath := filepath.Join(dataDir, "wishbone.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no database at %s (start the server once first): %w", dbPath, err)
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	if err := st.EnsureMigrated(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func orStdout(w io.Writer) io.Writer {
	if w == nil {
		return os.Stdout
	}
	return w
}

// UsersCmd lists the users, so the operator can find an ID for the other
// commands. It prints no other profile fields.
type UsersCmd struct {
	DataDir string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`

	out io.Writer // stdout unless a test sets it
}

func (u *UsersCmd) Run() error {
	st, err := openMigrated(context.Background(), u.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	users, err := user.NewService(st.DB(), time.Now).List(context.Background())
	if err != nil {
		return err
	}
	out := orStdout(u.out)
	for _, usr := range users {
		line := fmt.Sprintf("%d\t%s", usr.ID, displayName(usr))
		if usr.IsAdmin {
			line += "\tadmin"
		}
		if !usr.Active() {
			line += "\tdeactivated"
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// displayName is a user's name as the CLI prints it: the name and the last name,
// each as [cliText] prints it.
func displayName(u user.User) string {
	if u.LastName != nil {
		return firstName(u) + " " + cliText(*u.LastName)
	}
	return firstName(u)
}

// firstName is a user's name without the last name, as [cliText] prints it.
func firstName(u user.User) string {
	return cliText(u.Name)
}

// cliText is text that a user wrote, such as a name, as the CLI prints it. Each
// control character is escaped as Go writes it in a string (\x1b, \n), and so
// is each byte that isn't UTF-8 (\x9b), so the text can't move the cursor,
// change colors, or do anything else a terminal does for an escape code.
// Wishbone refuses a control character in a name, but a row from before that
// rule can hold one (docs/spec.md, CLI). A passkey's name is escaped too, and
// quoted, with [strconv.Quote] (see [deletedPasskeys]).
func cliText(s string) string {
	if utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[0])
		case unicode.IsControl(r):
			q := strconv.QuoteRune(r) // '\x1b', with the quotes
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteString(s[:size])
		}
		s = s[size:]
	}
	return b.String()
}

// SetAdminCmd moves the admin role to another user. The API can't: a user's
// `is_admin` is read-only there. The old admin becomes a normal user and keeps
// their sessions.
type SetAdminCmd struct {
	DataDir string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`
	User    string `arg:"" help:"The ID of the user who becomes the admin. Run 'wishbone users' to find it."`

	out io.Writer // stdout unless a test sets it
}

func (a *SetAdminCmd) Run() error {
	id, err := strconv.ParseInt(a.User, 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("%q is not a user ID: this command takes an ID, and 'wishbone users' lists them", a.User)
	}
	st, err := openMigrated(context.Background(), a.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	target, previous, err := watchedUsers(st.DB()).SetAdmin(context.Background(), id)
	if errors.Is(err, user.ErrNotFound) {
		return fmt.Errorf("no user with ID %d: 'wishbone users' lists them", id)
	}
	if errors.Is(err, user.ErrHasParents) {
		// The admin can't have parents. The admin can unlink them in the app first.
		return fmt.Errorf("%s has parents, so can't be the admin: the admin removes their parents in the app first", displayName(target))
	}
	if errors.Is(err, user.ErrDeactivated) {
		// The admin reactivates the user in the app first.
		return fmt.Errorf("%s is deactivated, so can't be the admin: the admin reactivates them in the app first", displayName(target))
	}
	if err != nil {
		return err
	}
	out := orStdout(a.out)
	if previous == nil {
		_, err = fmt.Fprintf(out, "%s is already the admin.\n", displayName(target))
		return err
	}
	_, err = fmt.Fprintf(out, "%s is now the admin. %s is not the admin any more and keeps their sessions.\n",
		displayName(target), displayName(*previous))
	return err
}

// LoginLinkCmd makes a login link with the same code the server uses. It is the
// recovery path for everyone, the admin too. With --lasting it makes a lasting
// link, which only the CLI can make (docs/spec.md, Access, Lasting login links).
type LoginLinkCmd struct {
	config.Config `embed:""`
	User          string `arg:"" help:"The user's ID. Run 'wishbone users' to find it."`
	Lasting       bool   `name:"lasting" help:"Make a lasting link: it works any number of times and doesn't expire, until 'wishbone revoke-lasting-link' revokes it. For an App Review account."`

	out    io.Writer // stdout unless a test sets it
	stderr io.Writer // stderr unless a test sets it
}

func (l *LoginLinkCmd) Run() error {
	cfg := l.Config
	if err := cfg.Validate(); err != nil {
		return err
	}
	id, err := strconv.ParseInt(l.User, 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("%q is not a user ID: this command takes an ID, and 'wishbone users' lists them", l.User)
	}
	st, err := openMigrated(context.Background(), cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	target, err := user.NewService(st.DB(), time.Now).Get(context.Background(), id)
	if errors.Is(err, user.ErrNotFound) {
		return fmt.Errorf("no user with ID %d: 'wishbone users' lists them", id)
	}
	if err != nil {
		return err
	}
	if !target.Active() {
		// A deactivated user can't sign in, so a link would not work. The admin
		// reactivates them in the app first.
		return fmt.Errorf("%s is deactivated, so a login link would not work: the admin reactivates them in the app first", displayName(target))
	}
	links := loginlink.NewService(st.DB(), time.Now)
	var token, note string
	if l.Lasting {
		var link loginlink.Lasting
		if token, link, err = links.CreateLasting(context.Background(), id); err != nil {
			return err
		}
		note = fmt.Sprintf("Lasting link %d for %s. It works any number of times and doesn't expire. "+
			"'wishbone revoke-lasting-link %d' revokes it and signs out every session it started.\n",
			link.ID, displayName(target), link.ID)
	} else {
		if token, _, err = links.Create(context.Background(), id, 0); err != nil {
			return err
		}
		note = fmt.Sprintf("Expires in %d days.\n", int(loginlink.Lifetime/(24*time.Hour)))
	}
	if _, err := fmt.Fprintf(orStdout(l.out), "%s/login/%s\n", cfg.PublicURL, token); err != nil {
		return err
	}
	// The note goes to stderr so stdout stays a bare URL.
	_, err = fmt.Fprint(orStderr(l.stderr), note)
	return err
}

func orStderr(w io.Writer) io.Writer {
	if w == nil {
		return os.Stderr
	}
	return w
}
