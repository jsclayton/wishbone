package server

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests cover the ETag of a membership's collection (docs/spec.md,
// Versions of everything a caller sees; docs/routes.md, Versions). A membership
// is in no resource's body, so GET /versions gives it a version of its own: a
// hash of the IDs of its collection. The collection's answer carries that
// version as its ETag, so a client saves a membership with the version of what
// it fetched, and not with the version that an earlier GET /versions named.

// membershipPath is the collection of a membership in GET /versions.
func membershipPath(k versionKey) (path string, ok bool) {
	switch k.kind {
	case kindGroupUsers:
		return "/groups/" + k.id + "/users", true
	case kindGroupGroups:
		return "/groups/" + k.id + "/groups", true
	case kindOccasionGroups:
		return "/occasions/" + k.id + "/groups", true
	case kindUsersInGroup:
		return "/users?group_id=" + k.id, true
	case kindUsersOfOccasion:
		return "/users?occasion_id=" + k.id, true
	}
	return "", false
}

// withQuery adds the parts of a query to a path that can have a query already.
// An empty part is left out.
func withQuery(path string, parts ...string) string {
	for _, part := range parts {
		switch {
		case part == "":
		case strings.Contains(path, "?"):
			path += "&" + part
		default:
			path += "?" + part
		}
	}
	return path
}

// membershipPage is one answer of a membership's collection: its ETag, the IDs
// of its rows, and the next cursor.
type membershipPage struct {
	tag  string
	ids  []string
	next *string
}

// membershipPage reads one page of a collection as the caller, and requires the
// answer to carry an ETag.
func (e *env) membershipPage(token, path string) membershipPage {
	e.t.Helper()
	res := e.do(request{path: path, header: cookie(token)})
	require.Equal(e.t, http.StatusOK, res.StatusCode, "get %s", path)
	tag := res.Header.Get("ETag")
	require.Regexp(e.t, `^"[0-9a-f]{32}"$`, tag, "the ETag of %s", path)
	body := decode[struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		NextCursor *string `json:"next_cursor"`
	}](e.t, res)
	page := membershipPage{tag: tag, ids: []string{}, next: body.NextCursor}
	for _, row := range body.Data {
		page.ids = append(page.ids, row.ID)
	}
	return page
}

// memberships reads GET /versions as the caller, and then the collection of
// each membership in it. It returns the version of each membership, and the ETag
// of its collection's answer.
func (e *env) memberships(token string) (versions, tags map[versionKey]string) {
	e.t.Helper()
	all, _ := e.versions(token)
	versions, tags = map[versionKey]string{}, map[versionKey]string{}
	for key, version := range all {
		path, ok := membershipPath(key)
		if !ok {
			continue
		}
		versions[key] = version
		tags[key] = e.membershipPage(token, path).tag
	}
	return versions, tags
}

func TestAMembershipsCollectionAnswersWithTheVersionThatVersionsGivesIt(t *testing.T) {
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
		versions, tags := e.memberships(token)
		require.Equal(t, versions, tags, name)
		for k := range versions {
			kinds[k.kind] = true
		}
	}
	require.ElementsMatch(t,
		[]string{kindGroupUsers, kindGroupGroups, kindOccasionGroups, kindUsersInGroup, kindUsersOfOccasion},
		slices.Collect(maps.Keys(kinds)), "the family has every kind of membership in it")
}

