package server

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type versionedSession struct {
	sessionView
	Version string `json:"version"`
}

type versionedSessionList struct {
	Data       []versionedSession `json:"data"`
	NextCursor *string            `json:"next_cursor"`
	TotalCount int                `json:"total_count"`
}

func (e *env) sessionsQuery(userID, query, token string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/users/" + userID + "/sessions?" + query, header: cookie(token)})
}

// manySessions signs a user in four times: the cookie session from setup, then
// three bearer sessions an hour apart. It returns the user's ID and the tokens,
// oldest first.
func (e *env) manySessions() (id string, tokens []string) {
	e.t.Helper()
	id, cookieToken := e.signUp()
	tokens = []string{cookieToken}
	for range 3 {
		e.clock.advance(time.Hour)
		tokens = append(tokens, decode[redeemBody](e.t, e.redeem(e.makeLink(cookieToken, id), "bearer", nil)).Token)
	}
	// The next request is a minute after the last sign-in, so it is the newest use.
	e.clock.advance(time.Minute)
	return id, tokens
}

func TestTheSessionsListPagesNewestUseFirst(t *testing.T) {
	e := newEnv(t)
	id, tokens := e.manySessions()
	token := tokens[0]

	first := decode[versionedSessionList](t, e.sessionsQuery(id, "limit=3", token))
	require.Len(t, first.Data, 3)
	require.Equal(t, 4, first.TotalCount)
	require.NotNil(t, first.NextCursor)
	second := decode[versionedSessionList](t, e.sessionsQuery(id, "limit=3&cursor="+url.QueryEscape(*first.NextCursor), token))
	require.Len(t, second.Data, 1)
	require.Nil(t, second.NextCursor)

	all := append(append([]versionedSession{}, first.Data...), second.Data...)
	for i := 1; i < len(all); i++ {
		require.True(t, all[i-1].LastUsedAt.After(all[i].LastUsedAt) || all[i-1].LastUsedAt.Equal(all[i].LastUsedAt), "newest use first")
	}
	// The cookie session made the requests, so it was used last.
	require.True(t, all[0].IsCurrent)
}

func TestTheSessionsListSortsBackwardsWithAMinus(t *testing.T) {
	e := newEnv(t)
	id, tokens := e.manySessions()

	list := decode[versionedSessionList](t, e.sessionsQuery(id, "sort=-last_used_at", tokens[0]))

	require.Len(t, list.Data, 4)
	require.False(t, list.Data[0].IsCurrent)
	require.True(t, list.Data[3].IsCurrent, "the oldest use is last when reversed: the cookie session is the newest use")
	explicit := decode[versionedSessionList](t, e.sessionsQuery(id, "sort=last_used_at", tokens[0]))
	require.Equal(t, list.Data[3].ID, explicit.Data[0].ID)
}

func TestTheSessionsListRefusesUnknownFiltersAndSortKeysAndMixedCursors(t *testing.T) {
	e := newEnv(t)
	id, tokens := e.manySessions()
	token := tokens[0]
	first := decode[versionedSessionList](t, e.sessionsQuery(id, "limit=1", token))

	for _, tc := range []struct{ query, code string }{
		{"is_current=true", "unknown_filter"},
		{"sort=created_at", "unknown_sort"},
		{"sort=-user_agent", "unknown_sort"},
		{"sort=-last_used_at&cursor=" + url.QueryEscape(*first.NextCursor), "cursor_mismatch"},
		{"limit=0", "invalid_input"},
		{"limit=201", "invalid_input"},
	} {
		res := e.sessionsQuery(id, tc.query, token)
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, tc.query)
		require.Equal(t, tc.code, readProblem(t, res).Code, tc.query)
	}
}

func TestACursorOfTheSessionsListDoesNotFitTheUsersList(t *testing.T) {
	e := newEnv(t)
	id, tokens := e.manySessions()
	token := tokens[0]
	sessions := decode[versionedSessionList](t, e.sessionsQuery(id, "limit=1", token))

	res := e.listUsers(token, "cursor="+url.QueryEscape(*sessions.NextCursor))

	require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode)
	require.Equal(t, "cursor_mismatch", readProblem(t, res).Code)
}

func TestEachSessionHasAVersionThatChangesWhenTheSessionIsUsed(t *testing.T) {
	e := newEnv(t)
	id, tokens := e.manySessions()
	// The ID of the second token's session. Asking as that session uses it, so
	// do it before the first version is read.
	var bearerID string
	for _, s := range decode[versionedSessionList](t, e.sessionsQuery(id, "", tokens[1])).Data {
		if s.IsCurrent {
			bearerID = s.ID
		}
	}
	versionOfBearer := func() string {
		for _, s := range decode[versionedSessionList](t, e.sessionsQuery(id, "", tokens[0])).Data {
			if s.ID == bearerID {
				return s.Version
			}
		}
		return ""
	}
	before := versionOfBearer()
	require.Regexp(t, `^"[0-9a-f]{32}"$`, before)
	require.Equal(t, before, versionOfBearer(), "listing changes nothing about another session")

	e.clock.advance(time.Minute)
	require.Equal(t, http.StatusOK, e.do(request{path: "/users/me", header: bearer(tokens[1])}).StatusCode)

	require.NotEqual(t, before, versionOfBearer())
}

func TestRevokingASessionChecksIfMatch(t *testing.T) {
	e := newEnv(t)
	id, tokens := e.manySessions()
	token := tokens[0]
	rows := decode[versionedSessionList](t, e.sessionsQuery(id, "", token)).Data
	var other versionedSession
	for _, s := range rows {
		if !s.IsCurrent {
			other = s
		}
	}
	revoke := func(sessionID, ifMatch string) *http.Response {
		h := cookie(token)
		if ifMatch != "" {
			h.Set("If-Match", ifMatch)
		}
		return e.do(request{method: http.MethodDelete, path: "/users/" + id + "/sessions/" + sessionID, header: h})
	}

	res := revoke(other.ID, `"stale"`)
	require.Equal(t, http.StatusPreconditionFailed, res.StatusCode)
	require.Equal(t, "resource_changed", readProblem(t, res).Code)
	require.Len(t, decode[versionedSessionList](t, e.sessionsQuery(id, "", token)).Data, 4, "nothing was revoked")

	require.Equal(t, http.StatusNoContent, revoke(other.ID, other.Version).StatusCode)
	require.Len(t, decode[versionedSessionList](t, e.sessionsQuery(id, "", token)).Data, 3)
	// A session that is gone is a 404, with or without If-Match.
	require.Equal(t, http.StatusNotFound, revoke(other.ID, other.Version).StatusCode)
	require.Equal(t, http.StatusNotFound, revoke(other.ID, "").StatusCode)
}
