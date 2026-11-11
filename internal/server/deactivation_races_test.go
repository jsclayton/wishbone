package server

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/passkey/passkeytest"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/store"
)

// These tests cover the check that session revoke and passkey delete make again
// inside the write's transaction (docs/spec.md, Who acts while a user is
// deactivated; the parent's powers). A request reads the user, and between that
// and the write an unlink or a deactivation can commit. Removing the check from
// either route turns a test here red.

// racingServices builds a server whose session and passkey services run a function
// before their transactions begin (see [racingPool]).
func racingServices(t *testing.T) (*env, *atomic.Pointer[func()]) {
	t.Helper()
	var armed *atomic.Pointer[func()]
	e := newEnv(t, func(o *Options) {
		pool, hook := racingPool(t, o.Store.(*store.Store).DB())
		armed = hook
		o.Sessions = session.NewService(pool, o.Now)
		passkeys, err := passkey.New(pool, o.Now, o.PublicURL)
		require.NoError(t, err)
		o.Passkeys = passkeys
	})
	return e, armed
}

// raceOf arms the hook: the function runs when the next transaction of a racing
// service begins.
func raceOf(armed *atomic.Pointer[func()], f func()) { armed.Store(&f) }

// emmasSession returns the ID of Emma's one live session.
func (e *env) emmasSession(admin, emmaID string) string {
	e.t.Helper()
	list := decode[sessionList](e.t, e.listSessions(emmaID, cookie(admin)))
	require.Len(e.t, list.Data, 1)
	return list.Data[0].ID
}

func TestARevokeThatRacesAnUnlinkIsRefusedAndTheSessionStaysLive(t *testing.T) {
	e, armed := racingServices(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	id := e.emmasSession(admin, emmaID)
	raceOf(armed, func() { e.linkParents(admin, emmaID) })

	res := e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/sessions/" + id, header: cookie(mom)})

	requireCoded(t, res, http.StatusNotFound, "not_found") // Mom no longer sees Emma
	require.Equal(t, id, e.emmasSession(admin, emmaID), "the session was not revoked")
}

func TestARevokeThatRacesADeactivationIsRefusedAndTheSessionStaysLive(t *testing.T) {
	e, armed := racingServices(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	id := e.emmasSession(admin, emmaID)
	// Deactivation revokes the sessions, so the session that races it is one that
	// was made just after, as a sign-in that had not seen the deactivation would.
	raceOf(armed, func() {
		e.deactivated(admin, emmaID)
		_, err := e.db.ExecContext(t.Context(), `UPDATE sessions SET revoked_at = NULL WHERE id = ?`, id)
		require.NoError(t, err)
	})

	res := e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/sessions/" + id, header: cookie(mom)})

	requireDeactivated(t, res)
	require.Equal(t, id, e.emmasSession(admin, emmaID), "the session was not revoked")
}

// emmaWithAPasskey makes Emma, Mom's child, with one passkey, and returns the
// passkey's ID.
func (e *env) emmaWithAPasskey(admin, momID string) (emmaID, passkeyID string) {
	e.t.Helper()
	emmaID, emma := e.otherUser("Emma")
	e.linkParents(admin, emmaID, momID)
	made := decode[passkeyView](e.t, e.registerPasskey(emmaID, cookie(emma), passkeytest.New(e.t), "Phone"))
	return emmaID, made.ID
}

func TestAPasskeyDeleteThatRacesAnUnlinkIsRefusedAndThePasskeyStays(t *testing.T) {
	e, armed := racingServices(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, passkeyID := e.emmaWithAPasskey(admin, momID)
	raceOf(armed, func() { e.linkParents(admin, emmaID) })

	res := e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/passkeys/" + passkeyID, header: cookie(mom)})

	requireCoded(t, res, http.StatusNotFound, "not_found")
	require.Len(t, decode[passkeyList](t, e.listPasskeys(emmaID, cookie(admin), "")).Data, 1)
}

func TestAPasskeyDeleteThatRacesADeactivationIsRefusedAndThePasskeyStays(t *testing.T) {
	e, armed := racingServices(t)
	_, admin := e.signUp()
	momID, mom := e.otherUser("Mom")
	emmaID, passkeyID := e.emmaWithAPasskey(admin, momID)
	raceOf(armed, func() { e.deactivated(admin, emmaID) })

	res := e.do(request{method: http.MethodDelete, path: "/users/" + emmaID + "/passkeys/" + passkeyID, header: cookie(mom)})

	requireDeactivated(t, res)
	require.Len(t, decode[passkeyList](t, e.listPasskeys(emmaID, cookie(admin), "")).Data, 1)
}
