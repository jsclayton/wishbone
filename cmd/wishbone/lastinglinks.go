package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
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

// RevokeLastingLinkCmd revokes a lasting login link, and removes what it left
// that still signs someone in: the sessions that record it, and the passkeys
// and unused login links that those sessions made. It says what it removed, by
// user. The user's other sessions, and what they made, stay.
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

	revoked, err := loginlink.NewService(st.DB(), time.Now).RevokeLasting(ctx, id)
	if errors.Is(err, loginlink.ErrNotFound) {
		return fmt.Errorf("no lasting link with ID %d: 'wishbone lasting-links' lists them", id)
	}
	if err != nil {
		return err
	}
	users := user.NewService(st.DB(), time.Now)
	owner, err := users.Get(ctx, revoked.Link.UserID)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Revoked lasting link %d for %s.\n", revoked.Link.ID, displayName(owner))
	if len(revoked.Users) == 0 {
		b.WriteString("No session that it started was live, and its sessions made no passkey or login link that still worked.\n")
	} else {
		b.WriteString("It removed what it left that still signed someone in:\n")
	}
	var noPasskey []user.User
	for _, r := range revoked.Users {
		u, err := users.Get(ctx, r.UserID)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "  %s (%d): %s, %s, %s.\n", displayName(u), u.ID,
			counted(r.SessionsEnded, "session", "signed out"),
			deletedPasskeys(r.PasskeysDeleted),
			counted(r.LoginLinksEnded, "unused login link", "ended"))
		if len(r.PasskeysDeleted) > 0 && r.PasskeysLeft == 0 {
			noPasskey = append(noPasskey, u)
		}
	}
	for _, u := range noPasskey {
		fmt.Fprintf(&b, "%s has no passkey left. To sign %s in again, make a login link with 'wishbone login-link %d'.\n",
			displayName(u), u.Name, u.ID)
	}
	if revoked.Link.PredatesProvenance {
		b.WriteString(afterRevoke(owner))
	}
	_, err = io.WriteString(orStdout(c.out), b.String())
	return err
}

// counted is "n things done", with the noun in the plural unless n is 1.
func counted(n int, noun, done string) string {
	if n != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s %s", n, noun, done)
}

// deletedPasskeys is how many passkeys a revoke deleted, and their names. A
// name is the user's own text, so it is quoted, and a character that a terminal
// would act on is escaped.
func deletedPasskeys(names []string) string {
	s := counted(len(names), "passkey", "deleted")
	if len(names) == 0 {
		return s
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return s + " (" + strings.Join(quoted, ", ") + ")"
}

// afterRevoke says what a revoke of a link made before Wishbone recorded what
// a lasting link's sessions make can't find, and how the operator checks it. A
// passkey that one of its sessions added before then, or a login link that one
// of them made, records no lasting link, so both outlive the revoke
// (docs/spec.md, Access, Lasting login links). No command lists or removes
// one, so the operator checks in the app.
func afterRevoke(u user.User) string {
	name := u.Name
	whom, others := fmt.Sprintf("for %s or for one of %s's children", name, name), name+"'s children"
	if u.IsAdmin {
		whom, others = "for any user, because "+name+" is the admin", "every other user"
	}
	return fmt.Sprintf(`
This link is older than Wishbone's record of what a lasting link's sessions make.
So the revoke leaves what those sessions made before the upgrade, and it still works:
  - A passkey that one of them added for %[1]s. It works until it is deleted.
  - A login link that one of them made %[2]s. It works once, for up to 7 days, and the session it starts stays.
To check, sign in to Wishbone as the admin and open %[1]s's sign-in screen, /users/%[3]d/sign-in.
Delete each passkey and revoke each session that you don't know. Do the same for %[4]s.
No CLI command does this, and nothing lists a login link that isn't used yet, so look again in 7 days.
`, name, whom, u.ID, others)
}
