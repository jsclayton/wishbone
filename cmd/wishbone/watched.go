package main

import (
	"time"

	"github.com/jmoiron/sqlx"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/user"
	"jsclayton.dev/wishbone/internal/userdelete"
)

// watchedDeleter returns the service that deletes a user, built on services that
// keep the open gifts right as the server's do (see gift.Service.Watch), so a
// user's delete flags, moves and releases gifts in its own transaction.
func watchedDeleter(db *sqlx.DB) *userdelete.Service {
	users := user.NewService(db, time.Now)
	lists := list.NewService(db, time.Now)
	items := item.NewService(db, time.Now)
	groups := group.NewService(db, time.Now)
	occasions := occasion.NewService(db, time.Now, occasion.WithFallbackZone(time.Local))
	gifts := gift.NewService(db, occasions, time.Now)
	gifts.Watch(lists, occasions, users, items, groups)
	return userdelete.New(db, lists, items, gifts, time.Now)
}

// watchedUsers returns a user service whose changes keep the open gifts right,
// as the server's do (see gift.Service.Watch). A command that changes what a user
// sees, such as set-admin, uses it so the change and the gifts commit together.
func watchedUsers(db *sqlx.DB) *user.Service {
	users := user.NewService(db, time.Now)
	lists := list.NewService(db, time.Now)
	items := item.NewService(db, time.Now)
	groups := group.NewService(db, time.Now)
	occasions := occasion.NewService(db, time.Now, occasion.WithFallbackZone(time.Local))
	gift.NewService(db, occasions, time.Now).Watch(lists, occasions, users, items, groups)
	return users
}
