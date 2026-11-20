package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/extract/llm"
	"jsclayton.dev/wishbone/internal/extract/pipeline"
	"jsclayton.dev/wishbone/internal/session"
)

// keyed is the sign-in of a write that carries an Idempotency-Key.
func keyed(token, key string) http.Header {
	h := cookie(token)
	h.Set("Idempotency-Key", key)
	return h
}

// answer is what a client gets back from a write, in the parts that a repeat
// with the same key returns again.
type answer struct {
	status                      int
	location, etag, contentType string
	body                        string
}

func answerOf(t *testing.T, res *http.Response) answer {
	t.Helper()
	return answer{
		status:      res.StatusCode,
		location:    res.Header.Get("Location"),
		etag:        res.Header.Get("ETag"),
		contentType: res.Header.Get("Content-Type"),
		body:        readBody(t, res),
	}
}

// storedKeys is how many answers the server keeps for repeats.
func (e *env) storedKeys() int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.db.GetContext(e.t.Context(), &n, `SELECT count(*) FROM idempotency_keys`))
	return n
}

func (e *env) postItemWithKey(token, listID, key string, body map[string]any) *http.Response {
	e.t.Helper()
	return e.do(request{method: http.MethodPost, path: "/lists/" + listID + "/items", body: body, header: keyed(token, key)})
}

