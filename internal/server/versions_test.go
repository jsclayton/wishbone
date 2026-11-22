package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/user"
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
		if strings.Contains(path, "?") {
			cursor = "&cursor=" + *page.NextCursor
		}
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
// every collection they can read, with the row's version, each user who is in
// one of those rows only as a summary, and each membership they can read, with a
// hash of the IDs that its collection gives them. It also requires each
// resource's own GET to carry the row's version as its ETag.
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
	// A membership is the IDs that the caller's own collection gives, in its order.
	membership := func(kind, owner, collection string) {
		ids := []string{}
		for _, row := range e.rows(token, collection) {
			ids = append(ids, id(row))
		}
		version, err := versionOf(ids)
		require.NoError(e.t, err)
		want[versionKey{kind: kind, id: owner}] = version
	}
	// adminsMembership is one that only the admin reads: anyone else has no entry.
	adminsMembership := func(kind, owner, collection string, refused ...int) {
		res := e.do(request{path: collection, header: cookie(token)})
		if res.StatusCode != http.StatusOK {
			require.Contains(e.t, refused, res.StatusCode, "get %s", collection)
			return
		}
		membership(kind, owner, collection)
	}
	for _, g := range e.rows(token, "/groups") {
		membership("group_users", id(g), "/groups/"+id(g)+"/users")
		membership("users_in_group", id(g), "/users?group_id="+id(g))
		adminsMembership("group_groups", id(g), "/groups/"+id(g)+"/groups", http.StatusForbidden)
	}
	for _, o := range e.rows(token, "/occasions") {
		membership("users_of_occasion", id(o), "/users?occasion_id="+id(o))
		// A 404 is an occasion that isn't shared: it has no groups for anyone.
		adminsMembership("occasion_groups", id(o), "/occasions/"+id(o)+"/groups", http.StatusForbidden, http.StatusNotFound)
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
	t.Parallel()
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
	require.ElementsMatch(t, versionKindOrder, slices.Collect(maps.Keys(kinds)), "the family has every kind in it")
}

func TestVersionsNeedsASignIn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.signUp()

	res := e.do(request{path: "/versions"})

	requireCoded(t, res, http.StatusUnauthorized, "unauthorized")
}

func itemKey(it itemView) versionKey {
	return versionKey{kind: "item", id: it.ID, listID: it.ListID}
}

