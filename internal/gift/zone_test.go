package gift_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/giftflag"
	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

// These tests cover the zone of a user who has no timezone: the server's TZ,
// which cmd/wishbone gives to the occasion service once (docs/spec.md, Users).
// The gift service and the revealer both take it from there.

// auckland is 13 hours ahead of UTC in November, so a date has passed there
// hours before it has passed in UTC.
var auckland = mustZone("Pacific/Auckland")

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// noon is 12:00 UTC on 10 November: in Auckland it is 01:00 on the 11th.
var noon = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

type world struct {
	t         *testing.T
	db        *sqlx.DB
	users     *user.Service
	occasions *occasion.Service
	gifts     *gift.Service
	revealer  *gift.Revealer
	giver     user.User
	recipient user.User
	giftID    int64
}

// newWorld has a giver and a recipient who share a group and have no timezone,
// and the giver's open gift of the recipient's item for the recipient's birthday
// on 10 November. The clock reads now. fallback is the TZ of the server.
func newWorld(t *testing.T, now time.Time, fallback *time.Location) *world {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	db := st.DB()
	clock := func() time.Time { return now }

	files, err := imagestore.New(filepath.Join(t.TempDir(), "images"), imagestore.Options{})
	require.NoError(t, err)
	w := &world{t: t, db: db}
	w.users = user.NewService(db, clock)
	lists, items, groups := list.NewService(db, clock), item.NewService(db, clock), group.NewService(db, clock)
	w.occasions = occasion.NewService(db, clock, occasion.WithFallbackZone(fallback))
	w.gifts = gift.NewService(db, w.occasions, clock)
	w.gifts.Watch(lists, w.occasions, w.users, items, groups)
	w.revealer = gift.NewRevealer(db, image.NewService(db, files, clock), w.occasions, clock)

	nov10 := user.PartialDate{Month: 11, Day: 10}
	w.giver, err = w.users.Create(t.Context(), user.NewUser{Name: "Giver", Birthday: nov10})
	require.NoError(t, err)
	w.recipient, err = w.users.Create(t.Context(), user.NewUser{Name: "Recipient", Birthday: nov10})
	require.NoError(t, err)
	w.exec(`INSERT INTO groups (id, name, created_at) VALUES (1, 'Family', CURRENT_TIMESTAMP)`)
	w.exec(`INSERT INTO group_users (group_id, user_id) VALUES (1, ?), (1, ?)`, w.giver.ID, w.recipient.ID)
	w.exec(`INSERT INTO items (id, list_id, title, position, created_at)
		VALUES (1, (SELECT id FROM lists WHERE primary_user_id = ?), 'Kettle', 0, CURRENT_TIMESTAMP)`, w.recipient.ID)
	w.exec(`INSERT INTO gifts (item_id, giver_id, quantity, occasion_id, occasion_date, created_at)
		VALUES (1, ?, 1, (SELECT id FROM occasions WHERE kind = 'birthday' AND user_id = ?), '2026-11-10', CURRENT_TIMESTAMP)`,
		w.giver.ID, w.recipient.ID)
	require.NoError(t, db.GetContext(t.Context(), &w.giftID, `SELECT id FROM gifts`))
	return w
}

func (w *world) exec(q string, args ...any) {
	w.t.Helper()
	_, err := w.db.ExecContext(w.t.Context(), q, args...)
	require.NoError(w.t, err)
}

// occasionDateAndFlag returns the date the gift is for, and whether the gift is
// flagged as moved to another occasion.
func (w *world) occasionDateAndFlag() (string, bool) {
	w.t.Helper()
	var date string
	require.NoError(w.t, w.db.GetContext(w.t.Context(), &date, `SELECT occasion_date FROM gifts WHERE id = ?`, w.giftID))
	var flags int
	require.NoError(w.t, w.db.GetContext(w.t.Context(), &flags,
		`SELECT count(*) FROM gift_flags WHERE gift_id = ? AND code = ?`, w.giftID, giftflag.OccasionMoved))
	return date, flags == 1
}

// moveRecipientsBirthday changes the date of the occasion of the gift, which
// makes the gift service look at the gift again.
func (w *world) moveRecipientsBirthday() {
	w.t.Helper()
	_, err := w.users.Update(w.t.Context(), w.recipient.ID, user.Changes{
		Birthday: patch.Optional[user.PartialDate]{Set: true, Value: &user.PartialDate{Month: 12, Day: 25}},
	}, nil)
	require.NoError(w.t, err)
}

// A giver with no timezone is in the server's zone. Once the date of the gift's
// occasion has passed there, the gift waits for its reveal, and a change of the
// occasion's date leaves it alone, even though the date is still today in UTC.
func TestAGiftWhoseDatePassedInTheFallbackZoneWaitsForItsReveal(t *testing.T) {
	w := newWorld(t, noon, auckland)

	w.moveRecipientsBirthday()

	date, moved := w.occasionDateAndFlag()
	require.Equal(t, "2026-11-10", date, "the date has passed in Auckland, so the gift waits")
	require.False(t, moved)
}

// The same change with the fallback zone in UTC: the date has not passed, so the
// gift follows its occasion to the new date. This is what the test above would
// do if the giver's zone were not the fallback zone.
func TestAGiftWhoseDateHasNotPassedInTheFallbackZoneFollowsItsOccasion(t *testing.T) {
	w := newWorld(t, noon, time.UTC)

	w.moveRecipientsBirthday()

	date, moved := w.occasionDateAndFlag()
	require.Equal(t, "2026-12-25", date)
	require.True(t, moved)
}

// The reveal moment that a client reads and the moment the revealer gives the
// gift both come from the occasion service's fallback zone. The recipient has no
// timezone, and the date has passed at 01:00 in Auckland.
func TestTheRevealerGivesAGiftInTheZoneOfTheOccasionService(t *testing.T) {
	w := newWorld(t, noon, auckland)

	n, err := w.revealer.Run(t.Context())

	require.NoError(t, err)
	require.Equal(t, 1, n, "the reveal moment is 00:00 on the 11th in Auckland, which has passed")
	var givenAt *time.Time
	require.NoError(t, w.db.GetContext(t.Context(), &givenAt, `SELECT given_at FROM gifts WHERE id = ?`, w.giftID))
	require.NotNil(t, givenAt)
}

func TestTheRevealerWaitsInTheZoneOfTheOccasionService(t *testing.T) {
	w := newWorld(t, noon, time.UTC)

	n, err := w.revealer.Run(t.Context())

	require.NoError(t, err)
	require.Zero(t, n, "the reveal moment is 00:00 on the 11th in UTC, which has not passed")
}
