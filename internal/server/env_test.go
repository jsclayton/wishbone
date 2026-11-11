package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/blocklist"
	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/setup"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/user"
)

// clock is a clock that a test moves.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// set moves the clock to a time.
func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t.UTC()
}

// testOptions are the options of a server on a new database, with the real
// services. A test changes what it needs and calls New.
func testOptions(t *testing.T) (Options, *sqlx.DB, *clock) {
	t.Helper()
	return testOptionsIn(t, time.UTC)
}

// testOptionsIn is testOptions with a fallback zone: the zone of a user who has
// no timezone.
func testOptionsIn(t *testing.T, fallback *time.Location) (Options, *sqlx.DB, *clock) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "wishbone.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, st.Migrate(t.Context()))
	c := &clock{t: time.Date(2026, time.November, 8, 12, 0, 0, 0, time.UTC)}
	passkeys, err := passkey.New(st.DB(), c.now, "https://wish.example")
	require.NoError(t, err)
	users, lists, items := user.NewService(st.DB(), c.now), list.NewService(st.DB(), c.now), item.NewService(st.DB(), c.now)
	occasions := occasion.NewService(st.DB(), c.now, occasion.WithFallbackZone(fallback))
	gifts := gift.NewService(st.DB(), occasions, c.now)
	groups := group.NewService(st.DB(), c.now)
	gifts.Watch(lists, occasions, users, items, groups)
	files, err := imagestore.New(filepath.Join(t.TempDir(), "images"), imagestore.Options{})
	require.NoError(t, err)
	return Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:      st,
		Version:    "test",
		Commit:     "abc123",
		Users:      users,
		Groups:     groups,
		Blocked:    blocklist.NewService(st.DB(), c.now),
		Lists:      lists,
		Items:      items,
		Occasions:  occasions,
		Gifts:      gifts,
		Sessions:   session.NewService(st.DB(), c.now),
		Setup:      setup.NewService(st.DB(), c.now),
		LoginLinks: loginlink.NewService(st.DB(), c.now),
		Passkeys:   passkeys,
		Images:     image.NewService(st.DB(), files, c.now),
		PublicURL:  "https://wish.example",
		Now:        c.now,
	}, st.DB(), c
}

// env is a running server on a new database that still needs setup.
type env struct {
	t        *testing.T
	srv      *Server
	ts       *httptest.Server
	db       *sqlx.DB
	clock    *clock
	sessions *session.Service
	images   *image.Service
	// revealer gives the gifts whose moment has passed, as the scheduler does.
	revealer *gift.Revealer
	// code is the setup code, as the server's log would show it.
	code string
}

func newEnv(t *testing.T, change ...func(*Options)) *env {
	t.Helper()
	return newEnvIn(t, time.UTC, change...)
}

// newEnvIn is newEnv with a fallback zone: the zone of a user who has no
// timezone, as the server's TZ is.
func newEnvIn(t *testing.T, fallback *time.Location, change ...func(*Options)) *env {
	t.Helper()
	opts, db, c := testOptionsIn(t, fallback)
	for _, f := range change {
		f(&opts)
	}
	code, err := opts.Setup.Prepare(t.Context())
	require.NoError(t, err)
	srv := New(opts)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &env{t: t, srv: srv, ts: ts, db: db, clock: c, sessions: opts.Sessions, images: opts.Images,
		revealer: gift.NewRevealer(db, opts.Images, opts.Occasions, c.now), code: code}
}

// request is one call to the API. Paths are under /api/v2.
type request struct {
	method, path string
	body         any
	header       http.Header
	// contentType replaces application/json on a request with a body.
	contentType string
	// raw is a body that is sent as it is, and not as JSON. contentType says
	// what it is.
	raw []byte
}

func (e *env) do(r request) *http.Response {
	e.t.Helper()
	var body io.Reader
	if r.body != nil {
		raw, err := json.Marshal(r.body)
		require.NoError(e.t, err)
		body = bytes.NewReader(raw)
	}
	if r.raw != nil {
		body = bytes.NewReader(r.raw)
	}
	if r.method == "" {
		r.method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(e.t.Context(), r.method, e.ts.URL+"/api/v2"+r.path, body)
	require.NoError(e.t, err)
	if r.body != nil || r.raw != nil {
		req.Header.Set("Content-Type", "application/json")
		if r.contentType != "" {
			req.Header.Set("Content-Type", r.contentType)
		}
	}
	for k, v := range r.header {
		req.Header[k] = v
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	e.t.Cleanup(func() { res.Body.Close() })
	return res
}

func cookie(token string) http.Header {
	return http.Header{"Cookie": {sessionCookieName + "=" + token}}
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func decode[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	var v T
	require.NoError(t, json.NewDecoder(res.Body).Decode(&v))
	return v
}

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return string(raw)
}

// sessionCookie returns the session cookie a response sets, or nil.
func sessionCookie(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

// ada is a valid setup request body.
func (e *env) ada() map[string]any {
	return map[string]any{
		"setup_code": e.code,
		"name":       "Ada",
		"birthday":   map[string]any{"month": 12, "day": 10, "year": nil},
	}
}

// signUp runs setup and returns the admin's id and the session cookie's token.
func (e *env) signUp() (id, token string) {
	e.t.Helper()
	res := e.do(request{method: http.MethodPost, path: "/setup", body: e.ada()})
	require.Equal(e.t, http.StatusCreated, res.StatusCode, "setup")
	c := sessionCookie(res)
	require.NotNil(e.t, c)
	u := decode[struct {
		ID string `json:"id"`
	}](e.t, res)
	return u.ID, c.Value
}

// otherUser adds a user straight to the database, with a session, and returns
// the id and the token. There is no route that creates a second user yet.
func (e *env) otherUser(name string) (id string, token string) {
	e.t.Helper()
	uid, err := user.Create(e.t.Context(), e.db, e.clock.now(), user.NewUser{Name: name, Birthday: user.PartialDate{Month: 1, Day: 1}})
	require.NoError(e.t, err)
	token, _, err = session.Create(e.t.Context(), e.db, e.clock.now(), uid, "")
	require.NoError(e.t, err)
	return strconv.FormatInt(uid, 10), token
}
