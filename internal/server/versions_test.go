package server

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover GET /versions: the kind, the ID and the version of
// everything the caller can see, in one answer (docs/spec.md, API principles,
// Versions of everything a caller sees; docs/routes.md, Versions). What a
// caller can see is what their collections and GETs give, so the tests read
// those and compare.

// versionKey names one resource in the answer of GET /versions.
type versionKey struct{ kind, id, listID, itemID string }

type versionsView struct {
	Data []struct {
		Kind    string  `json:"kind"`
		ID      string  `json:"id"`
		Version string  `json:"version"`
		ListID  *string `json:"list_id"`
		ItemID  *string `json:"item_id"`
	} `json:"data"`
}

func (e *env) getVersions(token string, header http.Header) *http.Response {
	e.t.Helper()
	h := cookie(token)
	for k, v := range header {
		h[k] = v
	}
	return e.do(request{path: "/versions", header: h})
}

// versions reads GET /versions as the caller: the version of each resource, and
// the ETag of the answer.
func (e *env) versions(token string) (map[versionKey]string, string) {
	e.t.Helper()
	res := e.getVersions(token, nil)
	require.Equal(e.t, http.StatusOK, res.StatusCode, "get versions")
	got := map[versionKey]string{}
	for _, v := range decode[versionsView](e.t, res).Data {
		key := versionKey{kind: v.Kind, id: v.ID}
		if v.ListID != nil {
			key.listID = *v.ListID
		}
		if v.ItemID != nil {
			key.itemID = *v.ItemID
		}
		require.NotContains(e.t, got, key, "a resource is listed once")
		require.Regexp(e.t, `^"[0-9a-f]{32}"$`, v.Version)
		got[key] = v.Version
	}
	require.Regexp(e.t, `^"[0-9a-f]{32}"$`, res.Header.Get("ETag"))
	return got, res.Header.Get("ETag")
}

// rows reads every row of a collection as the caller, following next_cursor.
func (e *env) rows(token, path string) []map[string]any {
	e.t.Helper()
	var out []map[string]any
	cursor := ""
	for {
		res := e.do(request{path: path + cursor, header: cookie(token)})
		require.Equal(e.t, http.StatusOK, res.StatusCode, "list %s", path)
		page := decode[struct {
			Data       []map[string]any `json:"data"`
			NextCursor *string          `json:"next_cursor"`
		}](e.t, res)
		out = append(out, page.Data...)
		if page.NextCursor == nil {
			return out
		}
		cursor = "?cursor=" + *page.NextCursor
	}
}

// userSummariesIn finds the user summaries anywhere in a decoded body. A user
// summary is the one object with exactly the keys id, name and avatar
// (docs/routes.md, Summary shapes).
func userSummariesIn(v any, found func(map[string]any)) {
	switch v := v.(type) {
	case map[string]any:
		if len(v) == 3 && v["id"] != nil && v["name"] != nil {
			if _, ok := v["avatar"]; ok {
				found(v)
			}
		}
		for _, child := range v {
			userSummariesIn(child, found)
		}
	case []any:
		for _, child := range v {
			userSummariesIn(child, found)
		}
	}
}