func TestARepeatedCreateOfAnItemWithTheSameKeyMakesOneItemAndReturnsTheFirstAnswer(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()

	first := answerOf(t, e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"}))
	again := answerOf(t, e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"}))

	require.Equal(t, http.StatusCreated, first.status)
	require.NotEmpty(t, first.location)
	require.NotEmpty(t, first.etag)
	require.Contains(t, first.contentType, "application/json")
	require.Equal(t, first, again)
	require.Equal(t, []string{"Kettle"}, e.order(grace, listID), "the repeat made no second item")
}

func TestWithoutTheKeyARepeatedCreateStillMakesASecondItem(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()

	e.addItem(grace, listID, "Kettle")
	e.addItem(grace, listID, "Kettle")

	require.Equal(t, []string{"Kettle", "Kettle"}, e.order(grace, listID))
	require.Zero(t, e.storedKeys())
}

func TestARepeatedCreateOfAListWithTheSameKeyMakesOneList(t *testing.T) {
	e := newEnv(t)
	grace, _ := e.grace()
	post := func() answer {
		return answerOf(t, e.do(request{method: http.MethodPost, path: "/lists", body: map[string]any{"name": "Camping"}, header: keyed(grace, "key-1")}))
	}

	first := post()
	again := post()

	require.Equal(t, http.StatusCreated, first.status)
	require.NotEmpty(t, first.location)
	require.Equal(t, first, again)
	require.Equal(t, 2, decode[listList](t, e.listLists(grace, "")).TotalCount, "the primary list and one extra list")
}

func TestARepeatedUploadWithTheSameKeyMakesOneImageAndTheUploadIsNotKept(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	png := pngOf(t, 4, 3)
	upload := func() answer {
		return answerOf(t, e.do(request{method: http.MethodPost, path: "/images", raw: png, contentType: "image/png", header: keyed(admin, "key-1")}))
	}

	first := upload()
	again := upload()

	require.Equal(t, http.StatusCreated, first.status)
	require.NotEmpty(t, first.location)
	require.Equal(t, first, again)
	var images int
	require.NoError(t, e.db.GetContext(t.Context(), &images, `SELECT count(*) FROM images`))
	require.Equal(t, 1, images)

	// What the server keeps is the answer, and a hash of the request.
	var kept struct {
		Hash string `db:"request_hash"`
		Body []byte `db:"body"`
	}
	require.NoError(t, e.db.GetContext(t.Context(), &kept, `SELECT request_hash, body FROM idempotency_keys`))
	require.Equal(t, first.body, string(kept.Body))
	require.Len(t, kept.Hash, 64)
	var holdsUpload int
	require.NoError(t, e.db.GetContext(t.Context(), &holdsUpload,
		`SELECT count(*) FROM idempotency_keys WHERE instr(body, ?) > 0 OR instr(headers, ?) > 0 OR instr(key, ?) > 0`, png, png, png))
	require.Zero(t, holdsUpload)
}

func TestARepeatedPatchWithTheSameKeyReturnsTheFirstAnswerAndChangesNothingAgain(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()
	item := e.addItem(grace, listID, "Kettle")
	patch := func() answer {
		return answerOf(t, e.patchItem(grace, item.ID, map[string]any{"title": "Teapot"}, http.Header{"Idempotency-Key": {"key-1"}}))
	}

	first := patch()
	// Something else changes the item between the first request and the repeat.
	require.Equal(t, http.StatusOK, e.patchItem(grace, item.ID, map[string]any{"title": "Mug"}, nil).StatusCode)
	again := patch()

	require.Equal(t, http.StatusOK, first.status)
	require.NotEmpty(t, first.etag)
	require.Contains(t, first.body, "Teapot")
	require.Equal(t, first, again)
	require.Equal(t, []string{"Mug"}, e.order(grace, listID), "the repeat did not run the change again")
}

func TestARepeatedDeleteWithTheSameKeyIs204AgainAndNot404(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()
	item := e.addItem(grace, listID, "Kettle")
	key := http.Header{"Idempotency-Key": {"key-1"}}

	first := e.deleteItem(grace, item.ID, key)
	again := e.deleteItem(grace, item.ID, key)

	require.Equal(t, http.StatusNoContent, first.StatusCode)
	require.Equal(t, http.StatusNoContent, again.StatusCode)
	require.Empty(t, readBody(t, again))
	// Without the key, the item is gone as before.
	require.Equal(t, http.StatusNotFound, e.deleteItem(grace, item.ID, nil).StatusCode)
}

func TestTheSameKeyWithAnotherBodyMethodOrPathIsA422AndChangesNothing(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()
	other := e.extraListOf(grace, "Camping")
	first := decode[itemView](t, e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"}))

	for name, res := range map[string]*http.Response{
		"another body":   e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Teapot"}),
		"another path":   e.postItemWithKey(grace, other, "key-1", map[string]any{"title": "Kettle"}),
		"another method": e.patchItem(grace, first.ID, map[string]any{"title": "Teapot"}, http.Header{"Idempotency-Key": {"key-1"}}),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
			require.Equal(t, "idempotency_key_reused", readProblem(t, res).Code)
		})
	}

	require.Equal(t, []string{"Kettle"}, e.order(grace, listID))
	require.Empty(t, e.order(grace, other))
	// The key still answers its own request.
	require.Equal(t, http.StatusCreated, e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"}).StatusCode)
	require.Equal(t, []string{"Kettle"}, e.order(grace, listID))
}

// extraListOf makes an extra list for the user whose session is token.
func (e *env) extraListOf(token, name string) string {
	e.t.Helper()
	res := e.do(request{method: http.MethodPost, path: "/lists", body: map[string]any{"name": name}, header: cookie(token)})
	require.Equal(e.t, http.StatusCreated, res.StatusCode)
	return decode[listView](e.t, res).ID
}

// A client can build the body again for a repeat. The same JSON with its keys in
// another order, or a form with another boundary, is the same body.
func TestTheSameJSONInAnotherOrderIsTheSameBody(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()
	post := func(raw string) answer {
		return answerOf(t, e.do(request{method: http.MethodPost, path: "/lists/" + listID + "/items", raw: []byte(raw), header: keyed(grace, "key-1")}))
	}

	first := post(`{"title":"Kettle","notes":"The red one"}`)
	again := post(`{ "notes": "The red one", "title": "Kettle" }`)

	require.Equal(t, http.StatusCreated, first.status)
	require.Equal(t, first, again)
	require.Equal(t, []string{"Kettle"}, e.order(grace, listID))
}

func TestTheSameKeyFromAnotherSessionDoesItsOwnWork(t *testing.T) {
	e := newEnv(t)
	graceID, grace := e.otherUser("Grace")
	listID := e.primaryListOf(grace).ID
	// A second session of the same user, and a session of another user.
	uid, ok := parseID(graceID)
	require.True(t, ok)
	second, _, err := session.Create(t.Context(), e.db, e.clock.now(), uid, "")
	require.NoError(t, err)
	_, sue := e.otherUser("Sue")

	first := decode[itemView](t, e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"}))
	other := decode[itemView](t, e.postItemWithKey(second, listID, "key-1", map[string]any{"title": "Kettle"}))
	theirs := e.postItemWithKey(sue, e.primaryListOf(sue).ID, "key-1", map[string]any{"title": "Skates"})

	require.NotEqual(t, first.ID, other.ID)
	require.Equal(t, []string{"Kettle", "Kettle"}, e.order(grace, listID))
	require.Equal(t, http.StatusCreated, theirs.StatusCode, "another session's key is no reuse of this one")
	require.Equal(t, "Skates", decode[itemView](t, theirs).Title)
}

// slowSearch is a search that waits in the middle of reading a link or a photo.
type slowSearch struct {
	started chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func newSlowSearch() *slowSearch {
	return &slowSearch{started: make(chan struct{}, 1), release: make(chan struct{})}
}

func (s *slowSearch) wait() {
	s.reads.Add(1)
	s.started <- struct{}{}
	<-s.release
}

func (s *slowSearch) Extract(_ context.Context, _ int64, rawURL string, _ ...pipeline.Option) (pipeline.Result, error) {
	s.wait()
	return pipeline.Result{URL: rawURL}, nil
}

func (s *slowSearch) PhotoLimitReached(int64) bool { return false }

func (s *slowSearch) GuessPhoto(context.Context, int64, []byte) (llm.PhotoGuess, error) {
	s.wait()
	return llm.PhotoGuess{Title: "Kettle"}, nil
}

// The repeat is for the client that never got the answer. So the answer is
// kept when the work is done, also when the client has gone by then.
func TestAnAnswerThatTheClientNeverGotIsStillKeptForTheRepeat(t *testing.T) {
	slow := newSlowSearch()
	e := newEnv(t, func(o *Options) { o.Search = slow })
	release := sync.OnceFunc(func() { close(slow.release) })
	t.Cleanup(release)
	_, admin := e.signUp()
	body, contentType := form(t, nil, map[string][]byte{"image": pngOf(t, 4, 3)})
	ctx, hangUp := context.WithCancel(t.Context())
	defer hangUp()
	gone := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ts.URL+"/api/v2/search", bytes.NewReader(body))
		if err != nil {
			gone <- err
			return
		}
		req.Header = keyed(admin, "key-1")
		req.Header.Set("Content-Type", contentType)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			res.Body.Close()
		}
		gone <- err
	}()
	<-slow.started

	// The client gives up while the server is still at work.
	hangUp()
	require.ErrorIs(t, <-gone, context.Canceled)
	release()
	require.Eventually(t, func() bool { return e.storedKeys() == 1 }, 10*time.Second, 5*time.Millisecond)

	again := e.do(request{method: http.MethodPost, path: "/search", raw: body, contentType: contentType, header: keyed(admin, "key-1")})

	title := only(t, again).Title
	require.NotNil(t, title)
	require.Equal(t, "Kettle", *title)
	require.Equal(t, int32(1), slow.reads.Load(), "the photo was read once")
}

func TestARepeatWhileTheFirstIsStillRunningIsA409(t *testing.T) {
	slow := newSlowSearch()
	e := newEnv(t, func(o *Options) { o.Search = slow })
	// A failed test must still let the searches end, or the server never closes.
	release := sync.OnceFunc(func() { close(slow.release) })
	t.Cleanup(release)
	_, admin := e.signUp()
	body, contentType := form(t, map[string]string{"text": "https://shop.example/kettle"}, nil)
	// A server that runs the repeat too would wait for the release. The deadline
	// fails the test then, in place of a hang.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	search := func(key string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.ts.URL+"/api/v2/search", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header = keyed(admin, key)
		req.Header.Set("Content-Type", contentType)
		return http.DefaultClient.Do(req)
	}
	type result struct {
		res *http.Response
		err error
	}
	firstDone := make(chan result, 1)
	go func() {
		res, err := search("key-1")
		firstDone <- result{res, err}
	}()
	<-slow.started

	during, err := search("key-1")
	require.NoError(t, err)
	t.Cleanup(func() { during.Body.Close() })
	require.Equal(t, http.StatusConflict, during.StatusCode)
	require.Equal(t, "idempotency_key_in_use", readProblem(t, during).Code)

	// The same key with another body is refused as a reuse, also while the first runs.
	otherBody, otherType := form(t, map[string]string{"text": "https://shop.example/teapot"}, nil)
	reused := e.do(request{method: http.MethodPost, path: "/search", raw: otherBody, contentType: otherType, header: keyed(admin, "key-1")})
	require.Equal(t, http.StatusUnprocessableEntity, reused.StatusCode)
	require.Equal(t, "idempotency_key_reused", readProblem(t, reused).Code)

	release()
	first := <-firstDone
	require.NoError(t, first.err)
	t.Cleanup(func() { first.res.Body.Close() })
	require.Equal(t, http.StatusOK, first.res.StatusCode)
	firstBody := readBody(t, first.res)

	// Once the first is done, the repeat gets its answer.
	after, err := search("key-1")
	require.NoError(t, err)
	t.Cleanup(func() { after.Body.Close() })
	require.Equal(t, http.StatusOK, after.StatusCode)
	require.Equal(t, firstBody, readBody(t, after))
	require.Equal(t, int32(1), slow.reads.Load(), "the link was read once")
}

