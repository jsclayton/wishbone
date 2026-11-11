package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A user with no timezone is in the server's zone (TZ), not in UTC. The env
// injects that zone. The dates are tested in internal/occasion.

func TestARecipientWithNoTimezoneIsRevealedAtMidnightInTheFallbackZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	e := newEnvIn(t, la)
	f := e.family()
	kettle := e.addItem(f.mom, f.momList, "Kettle")
	g := e.claim(f.sue, kettle.ID, nil) // Mom's birthday, Jan 1

	require.Equal(t, "2027-01-02T08:00:00Z", g.RevealAt, "00:00 on Jan 2 in Los Angeles, not in UTC")
	require.Zero(t, e.revealAt("2027-01-02T07:59:59Z"))
	require.Equal(t, 1, e.revealAt("2027-01-02T08:00:00Z"))
}