// seenThroughGets is what GET /versions must list for the caller: every row of
// every collection they can read, with the row's version, and each user who is
// in one of those rows only as a summary. It also requires each resource's own
// GET to carry the row's version as its ETag.
func (e *env) seenThroughGets(token string) map[versionKey]string {
	e.t.Helper()
	want := map[versionKey]string{}
	var bodies []any
	id := func(row map[string]any) string { return row["id"].(string) }
	add := func(key versionKey, row map[string]any, address string) {
		version := row["version"].(string)
		if address != "" {
			res := e.do(request{path: address, header: cookie(token)})
			require.Equal(e.t, http.StatusOK, res.StatusCode, "get %s", address)
			require.Equal(e.t, version, res.Header.Get("ETag"), "the ETag of %s", address)
		}
		want[key] = version
		bodies = append(bodies, row)
	}
	for _, u := range e.rows(token, "/users") {
		add(versionKey{kind: "user", id: id(u)}, u, "/users/"+id(u))
	}
	for _, l := range e.rows(token, "/lists") {
		add(versionKey{kind: "list", id: id(l)}, l, "/lists/"+id(l))
		for _, r := range e.rows(token, "/lists/"+id(l)+"/recipients") {
			// A recipient has no address to GET: the row's version is all there is.
			add(versionKey{kind: "list_recipient", id: id(r["user"].(map[string]any)), listID: id(l)}, r, "")
		}
		for _, it := range e.rows(token, "/lists/"+id(l)+"/items") {
			add(versionKey{kind: "item", id: id(it), listID: id(l)}, it, "/items/"+id(it))
			for _, s := range e.rows(token, "/items/"+id(it)+"/store-links") {
				add(versionKey{kind: "store_link", id: id(s), itemID: id(it)}, s, "/items/"+id(it)+"/store-links/"+id(s))
			}
		}
	}
	for kind, path := range map[string]string{"gift": "/gifts", "occasion": "/occasions", "group": "/groups", "invitation": "/invitations"} {
		for _, row := range e.rows(token, path) {
			add(versionKey{kind: kind, id: id(row)}, row, path+"/"+id(row))
		}
	}
	userSummariesIn(bodies, func(summary map[string]any) {
		if _, full := want[versionKey{kind: "user", id: id(summary)}]; full {
			return
		}
		raw, err := json.Marshal(summary)
		require.NoError(e.t, err)
		var s UserSummary
		require.NoError(e.t, json.Unmarshal(raw, &s))
		version, err := versionOf(s)
		require.NoError(e.t, err)
		key := versionKey{kind: "user_summary", id: s.ID}
		if earlier, ok := want[key]; ok {
			require.Equal(e.t, earlier, version, "the summary of user %s is the same wherever it is", s.ID)
		}
		want[key] = version
	})
	return want
}

// kindsOf are the IDs of one kind in an answer of GET /versions.
func kindsOf(versions map[versionKey]string, kind string) []string {
	var ids []string
	for k := range versions {
		if k.kind == kind {
			ids = append(ids, k.id)
		}
	}
	slices.Sort(ids)
	return ids
}

// household is the family of [env.family] with more in it, so that every kind of
// resource is in somebody's answer: Emma is Mom's child and in no group, Mom's
// Kettle has store links and Sue's open gift, Sue's gift of the Grill is given,
// Mom has an avatar and a custom occasion, the admin made two shared occasions
// and a nested group, and Sue has an invitation to "Our house".
type household struct {
	family
	emma, emmaID, emmaList string
	kettle, grill          itemView
	open, given            giftView
}

func (e *env) household() household {
	e.t.Helper()
	h := household{family: e.family()}
	h.emmaID, h.emma = e.otherUser("Emma")
	e.linkParents(h.admin, h.emmaID, h.momID)
	h.emmaList = e.primaryListFor(h.emma, h.emmaID)
	require.Equal(e.t, http.StatusOK, e.setAvatar(h.mom, h.momID, e.uploaded(h.mom).ID).StatusCode)

	inLaws := e.newGroup(h.admin, "In-laws")
	e.join(h.admin, inLaws.ID, h.dadID)
	e.nest(h.admin, e.cousins(h.family), inLaws.ID)
	e.addShared(h.admin, "Christmas", annual(12, 25), true)
	e.attach(h.admin, e.addShared(h.admin, "Reunion", annual(7, 4), false).ID, inLaws.ID)
	advent := e.addOccasion(h.mom, "Advent", annual(12, 6), "")

	h.kettle = e.addItem(h.mom, h.momList, "Kettle", map[string]any{"quantity": 2, "store_links": []map[string]any{
		{"url": "https://shop.example/kettle", "price_minor": 4500, "currency": "USD"},
		{"url": "https://other.example/kettle"},
	}})
	h.grill = e.addItem(h.dad, h.house, "Grill")
	e.addItem(h.emma, h.emmaList, "Crayons")
	e.addItem(h.sue, e.primaryListFor(h.sue, h.sueID), "Scarf")

	h.given = e.claim(h.sue, h.grill.ID, map[string]any{"occasion_id": advent.ID})
	require.Equal(e.t, 1, e.revealAt("2026-12-07T00:00:00Z"))
	h.open = e.claim(h.sue, h.kettle.ID, nil)
	e.invite(h.mom, h.house, h.sueID)
	return h
}

