package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/user"
)

// The size of the instance that BenchmarkVersions measures: a large family.
const (
	benchUsers        = 40
	benchExtraLists   = 6  // for each user, beside the primary list
	benchSharedLists  = 26 // extra lists with two recipients
	benchItemsPerList = 17
	benchClaimEvery   = 9 // one item in this many is claimed
)

// familySized seeds a family-sized instance: tens of users who all share one
// group, hundreds of lists, and thousands of items, each with a store link or
// two. One item in nine is claimed, the gifts whose occasion falls in the next
// two months are given, every user has a custom occasion, and some lists have a
// pending invitation. It returns the server and the session token of a user who
// sees every other user, gives gifts, and is a parent.
func familySized(b *testing.B) (*Server, string) {
	b.Helper()
	ctx := context.Background()
	opts, db, c := testOptions(b)
	must := func(err error) {
		b.Helper()
		require.NoError(b, err)
	}

	admin, err := opts.Users.Create(ctx, user.NewUser{Name: "Ada", Birthday: user.PartialDate{Month: 12, Day: 10}, IsAdmin: true})
	must(err)
	family, err := opts.Groups.Create(ctx, "Family")
	must(err)
	ids := make([]int64, benchUsers)
	for i := range ids {
		n := user.NewUser{Name: fmt.Sprintf("User %02d", i), Birthday: user.PartialDate{Month: 1 + i%12, Day: 1 + i%28}}
		if i >= 30 {
			n.ParentIDs = []int64{ids[i-30], ids[i-29]} // ten children, each with two parents
		}
		u, err := opts.Users.Create(ctx, n)
		must(err)
		ids[i] = u.ID
		must(opts.Groups.AddUser(ctx, family.ID, u.ID))
	}
	for g := range 4 {
		inner, err := opts.Groups.Create(ctx, fmt.Sprintf("Branch %d", g))
		must(err)
		must(opts.Groups.AddGroup(ctx, family.ID, inner.ID))
		for i := g; i < benchUsers; i += 4 {
			must(opts.Groups.AddUser(ctx, inner.ID, ids[i]))
		}
	}
	// Load the users again, so each one has their parents and children.
	users := make([]user.User, benchUsers)
	for i, id := range ids {
		users[i], err = opts.Users.Get(ctx, id)
		must(err)
	}

	type owned struct {
		id    int64
		owner int // index of a recipient
	}
	var lists []owned
	for i, u := range users {
		var primary int64
		must(db.GetContext(ctx, &primary, `SELECT id FROM lists WHERE primary_user_id = ?`, u.ID))
		lists = append(lists, owned{primary, i})
		for j := range benchExtraLists {
			l, err := opts.Lists.Create(ctx, u, list.New{Name: fmt.Sprintf("List %d of %s", j, u.Name)})
			must(err)
			lists = append(lists, owned{l.ID, i})
		}
		_, err := opts.Occasions.Create(ctx, u, occasion.New{Name: "Name day", Rule: occasion.Annual(time.Month(1+(i+5)%12), 1+i%28)})
		must(err)
	}
	for j := range benchSharedLists {
		a, other := j%benchUsers, (j+7)%benchUsers
		l, err := opts.Lists.Create(ctx, users[a], list.New{Name: fmt.Sprintf("Shared %d", j)})
		must(err)
		if j%2 == 0 {
			_, err = opts.Lists.Invite(ctx, users[a], l.ID, users[other].ID) // stays pending
		} else {
			_, err = db.ExecContext(ctx, `INSERT INTO list_recipients (list_id, user_id, created_at) VALUES (?, ?, ?)`, l.ID, users[other].ID, c.now())
		}
		must(err)
		lists = append(lists, owned{l.ID, a})
	}
	for j := range 5 {
		_, err := opts.Occasions.CreateShared(ctx, admin, occasion.NewShared{Name: fmt.Sprintf("Holiday %d", j), Rule: occasion.Annual(time.Month(2+2*j), 15), IsEveryone: true})
		must(err)
	}

	n := 0
	for _, l := range lists {
		for j := range benchItemsPerList {
			n++
			price := int64(500 + 37*n%20000)
			links := []item.NewStoreLink{{URL: fmt.Sprintf("https://shop.example/p/%d", n), Store: "shop.example", PriceMinor: &price, Currency: "USD"}}
			if n%3 == 0 {
				links = append(links, item.NewStoreLink{URL: fmt.Sprintf("https://other.example/p/%d", n), Store: "other.example"})
			}
			it, err := opts.Items.Add(ctx, users[l.owner], l.id, item.New{
				Title: fmt.Sprintf("Item %d", n), Notes: "Not the green one.", Quantity: 1 + j%3, StoreLinks: links,
			}, nil)
			must(err)
			if n%benchClaimEvery == 0 {
				giver := users[(l.owner+1+n%7)%benchUsers]
				if _, err := opts.Gifts.Claim(ctx, giver, it.ID, gift.New{}); err != nil {
					// A giver who is a recipient of a shared list can't claim there.
					require.ErrorIs(b, err, gift.ErrRecipientCannotClaim)
				}
			}
		}
	}
	// Two months on, the gifts for the occasions that passed are given.
	c.advance(60 * 24 * time.Hour)
	_, err = gift.NewRevealer(db, opts.Images, opts.Occasions, c.now).Run(ctx)
	must(err)

	token, _, err := session.Create(ctx, db, c.now(), users[1].ID, "bench")
	must(err)
	b.Logf("seeded: %s", counts(b, db))
	return New(opts), token
}

func counts(b *testing.B, db *sqlx.DB) string {
	b.Helper()
	out := ""
	for _, table := range []string{"users", "lists", "items", "store_links", "gifts", "occasions", "groups", "list_invitations"} {
		var n int
		require.NoError(b, db.Get(&n, `SELECT count(*) FROM `+table))
		out += fmt.Sprintf("%s %d, ", table, n)
	}
	return out
}

// BenchmarkVersions times one GET /versions on a family-sized instance, for one
// caller: the whole request, from the session cookie to the encoded answer. A 304
// builds every row as a 200 does, and writes no body.
//
//	go test ./internal/server -run '^$' -bench Versions -benchtime 20x
func BenchmarkVersions(b *testing.B) {
	srv, token := familySized(b)
	get := func(ifNoneMatch string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, apiPrefix+"/versions", nil)
		req.Header.Set("Cookie", sessionCookieName+"="+token)
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	first := get("")
	require.Equal(b, http.StatusOK, first.Code)
	etag := first.Header().Get("ETag")
	var answer versionsView
	require.NoError(b, json.Unmarshal(first.Body.Bytes(), &answer))
	resources := len(answer.Data)

	b.Run("200", func(b *testing.B) {
		for b.Loop() {
			require.Equal(b, http.StatusOK, get("").Code)
		}
		b.ReportMetric(float64(resources), "resources")
		b.ReportMetric(float64(first.Body.Len()), "bytes")
	})
	b.Run("304", func(b *testing.B) {
		for b.Loop() {
			require.Equal(b, http.StatusNotModified, get(etag).Code)
		}
	})
}