func TestTheETagOfAMembershipsCollectionMovesExactlyWhenItsVersionDoes(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	cousins := e.cousins(f)
	inner := e.newGroup(f.admin, "Second cousins").ID
	e.nest(f.admin, cousins, inner)
	bobID, _ := e.otherUser("Bob")
	e.join(f.admin, inner, bobID)
	reunion := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	e.attach(f.admin, reunion, cousins)
	club := e.newGroup(f.admin, "Club").ID

	rename := func(name string) {
		res := e.patchUser(f.admin, bobID, map[string]any{"name": name}, nil)
		require.Equal(t, http.StatusOK, res.StatusCode, "rename Bob to %s", name)
	}
	users := func(groups ...string) []versionKey {
		var keys []versionKey
		for _, g := range groups {
			keys = append(keys, versionKey{kind: kindGroupUsers, id: g}, versionKey{kind: kindUsersInGroup, id: g})
		}
		return keys
	}
	reunionUsers := versionKey{kind: kindUsersOfOccasion, id: reunion}
	reunionGroups := versionKey{kind: kindOccasionGroups, id: reunion}

	// Each step is one change, with the memberships that it moves for the admin,
	// who reads every one.
	steps := []struct {
		name   string
		change func()
		moves  []versionKey
	}{
		{"a user joins the inner group", func() { e.join(f.admin, inner, f.dadID) },
			append(users(inner), versionKey{kind: kindUsersInGroup, id: cousins}, reunionUsers)},
		// The inner group was Bob and Dad, by name. It is now Dad and Zed.
		{"a rename changes the order", func() { rename("Zed") },
			append(users(inner), versionKey{kind: kindUsersInGroup, id: cousins}, reunionUsers)},
		{"a rename keeps the order", func() { rename("Zoe") }, nil},
		{"an empty group goes inside the outer group", func() { e.nest(f.admin, cousins, club) },
			[]versionKey{{kind: kindGroupGroups, id: cousins}}},
		// The occasion reached the inner group through the outer one already.
		{"the inner group is attached to the occasion", func() { e.attach(f.admin, reunion, inner) },
			[]versionKey{reunionGroups}},
		{"the outer group is detached from the occasion", func() {
			require.Equal(t, http.StatusNoContent, e.detachGroup(f.admin, reunion, cousins).StatusCode)
		}, []versionKey{reunionGroups, reunionUsers}},
		{"a user leaves the inner group", func() { e.removeFromGroup(f.admin, inner, f.dadID) },
			append(users(inner), versionKey{kind: kindUsersInGroup, id: cousins}, reunionUsers)},
		{"an item is added", func() { e.addItem(f.mom, f.momList, "Kettle") }, nil},
	}

	callers := map[string]string{"the admin": f.admin, "Sue": f.sue, "Mom": f.mom, "Hedy": f.hedy}
	before := map[string]map[versionKey]string{}
	for name, token := range callers {
		versions, tags := e.memberships(token)
		require.Equal(t, versions, tags, name)
		before[name] = tags
	}
	for _, step := range steps {
		step.change()
		for name, token := range callers {
			versions, tags := e.memberships(token)
			require.Equal(t, versions, tags, "%s, after %s", name, step.name)
			if name == "the admin" {
				require.ElementsMatch(t, step.moves, moved(before[name], tags), "after %s", step.name)
			}
			before[name] = tags
		}
	}
}

func TestAMembershipsETagIsTheSameOnEveryPageAndInEverySort(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	cousins := e.cousins(f)
	e.cousin("Gus", f)
	for _, name := range []string{"Second cousins", "Third cousins"} {
		e.nest(f.admin, cousins, e.newGroup(f.admin, name).ID)
	}
	reunion := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	e.attach(f.admin, reunion, cousins, e.newGroup(f.admin, "Club").ID)

	versions, _ := e.versions(f.admin)
	for _, key := range []versionKey{
		{kind: kindGroupUsers, id: cousins},
		{kind: kindUsersInGroup, id: cousins},
		{kind: kindUsersOfOccasion, id: reunion},
		{kind: kindGroupGroups, id: cousins},
		{kind: kindOccasionGroups, id: reunion},
	} {
		version := versions[key]
		require.NotEmpty(t, version, key.kind)
		path, _ := membershipPath(key)
		sorts := []string{"", "sort=name", "sort=-name"}
		if key.kind == kindUsersInGroup || key.kind == kindUsersOfOccasion {
			// GET /users has a second sort key.
			sorts = append(sorts, "sort=created_at", "sort=-created_at")
		}
		for _, sort := range sorts {
			// One row to a page, so the collection has more than one page.
			pages := 0
			for cursor := ""; ; pages++ {
				page := e.membershipPage(f.admin, withQuery(path, sort, "limit=1", cursor))
				require.Equal(t, version, page.tag, "%s, page %d with %q", key.kind, pages+1, sort)
				require.Len(t, page.ids, 1)
				if page.next == nil {
					break
				}
				cursor = "cursor=" + *page.next
			}
			require.Greater(t, pages, 0, "%s has more than one page", key.kind)
		}
	}
}

func TestOnlyAMembershipFilterGivesTheUsersCollectionAnETag(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	cousins := e.cousins(f)
	reunion := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	e.attach(f.admin, reunion, cousins)
	versions, _ := e.versions(f.sue)

	for query, want := range map[string]string{
		"group_id=" + cousins:    versions[versionKey{kind: kindUsersInGroup, id: cousins}],
		"occasion_id=" + reunion: versions[versionKey{kind: kindUsersOfOccasion, id: reunion}],
	} {
		require.NotEmpty(t, want, query)
		res := e.listUsers(f.sue, query)
		require.Equal(t, http.StatusOK, res.StatusCode, query)
		require.Equal(t, want, res.Header.Get("ETag"), query)
	}

	// Every other request for users is not a membership: all the users, another
	// filter, a membership narrowed by another filter, and two memberships at once.
	for _, query := range []string{
		"",
		"sort=created_at&limit=1",
		"group_id=",
		"occasion_id=",
		"invitable_to=" + f.house,
		"group_id=" + cousins + "&invitable_to=" + f.house,
		"occasion_id=" + reunion + "&invitable_to=" + f.house,
		"group_id=" + cousins + "&occasion_id=" + reunion,
	} {
		res := e.listUsers(f.sue, query)
		require.Equal(t, http.StatusOK, res.StatusCode, query)
		require.Empty(t, res.Header.Values("ETag"), "GET /users?%s is not a membership", query)
	}
}