func TestVersionsListsExactlyWhatTheCallersCollectionsAndGetsGive(t *testing.T) {
	e := newEnv(t)
	h := e.household()

	kinds := map[string]bool{}
	for name, token := range map[string]string{
		"the admin":                      h.admin,
		"a parent and recipient":         h.mom,
		"a child":                        h.emma,
		"a giver":                        h.sue,
		"a recipient in no shared group": h.dad,
		"a user only the admin sees":     h.hedy,
	} {
		got, _ := e.versions(token)
		require.Equal(t, e.seenThroughGets(token), got, name)
		for k := range got {
			kinds[k.kind] = true
		}
	}
	require.ElementsMatch(t, []string{"user", "user_summary", "list", "list_recipient", "item", "store_link", "gift", "occasion", "group", "invitation"},
		slices.Collect(maps.Keys(kinds)), "the family has every kind in it")
}

func TestVersionsNeedsASignIn(t *testing.T) {
	e := newEnv(t)
	e.signUp()

	res := e.do(request{path: "/versions"})

	requireCoded(t, res, http.StatusUnauthorized, "unauthorized")
}

func itemKey(it itemView) versionKey {
	return versionKey{kind: "item", id: it.ID, listID: it.ListID}
}

func TestAClaimMovesTheGiversVersionsAndNotTheRecipients(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	momBefore, momTag := e.versions(f.mom)
	sueBefore, sueTag := e.versions(f.sue)

	g := e.claim(f.sue, kettle.ID, nil)

	// Mom is a recipient of the list: nothing she can read says that it happened.
	momAfter, momTagAfter := e.versions(f.mom)
	require.Equal(t, momBefore, momAfter)
	require.Equal(t, momTag, momTagAfter)
	require.Equal(t, http.StatusNotModified, e.getVersions(f.mom, http.Header{"If-None-Match": {momTag}}).StatusCode)
	require.NotContains(t, momAfter, versionKey{kind: "gift", id: g.ID})

	// Sue is the giver: she has a gift, and the item's claim state moved for her.
	sueAfter, sueTagAfter := e.versions(f.sue)
	require.NotEqual(t, sueTag, sueTagAfter)
	require.Equal(t, g.Version, sueAfter[versionKey{kind: "gift", id: g.ID}])
	require.NotEqual(t, sueBefore[itemKey(kettle)], sueAfter[itemKey(kettle)])
	require.Equal(t, e.seenThroughGets(f.sue), sueAfter)
}

func TestAFullyClaimedItemLeavesTheVersionsOfAGiverWithNoOpenGiftOnIt(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	gus := e.giver("Gus", f)
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	momBefore, _ := e.versions(f.mom)
	for name, token := range map[string]string{"Gus": gus, "Sue": f.sue} {
		got, _ := e.versions(token)
		require.Contains(t, got, itemKey(kettle), name)
	}

	g := e.claim(f.sue, kettle.ID, nil)

	gusAfter, _ := e.versions(gus)
	require.NotContains(t, gusAfter, itemKey(kettle), "the last one is claimed, and Gus has no gift on it")
	require.Equal(t, e.seenThroughGets(gus), gusAfter)
	sueAfter, _ := e.versions(f.sue)
	require.Contains(t, sueAfter, itemKey(kettle), "Sue has an open gift on it")
	momAfter, _ := e.versions(f.mom)
	require.Equal(t, momBefore, momAfter, "a recipient sees the item as it was")

	// Sue unclaims, and the item is back for Gus.
	require.Equal(t, http.StatusNoContent, e.deleteGift(f.sue, g.ID, nil).StatusCode)
	gusAgain, _ := e.versions(gus)
	require.Contains(t, gusAgain, itemKey(kettle))
}

