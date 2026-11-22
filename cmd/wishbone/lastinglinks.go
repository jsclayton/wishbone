package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/user"
)

// LastingLinksCmd lists the lasting login links that aren't revoked, so the
// operator can find one to revoke. The server keeps only a hash of each token,
// so it can't print the links themselves.
type LastingLinksCmd struct {
	DataDir string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`

	out io.Writer // stdout unless a test sets it
}

func (c *LastingLinksCmd) Run() error {
	ctx := context.Background()
	st, err := openMigrated(ctx, c.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	links, err := loginlink.NewService(st.DB(), time.Now).ListLasting(ctx)
	if err != nil {
		return err
	}
	users := user.NewService(st.DB(), time.Now)
	out := orStdout(c.out)
	for _, l := range links {
		u, err := users.Get(ctx, l.UserID)
		if err != nil {
			return err
		}
		used := "never used"
		if l.LastUsedAt != nil {
			used = "last used " + cliTime(*l.LastUsedAt)
		}
		line := fmt.Sprintf("%d\t%d\t%s\tmade %s\t%s", l.ID, u.ID, displayName(u), cliTime(l.CreatedAt), used)
		if !u.Active() {
			line += "\tdeactivated"
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

// cliTime is a time as the CLI prints it: in UTC, to the minute.
func cliTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// RevokeLastingLinkCmd revokes a lasting login link and ends every session it
// started. The user's other sessions stay.
type RevokeLastingLinkCmd struct {
	DataDir string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`
	Link    string `arg:"" help:"The ID of the lasting link. Run 'wishbone lasting-links' to find it."`

	out io.Writer // stdout unless a test sets it
}

func (c *RevokeLastingLinkCmd) Run() error {
	id, err := strconv.ParseInt(c.Link, 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("%q is not the ID of a lasting link: 'wishbone lasting-links' lists them", c.Link)
	}
	ctx := context.Background()
	st, err := openMigrated(ctx, c.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	link, ended, err := loginlink.NewService(st.DB(), time.Now).RevokeLasting(ctx, id)
	if errors.Is(err, loginlink.ErrNotFound) {
		return fmt.Errorf("no lasting link with ID %d: 'wishbone lasting-links' lists them", id)
	}
	if err != nil {
		return err
	}
	u, err := user.NewService(st.DB(), time.Now).Get(ctx, link.UserID)
	if err != nil {
		return err
	}
	sessions := "sessions"
	if ended == 1 {
		sessions = "session"
	}
	out := orStdout(c.out)
	if _, err := fmt.Fprintf(out, "Revoked lasting link %d for %s, and signed out the %d %s it started.\n",
		link.ID, displayName(u), ended, sessions); err != nil {
		return err
	}
	_, err = io.WriteString(out, afterRevoke(u))
	return err
}

// afterRevoke says what a revoke leaves, and how the operator checks it. A
// session that the link started can add a passkey, or make a login link, and
// neither belongs to the link, so both outlive the revoke (docs/spec.md,
// Access, Lasting login links). The server can't tell which session made a
// passkey or a login link, and no command lists or removes one, so the
// operator checks in the app.
func afterRevoke(u user.User) string {
	name := u.Name
	whom, others := fmt.Sprintf("for %s or for one of %s's children", name, name), name+"'s children"
	if u.IsAdmin {
		whom, others = "for any user, because "+name+" is the admin", "every other user"
	}
	return fmt.Sprintf(`
The revoke leaves what those sessions made, and it still works:
  - A passkey that one of them added for %[1]s. It works until it is deleted.
  - A login link that one of them made %[2]s. It works once, for up to 7 days, and the session it starts stays.
To check, sign in to Wishbone as the admin and open %[1]s's sign-in screen, /users/%[3]d/sign-in.
Delete each passkey and revoke each session that you don't know. Do the same for %[4]s.
No CLI command does this, and nothing lists a login link that isn't used yet, so look again in 7 days.
`, name, whom, u.ID, others)
}
