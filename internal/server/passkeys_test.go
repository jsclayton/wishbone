package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
)

// passkeyOrigin is the origin of the test server's PublicURL.
const passkeyOrigin = "https://wish.example"

type passkeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CanEdit    bool       `json:"can_edit"`
	CanDelete  bool       `json:"can_delete"`
	Version    string     `json:"version"`
}

type passkeyList struct {
	Data       []passkeyView `json:"data"`
	NextCursor *string       `json:"next_cursor"`
	TotalCount int           `json:"total_count"`
}

func (e *env) listPasskeys(userID string, header http.Header, query string) *http.Response {
	e.t.Helper()
	return e.do(request{path: "/users/" + userID + "/passkeys" + query, header: header})
}

// registerPasskey runs the whole registration over HTTP, with the caller's
// credentials, for the user at userID, and returns the answer to the last call.
func (e *env) registerPasskey(userID string, header http.Header, a *passkeytest.Authenticator, name string) *http.Response {
	e.t.Helper()
	begin := e.do(request{method: http.MethodPost, path: "/users/" + userID + "/passkeys/registration", header: header})
	if begin.StatusCode != http.StatusOK {
		return begin
	}
	var opts json.RawMessage
	require.NoError(e.t, json.NewDecoder(begin.Body).Decode(&opts))
	body := map[string]any{"credential": json.RawMessage(a.Create(e.t, opts, passkeyOrigin))}
	if name != "" {
		body["name"] = name
	}
	return e.do(request{method: http.MethodPost, path: "/users/" + userID + "/passkeys", header: header, body: body})
}

func TestAUserAddsAPasskeyAndSeesItInTheList(t *testing.T) {
	e := newEnv(t)
	id, token := e.signUp()
	require.Empty(t, decode[passkeyList](t, e.listPasskeys(id, cookie(token), "")).Data)

	res := e.registerPasskey(id, cookie(token), passkeytest.New(t), "Laptop")

	require.Equal(t, http.StatusCreated, res.StatusCode)
	made := decode[passkeyView](t, res)
	require.Equal(t, "/api/v2/users/"+id+"/passkeys/"+made.ID, res.Header.Get("Location"))
	etag := res.Header.Get("ETag")
	require.NotEmpty(t, etag)
	require.Equal(t, "Laptop", made.Name)
	require.Nil(t, made.LastUsedAt)
	require.True(t, made.CanEdit)
	require.True(t, made.CanDelete)

	res = e.listPasskeys(id, cookie(token), "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	list := decode[passkeyList](t, res)
	require.Equal(t, 1, list.TotalCount)
	require.Nil(t, list.NextCursor)
	require.Len(t, list.Data, 1)
	row := list.Data[0]
	require.Equal(t, made.ID, row.ID)
	require.Equal(t, "Laptop", row.Name)
	require.Equal(t, e.clock.now(), row.CreatedAt)
	require.Nil(t, row.LastUsedAt)
	require.True(t, row.CanEdit)
	require.True(t, row.CanDelete)
	require.Equal(t, etag, row.Version, "a row's version is the ETag of the passkey")
}
