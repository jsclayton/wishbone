package server

import (
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/patch"
	"jsclayton.dev/wishbone/internal/user"
)

// A request reads its caller and the occasion, and writes later. An unlink or a
// deactivation that commits between the two must stop the write: the service
// checks the caller's role again inside the write's transaction. The tests call
// the service with the parent as the route first loaded them, after the change.

func TestAnUnlinkBetweenTheCheckAndTheWriteStopsTheWrite(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	recital := e.addOccasion(f.mom, "Recital", annual(3, 3), emmaID)
	mom, err := user.Get(t.Context(), e.db, mustID(t, f.momID))
	require.NoError(t, err)
	e.linkParents(f.admin, emmaID)

	svc := e.srv.opts.Occasions
	id := mustID(t, recital.ID)
	name := "Nope"
	_, err = svc.Update(t.Context(), mom, id, occasion.Changes{Name: patch.Optional[string]{Set: true, Value: &name}}, nil)
	require.ErrorIs(t, err, occasion.ErrNotFound, "Mom no longer sees Emma, so the occasion is gone for her")
	require.ErrorIs(t, svc.Delete(t.Context(), mom, id, nil), occasion.ErrNotFound)
	_, err = svc.Create(t.Context(), mom, occasion.New{Name: "Nope", Rule: occasion.Annual(1, 1), UserID: ptrID(mustID(t, emmaID))})
	require.ErrorIs(t, err, occasion.ErrUserNotFound)
}

func TestADeactivationBetweenTheCheckAndTheWriteStopsTheWrite(t *testing.T) {
	e := newEnv(t)
	f := e.family()
	emmaID, _ := e.otherUser("Emma")
	e.linkParents(f.admin, emmaID, f.momID)
	recital := e.addOccasion(f.mom, "Recital", annual(3, 3), emmaID)
	mom, err := user.Get(t.Context(), e.db, mustID(t, f.momID))
	require.NoError(t, err)
	e.deactivated(f.admin, emmaID)

	svc := e.srv.opts.Occasions
	id := mustID(t, recital.ID)
	name := "Nope"
	_, err = svc.Update(t.Context(), mom, id, occasion.Changes{Name: patch.Optional[string]{Set: true, Value: &name}}, nil)
	require.ErrorIs(t, err, occasion.ErrUserDeactivated)
	require.ErrorIs(t, svc.Delete(t.Context(), mom, id, nil), occasion.ErrUserDeactivated)
	_, err = svc.Create(t.Context(), mom, occasion.New{Name: "Nope", Rule: occasion.Annual(1, 1), UserID: ptrID(mustID(t, emmaID))})
	require.ErrorIs(t, err, occasion.ErrUserDeactivated)
	require.Equal(t, "Recital", decode[occasionView](t, e.getOccasion(f.mom, recital.ID)).Name)
}

func ptrID(id int64) *int64 { return &id }