func TestAfter24HoursTheKeyIsForgotten(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()
	post := func() int {
		return e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"}).StatusCode
	}
	require.Equal(t, http.StatusCreated, post())

	e.clock.advance(24*time.Hour - time.Second)
	require.Equal(t, http.StatusCreated, post())
	require.Equal(t, []string{"Kettle"}, e.order(grace, listID), "inside the window, the repeat gets the first answer")

	// The window starts at the first request. A repeat does not move it.
	e.clock.advance(time.Second)
	require.Equal(t, http.StatusCreated, post())
	require.Equal(t, []string{"Kettle", "Kettle"}, e.order(grace, listID), "after the window, the key is new again")

	// A forgotten key takes another body.
	e.clock.advance(24 * time.Hour)
	res := e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Teapot"})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, 1, e.storedKeys(), "the old answer is gone")
}

func TestRevokingASessionRemovesItsKeys(t *testing.T) {
	e := newEnv(t)
	id, admin := e.signUp()
	bearerToken := decode[redeemBody](t, e.redeem(e.makeLink(admin, id), "bearer", nil)).Token
	listID := e.primaryListOf(admin).ID
	post := func(h http.Header) {
		h.Set("Idempotency-Key", "key-1")
		res := e.do(request{method: http.MethodPost, path: "/lists/" + listID + "/items", body: map[string]any{"title": "Kettle"}, header: h})
		require.Equal(t, http.StatusCreated, res.StatusCode)
	}
	post(cookie(admin))
	post(bearer(bearerToken))
	require.Equal(t, 2, e.storedKeys())
	var target string
	for _, row := range decode[sessionList](t, e.listSessions(id, cookie(admin))).Data {
		if !row.IsCurrent {
			target = row.ID
		}
	}

	res := e.do(request{method: http.MethodDelete, path: "/users/" + id + "/sessions/" + target, header: cookie(admin)})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	var sessions []string
	require.NoError(t, e.db.SelectContext(t.Context(), &sessions, `SELECT CAST(session_id AS TEXT) FROM idempotency_keys`))
	require.Len(t, sessions, 1, "the other session keeps its key")
	require.NotEqual(t, target, sessions[0])
}

