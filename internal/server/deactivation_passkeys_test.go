package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheAdminOrAParentWhoAddsOrRenamesADeactivatedUsersPasskeyGetsForbidden(t *testing.T) {
	e := newEnv(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _, passkeyID := e.deactivatedEmma(admin, momID)

	// Only the user adds or renames a passkey, whoever else sees them. The reason
	// is the role, not the deactivation, so it is forbidden and not user_deactivated.
	for name, token := range map[string]string{"the admin": admin, "a parent": mom} {
		for call, r := range map[string]request{
			"begin":  {method: http.MethodPost, path: "/users/" + emmaID + "/passkeys/registration"},
			"finish": {method: http.MethodPost, path: "/users/" + emmaID + "/passkeys", body: map[string]any{"credential": map[string]any{}}},
			"rename": {method: http.MethodPatch, path: "/users/" + emmaID + "/passkeys/" + passkeyID, body: map[string]any{"name": "Mine"}},
		} {
			r.header = cookie(token)
			res := e.do(r)
			require.Equal(t, http.StatusForbidden, res.StatusCode, name+" "+call)
			require.Equal(t, "forbidden", readProblem(t, res).Code, name+" "+call)
		}
	}
	// Deleting is a power of the role, which a deactivated user's parent doesn't have.
	for _, token := range []string{admin, mom} {
		requireDeactivated(t, e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/passkeys/" + passkeyID, header: cookie(token)}))
	}
}