func TestAGiftEntersTheRecipientsVersionsWhenItIsGiven(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	g := e.claim(f.sue, kettle.ID, nil) // for Mom's birthday, Jan 1: given at 2027-01-02T00:00:00Z
	key := versionKey{kind: "gift", id: g.ID}
	before, tag := e.versions(f.mom)
	require.NotContains(t, before, key)

	// The moment before: nothing moved for Mom, though the clock did.
	require.Zero(t, e.revealAt("2027-01-01T23:59:59Z"))
	early, earlyTag := e.versions(f.mom)
	require.Equal(t, before, early)
	require.Equal(t, tag, earlyTag)

	// The server gives the gift on its own clock. Nobody sent a write.
	require.Equal(t, 1, e.revealAt("2027-01-02T00:00:00Z"))

	after, afterTag := e.versions(f.mom)
	require.NotEqual(t, tag, afterTag)
	require.Equal(t, e.getGift(f.mom, g.ID).Header.Get("ETag"), after[key])
	require.Equal(t, e.seenThroughGets(f.mom), after)
}

func TestADeletedAMovedAndAHiddenResourceAreOnlyAbsentFromVersions(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	gus := e.giver("Gus", f)
	deleted, moved, hidden := e.addItem(f.mom, f.momList, "Kettle"), e.addItem(f.mom, f.momList, "Scarf"), e.addItem(f.mom, f.momList, "Grill")
	kept := e.addItem(f.mom, f.momList, "Book")
	before, _ := e.versions(f.sue)
	for _, it := range []itemView{deleted, moved, hidden, kept} {
		require.Contains(t, before, itemKey(it), it.Title)
	}

	// Mom deletes one, moves one to her child's list, which Sue can't see, and Gus
	// claims the last of one.
	require.Equal(t, http.StatusNoContent, e.deleteItem(f.mom, deleted.ID, nil).StatusCode)
	require.Equal(t, http.StatusOK, e.patchItem(f.mom, moved.ID, map[string]any{"list_id": e.primaryListFor(emma, emmaID)}, nil).StatusCode)
	e.claim(gus, hidden.ID, nil)

	after, _ := e.versions(f.sue)
	require.Equal(t, []string{kept.ID}, kindsOf(after, "item"), "each of the three is absent, and nothing says why")
	for _, it := range []itemView{deleted, moved, hidden} {
		requireCoded(t, e.getItem(f.sue, it.ID), http.StatusNotFound, "not_found")
	}
	require.Equal(t, e.seenThroughGets(f.sue), after)

	// The answer has the resources, and nothing about the ones that went.
	raw := decode[map[string]any](t, e.getVersions(f.sue, nil))
	delete(raw, "$schema")
	require.Equal(t, []string{"data"}, slices.Collect(maps.Keys(raw)))
	for _, entry := range raw["data"].([]any) {
		require.ElementsMatch(t, []string{"kind", "id", "version", "list_id", "item_id"}, slices.Collect(maps.Keys(entry.(map[string]any))))
	}

	// A user goes the same way: out of the group, Sue sees none of Mom's.
	e.removeFromGroup(f.admin, e.cousins(f), f.sueID)
	alone, _ := e.versions(f.sue)
	require.Equal(t, []string{f.sueID}, kindsOf(alone, "user"))
	require.Empty(t, kindsOf(alone, "item"))
	require.Empty(t, kindsOf(alone, "group"))
	require.Equal(t, e.seenThroughGets(f.sue), alone)
}