// A sign-out with a key keeps nothing: the answer would belong to a session
// that is over.
func TestASignOutWithAKeyKeepsNoKeyOfTheSession(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	listID := e.primaryListOf(admin).ID
	require.Equal(t, http.StatusCreated, e.postItemWithKey(admin, listID, "key-1", map[string]any{"title": "Kettle"}).StatusCode)

	res := e.do(request{method: http.MethodDelete, path: "/users/me/sessions/current", header: keyed(admin, "key-2")})

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Zero(t, e.storedKeys())
}

func TestDeactivatingAUserRemovesTheKeysOfTheirSessions(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	graceID, grace := e.otherUser("Grace")
	require.Equal(t, http.StatusCreated, e.postItemWithKey(grace, e.primaryListOf(grace).ID, "key-1", map[string]any{"title": "Kettle"}).StatusCode)
	require.Equal(t, 1, e.storedKeys())

	res := e.do(request{method: http.MethodPost, path: "/users/" + graceID + "/deactivate", header: cookie(admin)})

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Zero(t, e.storedKeys())
}

func TestAReplayedSearchDoesNotCountAgainstTheHourlyLimit(t *testing.T) {
	eyes := &fakeEyes{guess: llm.PhotoGuess{Title: "Kettle"}}
	e := newEnv(t, withSearch(&fakeShop{pages: anyProduct}, eyes))
	_, admin := e.signUp()
	photo := pngOf(t, 4, 3)
	// Each call builds the form again, with a boundary of its own.
	keyedPhoto := func() *http.Response {
		body, contentType := form(t, nil, map[string][]byte{"image": photo})
		return e.do(request{method: http.MethodPost, path: "/search", raw: body, contentType: contentType, header: keyed(admin, "key-1")})
	}

	first := answerOf(t, keyedPhoto())
	require.Equal(t, http.StatusOK, first.status)
	for range 40 {
		require.Equal(t, first, answerOf(t, keyedPhoto()))
	}

	// The photo counted once: 29 more fit in the hour, and the one after does not.
	for i := range 29 {
		require.Equal(t, http.StatusOK, e.searchPhoto(admin, photo).StatusCode, i)
	}
	over := e.searchPhoto(admin, photo)
	require.Equal(t, http.StatusTooManyRequests, over.StatusCode)
	require.Equal(t, "rate_limited", readProblem(t, over).Code)
	require.Len(t, eyes.seen, 30)
	// Over the limit, the key still gets its first answer.
	require.Equal(t, first, answerOf(t, keyedPhoto()))
}