func TestAClaimMovesTheGiversVersionsAndNotTheRecipients(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// moved are the entries that differ between two answers of GET /versions: the
// ones that are in one and not the other, and the ones whose version changed.
func moved(before, after map[versionKey]string) []versionKey {
	var keys []versionKey
	for k, v := range before {
		if after[k] != v {
			keys = append(keys, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func notModified(tag string) http.Header { return http.Header{"If-None-Match": {tag}} }

func TestVersionsRefusesAQuery(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	_, admin := e.signUp()

	// The route has no paging, no filter and no sort, so it takes no query at all.
	for _, query := range []string{"limit=5", "cursor=abc", "sort=kind", "kind=item", "list_id=7"} {
		res := e.do(request{path: "/versions?" + query, header: cookie(admin)})
		requireCoded(t, res, http.StatusUnprocessableEntity, "unknown_filter")
	}
}

func TestAUserWhoMovesToAnotherGroupMovesTheVersionsOfWhoeverReadsEitherGroup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	gusID, _ := e.cousin("Gus", f)
	cousins := e.cousins(f)
	club := e.newGroup(f.admin, "Book club").ID
	e.join(f.admin, club, f.sueID)
	type answer struct {
		versions map[versionKey]string
		tag      string
	}
	before := map[string]answer{}
	for name, token := range map[string]string{"Sue": f.sue, "the admin": f.admin, "Hedy": f.hedy} {
		versions, tag := e.versions(token)
		before[name] = answer{versions, tag}
	}

	// Gus goes from Cousins to the Book club. Sue is in both, so she sees Gus
	// before and after: no user, group or other resource of hers changes.
	e.join(f.admin, club, gusID)
	e.removeFromGroup(f.admin, cousins, gusID)

	groups := []versionKey{
		{kind: "group_users", id: cousins}, {kind: "users_in_group", id: cousins},
		{kind: "group_users", id: club}, {kind: "users_in_group", id: club},
	}
	for name, token := range map[string]string{"Sue": f.sue, "the admin": f.admin} {
		after, _ := e.versions(token)
		require.ElementsMatch(t, groups, moved(before[name].versions, after), "%s reads both groups, and only their users moved", name)
		require.Equal(t, http.StatusOK, e.getVersions(token, notModified(before[name].tag)).StatusCode, "%s: the old ETag is not the answer any more", name)
		require.Equal(t, e.seenThroughGets(token), after, name)
	}
	// Hedy is in neither group, so she reads neither, and nothing moved for her.
	require.Equal(t, http.StatusNotModified, e.getVersions(f.hedy, notModified(before["Hedy"].tag)).StatusCode)
}

func TestAUserWhoMovesFromAnInnerGroupToTheOuterOneMovesItsDirectUsers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	cousins := e.cousins(f)
	inner := e.newGroup(f.admin, "Second cousins").ID
	e.nest(f.admin, cousins, inner)
	bobID, _ := e.otherUser("Bob")
	e.join(f.admin, inner, bobID)
	before, tag := e.versions(f.sue)

	// Bob was in Cousins through the inner group, and is now in it directly. For
	// Sue, the users of Cousins with nesting are the same. The users directly in it
	// are not.
	e.join(f.admin, cousins, bobID)
	e.removeFromGroup(f.admin, inner, bobID)

	after, _ := e.versions(f.sue)
	require.Equal(t, []versionKey{{kind: "group_users", id: cousins}}, moved(before, after))
	require.Equal(t, http.StatusOK, e.getVersions(f.sue, notModified(tag)).StatusCode)
	require.Equal(t, e.seenThroughGets(f.sue), after)
}

func TestACallerWhoCannotReadAMembershipHasNoEntryForIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	cousins := e.cousins(f)
	inner := e.newGroup(f.admin, "Second cousins").ID
	e.nest(f.admin, cousins, inner)
	reunion := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	e.attach(f.admin, reunion, cousins)
	birthday := e.birthdayOf(f.sue, f.sueID)

	// Sue is in Cousins and sees the Reunion. She reads their users, and not their
	// groups: those two collections are the admin's.
	sue, _ := e.versions(f.sue)
	require.Contains(t, sue, versionKey{kind: "group_users", id: cousins})
	require.Contains(t, sue, versionKey{kind: "users_in_group", id: cousins})
	require.Contains(t, sue, versionKey{kind: "users_of_occasion", id: reunion})
	require.Contains(t, sue, versionKey{kind: "users_of_occasion", id: birthday})
	require.Empty(t, kindsOf(sue, "group_groups"))
	require.Empty(t, kindsOf(sue, "occasion_groups"))
	requireCoded(t, e.innerGroups(f.sue, cousins), http.StatusForbidden, "forbidden")
	requireCoded(t, e.occasionGroups(f.sue, reunion, ""), http.StatusForbidden, "forbidden")
	// She is not in the inner group, so she has nothing of it.
	require.Equal(t, []string{cousins}, kindsOf(sue, "group_users"))
	require.Equal(t, []string{cousins}, kindsOf(sue, "users_in_group"))

	// The admin reads all four. Only a shared occasion has groups.
	admin, _ := e.versions(f.admin)
	for _, kind := range []string{"group_users", "users_in_group", "group_groups"} {
		require.ElementsMatch(t, []string{cousins, inner}, kindsOf(admin, kind), kind)
	}
	require.Equal(t, []string{reunion}, kindsOf(admin, "occasion_groups"))
	require.Contains(t, admin, versionKey{kind: "users_of_occasion", id: reunion})

	// Hedy is in no group and sees neither occasion.
	hedy, _ := e.versions(f.hedy)
	require.Empty(t, kindsOf(hedy, "group_users"))
	require.Empty(t, kindsOf(hedy, "users_in_group"))
	require.NotContains(t, hedy, versionKey{kind: "users_of_occasion", id: reunion})
	require.NotContains(t, hedy, versionKey{kind: "users_of_occasion", id: birthday})
}

func TestAttachingAGroupMovesTheOccasionsMembershipsAndNotTheOccasion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	reunion := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	before, tag := e.versions(f.admin)

	e.attach(f.admin, reunion, e.cousins(f))

	after, _ := e.versions(f.admin)
	require.ElementsMatch(t, []versionKey{{kind: "users_of_occasion", id: reunion}, {kind: "occasion_groups", id: reunion}}, moved(before, after),
		"the occasion's own body is the same: only who it applies to, and its groups, moved")
	require.Equal(t, http.StatusOK, e.getVersions(f.admin, notModified(tag)).StatusCode)
	require.Equal(t, e.seenThroughGets(f.admin), after)
}

func TestAClaimOnAChildsListMovesTheParentWhoIsAGiverAndNotTheChild(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	e.join(f.admin, e.cousins(f), emmaID)
	crayons := e.addItem(emma, e.primaryListFor(emma, emmaID), "Crayons", map[string]any{"quantity": 2})
	emmaBefore, emmaTag := e.versions(emma)
	momBefore, momTag := e.versions(f.mom)

	e.claim(f.sue, crayons.ID, nil)

	// Emma is the recipient: nothing she can read says that it happened.
	emmaAfter, _ := e.versions(emma)
	require.Equal(t, emmaBefore, emmaAfter)
	require.Equal(t, http.StatusNotModified, e.getVersions(emma, notModified(emmaTag)).StatusCode)

	// Mom edits the list as Emma's parent, and is not a recipient of it. So she is a
	// giver there, and the item's claim state is hers to see: it moved.
	momAfter, _ := e.versions(f.mom)
	require.Equal(t, []versionKey{itemKey(crayons)}, moved(momBefore, momAfter))
	require.Equal(t, http.StatusOK, e.getVersions(f.mom, notModified(momTag)).StatusCode)
	require.Equal(t, e.seenThroughGets(f.mom), momAfter)
}

func TestTheClockAloneMovesAGiverWhoseGiftNowNeedsAttentionAndNoRecipient(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle", map[string]any{"quantity": 2})
	g := e.claim(f.sue, kettle.ID, nil) // not bought, for Mom's birthday: given at 2027-01-02T00:00:00Z
	sueBefore, sueTag := e.versions(f.sue)
	_, momTag := e.versions(f.mom)

	// Eight days before the gift is given. Nobody writes anything, and the server's
	// scheduler has nothing to do.
	e.clock.set(time.Date(2026, time.December, 25, 0, 0, 0, 0, time.UTC))

	sueAfter, _ := e.versions(f.sue)
	require.Equal(t, []versionKey{{kind: "gift", id: g.ID}}, moved(sueBefore, sueAfter), "the gift needs attention now")
	require.Equal(t, http.StatusOK, e.getVersions(f.sue, notModified(sueTag)).StatusCode)
	require.Equal(t, e.seenThroughGets(f.sue), sueAfter)
	require.Equal(t, http.StatusNotModified, e.getVersions(f.mom, notModified(momTag)).StatusCode, "a recipient reads nothing of it")
}

func TestADeactivationThatReleasesGiftsMovesNoRecipientThroughTheRelease(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	gusID, gus := e.cousin("Gus", f)
	grill := e.addItem(f.mom, f.house, "Grill", map[string]any{"quantity": 2})
	e.claim(gus, grill.ID, nil)
	momBefore, _ := e.versions(f.mom)
	_, dadTag := e.versions(f.dad)

	require.Equal(t, http.StatusOK, e.deactivate(f.admin, gusID).StatusCode)
	require.Zero(t, e.openGiftRows(gusID), "the gift was released")

	// Dad is a recipient of the list and shares no group with Gus. The release is
	// the only thing that could reach him, and it doesn't.
	require.Equal(t, http.StatusNotModified, e.getVersions(f.dad, notModified(dadTag)).StatusCode)

	// Mom is a recipient too, and she shared a group with Gus. She stops seeing
	// Gus and his own list, as her GETs show. Nothing of "Our house" or its item
	// moves for her.
	momAfter, _ := e.versions(f.mom)
	for _, k := range moved(momBefore, momAfter) {
		require.NotEqual(t, f.house, k.listID, "moved on the list: %+v", k)
		require.NotEqual(t, versionKey{kind: "list", id: f.house}, k)
	}
	require.Equal(t, momBefore[itemKey(grill)], momAfter[itemKey(grill)])
	require.NotContains(t, momAfter, versionKey{kind: "user", id: gusID})
	require.Equal(t, e.seenThroughGets(f.mom), momAfter)
}

// TestEveryKindOfVersionHasAPlaceInTheOrderAndInTheEnum reads the kind
// constants from versions.go. A kind that is missing from versionKindOrder would
// sort first with no error, and one that is missing from the enum would be
// absent from the contract.
func TestEveryKindOfVersionHasAPlaceInTheOrderAndInTheEnum(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(gotoken.NewFileSet(), "versions.go", nil, 0)
	require.NoError(t, err)
	var constants []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "kind") {
				continue
			}
			value, err := strconv.Unquote(spec.Values[i].(*ast.BasicLit).Value)
			require.NoError(t, err)
			constants = append(constants, value)
		}
		return true
	})
	require.GreaterOrEqual(t, len(constants), 14, "the kind constants of versions.go")

	require.ElementsMatch(t, constants, versionKindOrder, "versionKindOrder has each kind once")
	field, _ := reflect.TypeFor[ResourceVersion]().FieldByName("Kind")
	require.Equal(t, versionKindOrder, strings.Split(field.Tag.Get("enum"), ","), "the enum has the kinds, in the same order")
}

