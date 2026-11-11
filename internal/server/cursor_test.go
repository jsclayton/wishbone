package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/require"
)

func TestACollectionWithNoNameIsRefusedWhenItsRouteIsRegistered(t *testing.T) {
	// A cursor carries the name, so without one two collections could share cursors.
	require.Panics(t, func() {
		collectionRules{Sorts: passkeyRules.Sorts}.apply(&huma.Operation{})
	})
}

// forge makes a cursor the way the server does, with a value of the caller's choice.
func forge(t *testing.T, collection, sort, value string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"c": collection, "s": sort, "v": value, "i": 1})
	require.NoError(t, err)
	return url.QueryEscape(base64.RawURLEncoding.EncodeToString(raw))
}

// A cursor that decodes but holds a value the server cannot read is the
// client's mistake, in every collection: a 422, never a 500.
func TestACursorWithAValueThatCannotBeReadIsA422InEveryCollection(t *testing.T) {
	e := newEnv(t)
	id, admin := e.signUp()

	for name, res := range map[string]*http.Response{
		"users by created_at":   e.listUsers(admin, "sort=created_at&cursor="+forge(t, "users", "created_at", "yesterday")),
		"users by -created_at":  e.listUsers(admin, "sort=-created_at&cursor="+forge(t, "users", "-created_at", "")),
		"sessions by last use":  e.sessionsQuery(id, "cursor="+forge(t, "sessions", "last_used_at", "yesterday"), admin),
		"sessions by -last use": e.sessionsQuery(id, "sort=-last_used_at&cursor="+forge(t, "sessions", "-last_used_at", "12"), admin),
	} {
		require.Equal(t, http.StatusUnprocessableEntity, res.StatusCode, name)
		require.Equal(t, "invalid_input", readProblem(t, res).Code, name)
	}
}

func TestAWellFormedForgedCursorStillWorks(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()

	res := e.listUsers(admin, "sort=created_at&cursor="+forge(t, "users", "created_at", "2026-11-08T12:00:00Z"))

	require.Equal(t, http.StatusOK, res.StatusCode)
}