// Only an answer that did the work is kept. A request that failed runs again.
func TestARequestThatFailedKeepsNothingSoTheKeyRunsAgain(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()

	bad := e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": ""})
	require.Equal(t, http.StatusUnprocessableEntity, bad.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, bad).Code)
	require.Zero(t, e.storedKeys())

	// The client corrects the body and sends it with the same key.
	res := e.postItemWithKey(grace, listID, "key-1", map[string]any{"title": "Kettle"})
	require.Equal(t, http.StatusCreated, res.StatusCode)
	require.Equal(t, []string{"Kettle"}, e.order(grace, listID))
}

func TestAKeyThatIsTooLongIsA422(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()

	res := e.postItemWithKey(grace, listID, strings.Repeat("k", 256), map[string]any{"title": "Kettle"})

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "invalid_input", readProblem(t, res).Code)
	require.Empty(t, e.order(grace, listID))
	require.Equal(t, http.StatusCreated, e.postItemWithKey(grace, listID, strings.Repeat("k", 255), map[string]any{"title": "Kettle"}).StatusCode)
}

// A header with no value is no key.
func TestAnEmptyKeyIsNoKey(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()

	for range 2 {
		require.Equal(t, http.StatusCreated, e.postItemWithKey(grace, listID, "", map[string]any{"title": "Kettle"}).StatusCode)
	}

	require.Equal(t, []string{"Kettle", "Kettle"}, e.order(grace, listID))
	require.Zero(t, e.storedKeys())
}