// A statement that reads the users of too many groups or occasions at once is
// more than SQLite takes, so [user.Service.IDsMatchingEach] reads them in parts.
// 120 of each kind is more than one statement can hold for a caller who is not
// the admin, and each one must still be what the collection gives.
func TestTheUsersOfManyGroupsAndOccasionsAreReadInParts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.family()
	ctx := t.Context()
	cousins, err := strconv.ParseInt(e.cousins(f), 10, 64)
	require.NoError(t, err)
	reunionID := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	e.attach(f.admin, reunionID, e.cousins(f))
	sueID, err := strconv.ParseInt(f.sueID, 10, 64)
	require.NoError(t, err)
	sue, err := e.srv.opts.Users.Get(ctx, sueID)
	require.NoError(t, err)
	id, err := strconv.ParseInt(reunionID, 10, 64)
	require.NoError(t, err)
	reunion, err := e.srv.opts.Occasions.GetVisible(ctx, sue, id)
	require.NoError(t, err)
	birthday, err := strconv.ParseInt(e.birthdayOf(f.sue, f.momID), 10, 64)
	require.NoError(t, err)
	moms, err := e.srv.opts.Occasions.GetVisible(ctx, sue, birthday)
	require.NoError(t, err)

	kinds := map[string]user.Filter{
		"a group with nesting": {GroupID: &cousins},
		"a group, directly":    user.DirectlyIn(cousins),
		"a shared occasion":    {},
		"a birthday":           {},
	}
	shared, birth := kinds["a shared occasion"], kinds["a birthday"]
	shared.Where, shared.WhereArgs = occasion.UsersOf(reunion)
	birth.Where, birth.WhereArgs = occasion.UsersOf(moms)
	kinds["a shared occasion"], kinds["a birthday"] = shared, birth

	every, err := userRules.all()
	require.NoError(t, err)
	for name, filter := range kinds {
		users, _, _, err := e.srv.opts.Users.Matching(ctx, sue, filter, every)
		require.NoError(t, err, name)
		want := make([]int64, len(users))
		for i, u := range users {
			want[i] = u.ID
		}
		require.NotEmpty(t, want, name)

		got, err := e.srv.opts.Users.IDsMatchingEach(ctx, sue, slices.Repeat([]user.Filter{filter}, 120))

		require.NoError(t, err, name)
		require.Len(t, got, 120, name)
		for i := range got {
			require.Equal(t, want, got[i], "%s, filter %d", name, i)
		}
	}
}
