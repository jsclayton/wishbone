package userdelete

import (
	"context"
	"fmt"

	"jsclayton.dev/wishbone/internal/user"
)

// Plan counts what a delete would do, for the operator to read before they
// confirm. It changes nothing.
type Plan struct {
	// User is the user as they are.
	User user.User
	// Orphans are the children who would have no parent.
	Orphans []user.User
	// ListsLeft are the lists with another recipient, which the user leaves.
	ListsLeft int
	// ListsDeleted are the lists where the user is the only recipient, the primary
	// list too.
	ListsDeleted int
	// GiftsReleased are the open gifts that the user gives.
	GiftsReleased int
	// GiftsDeleted are the open gifts of other givers that only the user could
	// receive: the ones on the lists above, and on lists deleted earlier whose last
	// recipient they are.
	GiftsDeleted int
	// GivenKept are the given gifts the user gave. They stay in Received.
	GivenKept int
	// GivenDeleted are the given gifts that only the user received.
	GivenDeleted int
	// Sessions, LoginLinks and Passkeys are the rows that go with the user.
	Sessions, LoginLinks, Passkeys int
}

// Plan returns what Delete would do. It returns [ErrNotFound] and [ErrAdmin] as
// Delete does.
func (s *Service) Plan(ctx context.Context, id int64) (Plan, error) {
	u, err := user.Get(ctx, s.db, id)
	if err != nil {
		return Plan{}, err
	}
	if u.IsAdmin {
		return Plan{}, ErrAdmin
	}
	p := Plan{User: u}
	if p.Orphans, err = orphansOf(ctx, s.db, id); err != nil {
		return Plan{}, err
	}
	const soleRecipient = `SELECT lr.list_id FROM list_recipients lr WHERE lr.user_id = ?
		AND (SELECT count(*) FROM list_recipients o WHERE o.list_id = lr.list_id) = 1`
	for _, c := range []struct {
		into  *int
		query string
		args  []any
	}{
		{&p.ListsLeft, `SELECT count(*) FROM list_recipients lr WHERE lr.user_id = ?
			AND (SELECT count(*) FROM list_recipients o WHERE o.list_id = lr.list_id) > 1`, []any{id}},
		{&p.ListsDeleted, `SELECT count(*) FROM (` + soleRecipient + `)`, []any{id}},
		{&p.GiftsReleased, `SELECT count(*) FROM gifts WHERE giver_id = ? AND given_at IS NULL`, []any{id}},
		{&p.GiftsDeleted, `SELECT count(*) FROM gifts JOIN items ON items.id = gifts.item_id
			WHERE gifts.given_at IS NULL AND gifts.giver_id <> ? AND (
				items.list_id IN (` + soleRecipient + `)
				OR (items.list_id IN (SELECT list_id FROM kept_list_recipients WHERE user_id = ?)
					AND NOT EXISTS (SELECT 1 FROM kept_list_recipients k WHERE k.list_id = items.list_id
						AND k.user_id <> ? AND k.user_id <> gifts.giver_id)))`, []any{id, id, id, id}},
		{&p.GivenKept, `SELECT count(*) FROM gifts WHERE giver_id = ? AND given_at IS NOT NULL`, []any{id}},
		{&p.GivenDeleted, `SELECT count(*) FROM gifts WHERE given_at IS NOT NULL
			AND EXISTS (SELECT 1 FROM gift_recipients WHERE gift_id = gifts.id AND user_id = ?)
			AND NOT EXISTS (SELECT 1 FROM gift_recipients WHERE gift_id = gifts.id AND user_id <> ?)`, []any{id, id}},
		{&p.Sessions, `SELECT count(*) FROM sessions WHERE user_id = ?`, []any{id}},
		{&p.LoginLinks, `SELECT count(*) FROM login_links WHERE user_id = ?`, []any{id}},
		{&p.Passkeys, `SELECT count(*) FROM passkeys WHERE user_id = ?`, []any{id}},
	} {
		if err := s.db.GetContext(ctx, c.into, c.query, c.args...); err != nil {
			return Plan{}, fmt.Errorf("plan the delete of a user: %w", err)
		}
	}
	return p, nil
}