// The link is in its first answer and nowhere else: the server keeps only a
// hash of its token. So the answer is not kept for a repeat either.
func TestALoginLinkIsNeverKeptSoARepeatMakesANewLink(t *testing.T) {
	e := newEnv(t)
	id, admin := e.signUp()
	create := func() linkBody {
		res := e.do(request{method: http.MethodPost, path: "/users/" + id + "/login-links", header: keyed(admin, "key-1")})
		require.Equal(t, http.StatusCreated, res.StatusCode)
		return decode[linkBody](t, res)
	}

	first := create()
	again := create()

	require.NotEqual(t, first.URL, again.URL)
	require.Zero(t, e.storedKeys())
}

// The options of a passkey registration hold a challenge that lasts 5 minutes.
// A kept copy would be over for most of the 24 hours, so a repeat starts a new
// registration.
func TestPasskeyRegistrationOptionsAreNeverKeptSoARepeatStartsANewRegistration(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	begin := func() string {
		res := e.do(request{method: http.MethodPost, path: "/users/me/passkeys/registration", header: keyed(admin, "key-1")})
		require.Equal(t, http.StatusOK, res.StatusCode)
		var options struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		}
		require.NoError(t, json.Unmarshal([]byte(readBody(t, res)), &options))
		require.NotEmpty(t, options.PublicKey.Challenge)
		return options.PublicKey.Challenge
	}

	first := begin()
	again := begin()

	require.NotEqual(t, first, again, "the repeat got a challenge of its own")
	require.Zero(t, e.storedKeys())
}

// A key belongs to a session, so a route that needs no sign-in takes none, and
// a read takes none.
func TestARouteWithNoSignInAndAReadIgnoreTheKey(t *testing.T) {
	e := newEnv(t)
	id, admin := e.signUp()
	link := e.makeLink(admin, id)
	check := func(token string) *http.Response {
		return e.do(request{method: http.MethodPost, path: "/login-links/check", body: map[string]any{"token": token}, header: keyed(admin, "key-1")})
	}

	require.Equal(t, http.StatusOK, check(link).StatusCode)
	// The same key with another body is no reuse, because no key was kept.
	require.Equal(t, http.StatusUnauthorized, check("not-a-link").StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: keyed(admin, "key-1")}).StatusCode)
	require.Equal(t, http.StatusOK, e.do(request{path: "/lists", header: keyed(admin, "key-1")}).StatusCode)
	require.Zero(t, e.storedKeys())
}

// A body over the route's limit is refused as before, whether it has a key or not.
func TestAnUploadOverTheLimitWithAKeyIsStillA413(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.do(request{method: http.MethodPost, path: "/images", raw: make([]byte, imageTooLargeLimit), contentType: "image/png", header: keyed(admin, "key-1")})

	require.Equal(t, http.StatusRequestEntityTooLarge, res.StatusCode)
	require.Equal(t, "image_too_large", readProblem(t, res).Code)
	require.Zero(t, e.storedKeys())
}

// The contract says which routes take the key: every write that needs a sign-in.
func TestEverySignedInWriteDeclaresTheKeyAndItsTwoCodes(t *testing.T) {
	e := newEnv(t)
	raw, err := io.ReadAll(e.do(request{path: "/openapi.json"}).Body)
	require.NoError(t, err)
	var doc struct {
		Paths map[string]map[string]struct {
			Security   []map[string][]string `json:"security"`
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))

	var writes int
	for path, methods := range doc.Paths {
		for method, op := range methods {
			hasKey := false
			for _, p := range op.Parameters {
				if p.Name == "Idempotency-Key" && p.In == "header" {
					hasKey = true
				}
			}
			name := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			if method == "get" || len(op.Security) == 0 {
				require.False(t, hasKey, name)
				continue
			}
			writes++
			require.True(t, hasKey, name)
			require.Contains(t, op.Responses, "409", name)
			require.Contains(t, op.Responses, "422", name)
		}
	}
	require.Greater(t, writes, 20, "the document has the writes")
}