func TestAMembershipTheCallerCannotReadAnswersAsOneThatDoesNotExist(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	cousins := e.cousins(f)
	reunion := e.addShared(f.admin, "Reunion", annual(7, 4), false).ID
	e.attach(f.admin, reunion, cousins)
	empty, err := versionOf([]string{})
	require.NoError(t, err)

	// Hedy is in no group, so she sees neither the group nor the occasion. The
	// filter gives her no users, and the ETag of no users, as it does for an ID
	// that names nothing: the answer doesn't show which of the two it is.
	for _, query := range []string{"group_id=" + cousins, "group_id=999", "group_id=x", "occasion_id=" + reunion, "occasion_id=999", "occasion_id=x"} {
		page := e.membershipPage(f.hedy, "/users?"+query)
		require.Empty(t, page.ids, query)
		require.Equal(t, empty, page.tag, query)
	}
}

func TestAMembershipsPageAndItsETagComeFromOneStateOfTheMembership(t *testing.T) {
	e, race := racingReads(t)
	_, admin := e.signUp()
	group := e.newGroup(admin, "Cousins").ID
	occasion := e.addShared(admin, "Reunion", annual(7, 4), false).ID
	e.attach(admin, occasion, group)

	n := 0
	newUser := func() func() {
		n++
		id, _ := e.otherUser("User " + strconv.Itoa(n))
		return func() { e.join(admin, group, id) }
	}
	newGroup := func(put func(id string)) func() func() {
		return func() func() {
			n++
			id := e.newGroup(admin, "Group "+strconv.Itoa(n)).ID
			return func() { put(id) }
		}
	}

	// Each collection is read many times. During each read, a member joins: before
	// the first statement of the request, then before the second, and so on, until
	// the request is over before the member joins. Whichever statements the join
	// falls between, the ETag is the version of the members that the page is of.
	for path, newMember := range map[string]func() func(){
		"/groups/" + group + "/users":        newUser,
		"/users?group_id=" + group:           newUser,
		"/users?occasion_id=" + occasion:     newUser,
		"/groups/" + group + "/groups":       newGroup(func(id string) { e.nest(admin, group, id) }),
		"/occasions/" + occasion + "/groups": newGroup(func(id string) { e.attach(admin, occasion, id) }),
	} {
		joined := 0
		for statement := 1; ; statement++ {
			require.Less(t, statement, 100, "%s: a request reads fewer statements than this", path)
			join := newMember()
			race.before(statement, join)
			page := e.membershipPage(admin, path)
			require.Nil(t, page.next)
			version, err := versionOf(page.ids)
			require.NoError(t, err)
			require.Equal(t, version, page.tag, "%s: a member joined before statement %d of the request", path, statement)
			if !race.ran() {
				break
			}
			joined++
		}
		require.Greater(t, joined, 2, "%s: a member joined during the read", path)
	}
}

func TestTheOpenAPIDocumentDeclaresTheETagOfAMembershipsCollection(t *testing.T) {
	e := newEnv(t)
	doc := decode[struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Headers map[string]struct {
					Schema struct {
						Type        string `json:"type"`
						Description string `json:"description"`
					} `json:"schema"`
				} `json:"headers"`
			} `json:"responses"`
		} `json:"paths"`
	}](t, e.do(request{path: "/openapi"}))

	etag := func(path string) (description string, ok bool) {
		h, ok := doc.Paths["/api/v2"+path]["get"].Responses["200"].Headers["ETag"]
		if ok {
			require.Equal(t, "string", h.Schema.Type, path)
			require.NotEmpty(t, h.Schema.Description, path)
		}
		return h.Schema.Description, ok
	}
	for _, path := range []string{"/groups/{id}/users", "/groups/{id}/groups", "/occasions/{id}/groups", "/users"} {
		_, ok := etag(path)
		require.True(t, ok, "GET %s declares its ETag", path)
	}
	// GET /users has it only for a membership, and the document says so.
	description, _ := etag("/users")
	require.Contains(t, description, "group_id")
	require.Contains(t, description, "occasion_id")
	// A collection that is no membership has none.
	for _, path := range []string{"/groups", "/occasions", "/lists"} {
		_, ok := etag(path)
		require.False(t, ok, "GET %s has no ETag", path)
	}
}