func TestVersionsAnswers304WhileNothingTheCallerSeesChanged(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	_, tag := e.versions(f.sue)
	ifNoneMatch := func(value string) http.Header { return http.Header{"If-None-Match": {value}} }

	res := e.getVersions(f.sue, ifNoneMatch(tag))

	require.Equal(t, http.StatusNotModified, res.StatusCode)
	require.Empty(t, readBody(t, res))
	require.Equal(t, tag, res.Header.Get("ETag"))

	// A weak tag, one of several, a tag without its quotes, and * match too.
	for _, value := range []string{"W/" + tag, `"0123", ` + tag, strings.Trim(tag, `"`), "*"} {
		require.Equal(t, http.StatusNotModified, e.getVersions(f.sue, ifNoneMatch(value)).StatusCode, value)
	}
	// Another caller's tag is not Sue's.
	_, momTag := e.versions(f.mom)
	require.NotEqual(t, tag, momTag)
	require.Equal(t, http.StatusOK, e.getVersions(f.sue, ifNoneMatch(momTag)).StatusCode)

	// A change that Sue can't see leaves her answer where it was.
	require.Equal(t, http.StatusOK, e.patchList(f.hedy, f.hedysList, map[string]any{"name": "Hedy's wishes"}, nil).StatusCode)
	require.Equal(t, http.StatusNotModified, e.getVersions(f.sue, ifNoneMatch(tag)).StatusCode)

	// One that she can see gets a 200, with a new ETag that then gets a 304.
	require.Equal(t, http.StatusOK, e.patchList(f.mom, f.momList, map[string]any{"name": "Mom's wishes"}, nil).StatusCode)
	res = e.getVersions(f.sue, ifNoneMatch(tag))
	require.Equal(t, http.StatusOK, res.StatusCode)
	changed := res.Header.Get("ETag")
	require.NotEqual(t, tag, changed)
	require.NotEmpty(t, decode[versionsView](t, res).Data)
	require.Equal(t, http.StatusNotModified, e.getVersions(f.sue, ifNoneMatch(changed)).StatusCode)
}

func TestAUserSeenOnlyAsASummaryIsListedAsASummary(t *testing.T) {
	e := newEnv(t)
	f := e.family()

	// Dad is a recipient of "Our house", which Sue sees through Mom. She shares no
	// group with him, so she gets his summary and never the user.
	got, _ := e.versions(f.sue)
	requireCoded(t, e.getUser(f.sue, f.dadID), http.StatusNotFound, "not_found")
	require.Equal(t, []string{f.dadID}, kindsOf(got, "user_summary"))
	require.NotContains(t, got, versionKey{kind: "user", id: f.dadID})
	want, err := versionOf(UserSummary{ID: f.dadID, Name: "Dad"})
	require.NoError(t, err)
	require.Equal(t, want, got[versionKey{kind: "user_summary", id: f.dadID}])
	require.Contains(t, got, versionKey{kind: "user", id: f.momID}, "a user she sees in full is not also a summary")

	// The summary's version moves when the summary does.
	require.Equal(t, http.StatusOK, e.patchUser(f.dad, f.dadID, map[string]any{"name": "Daddy"}, nil).StatusCode)
	renamed, _ := e.versions(f.sue)
	require.NotEqual(t, want, renamed[versionKey{kind: "user_summary", id: f.dadID}])
	require.Equal(t, e.seenThroughGets(f.sue), renamed)

	// In a group with Sue, Dad is a user and no longer a summary.
	e.join(f.admin, e.cousins(f), f.dadID)
	shared, _ := e.versions(f.sue)
	require.Empty(t, kindsOf(shared, "user_summary"))
	require.Contains(t, shared, versionKey{kind: "user", id: f.dadID})
}

func TestAUserWhoIsOnlyInAFlagOfTheCallersGiftIsListedAsASummary(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	bobID, bob := e.otherUser("Bob")
	cabin := e.extraList("Cabin", f.momID, bobID)
	g := e.claim(f.sue, e.addItem(f.mom, cabin, "Grill").ID, nil)

	// Bob leaves. Sue's gift is flagged with the recipients she last saw, and that
	// flag is the one place she still gets Bob.
	require.Equal(t, http.StatusNoContent, e.leave(bob, cabin, bobID, nil).StatusCode)
	oldIDs, _ := recipientsFlag(t, e.gift(f.sue, g.ID))
	require.Contains(t, oldIDs, bobID)

	got, _ := e.versions(f.sue)
	require.Contains(t, kindsOf(got, "user_summary"), bobID)
	require.Equal(t, e.seenThroughGets(f.sue), got)
}