// `me` is the caller's ID, so the two addresses of the caller are one path.
func TestMeAndTheCallersOwnIDAreTheSamePath(t *testing.T) {
	e := newEnv(t)
	id, admin := e.signUp()
	patch := func(path, name string) *http.Response {
		return e.do(request{method: http.MethodPatch, path: path, body: map[string]any{"name": name}, header: keyed(admin, "key-1")})
	}

	first := answerOf(t, patch("/users/me", "Augusta"))
	again := answerOf(t, patch("/users/"+id, "Augusta"))
	other := patch("/users/"+id, "Ada")

	require.Equal(t, http.StatusOK, first.status)
	require.Equal(t, first, again)
	require.Equal(t, http.StatusUnprocessableEntity, other.StatusCode)
	require.Equal(t, "idempotency_key_reused", readProblem(t, other).Code)
}

// bigDelete sends a DELETE of an item with a body of total bytes, and returns
// the status and how many bytes of the body the server took before it answered.
// It writes to the connection itself: a client that follows the rules stops
// when the answer comes, and so the count is what the server read, give or
// take what the connection holds on the way.
func (e *env) bigDelete(token, itemID string, total int, header http.Header) (status int, sent int64) {
	e.t.Helper()
	conn, err := net.Dial("tcp", e.ts.Listener.Addr().String())
	require.NoError(e.t, err)
	defer conn.Close()
	require.NoError(e.t, conn.SetDeadline(time.Now().Add(30*time.Second)))
	head := "DELETE /api/v2/items/" + itemID + " HTTP/1.1\r\nHost: wish.example\r\n" +
		"Cookie: " + sessionCookieName + "=" + token + "\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Length: " + strconv.Itoa(total) + "\r\n"
	for name, values := range header {
		head += name + ": " + values[0] + "\r\n"
	}
	_, err = conn.Write([]byte(head + "\r\n"))
	require.NoError(e.t, err)

	var written atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		chunk := make([]byte, 64<<10)
		for written.Load() < int64(total) {
			n, err := conn.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(e.t, err)
	defer res.Body.Close()
	sent = written.Load()
	conn.Close() // stops the writer
	<-done
	return res.StatusCode, sent
}

// A DELETE takes no body, so the server never read one. A key must not make it
// read one: the body of a route that declares none is no part of the request.
func TestAKeyedWriteThatTakesNoBodyDoesNotReadOne(t *testing.T) {
	e := newEnv(t)
	grace, listID := e.grace()
	plain, keyedItem := e.addItem(grace, listID, "Kettle"), e.addItem(grace, listID, "Teapot")
	const total = 64 << 20
	key := http.Header{"Idempotency-Key": {"key-1"}}

	plainStatus, plainSent := e.bigDelete(grace, plain.ID, total, nil)
	keyedStatus, keyedSent := e.bigDelete(grace, keyedItem.ID, total, key)

	require.Equal(t, http.StatusNoContent, plainStatus)
	require.Equal(t, plainStatus, keyedStatus, "with the key, the answer is the one with no key")
	require.Less(t, plainSent, int64(total/4), "with no key, the server leaves the body unread")
	require.Less(t, keyedSent, int64(total/4), "with the key, the server leaves the body unread too")
	require.Empty(t, e.order(grace, listID))

	// The body and its type are no part of the request, so a repeat without
	// them is the same request, and gets the first answer.
	again := e.deleteItem(grace, keyedItem.ID, key)
	require.Equal(t, http.StatusNoContent, again.StatusCode)
}
