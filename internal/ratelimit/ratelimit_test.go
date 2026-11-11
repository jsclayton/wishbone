package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func TestLimitsPerKeyAndRecovers(t *testing.T) {
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(3, time.Minute, c.Now)

	for range 3 {
		require.True(t, l.Allow("a"))
	}
	require.False(t, l.Allow("a"))
	require.True(t, l.Allow("b"), "other keys are independent")

	c.now = c.now.Add(59 * time.Second)
	require.False(t, l.Allow("a"))
	c.now = c.now.Add(2 * time.Second)
	require.True(t, l.Allow("a"))
}

func TestRejectedAttemptsDoNotExtendTheWindow(t *testing.T) {
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(1, time.Minute, c.Now)
	require.True(t, l.Allow("a"))
	for range 5 {
		c.now = c.now.Add(10 * time.Second)
		require.False(t, l.Allow("a"))
	}
	c.now = c.now.Add(11 * time.Second)
	require.True(t, l.Allow("a"))
}

func TestIdleKeysAreForgotten(t *testing.T) {
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(1, time.Minute, c.Now)
	l.Allow("a")
	c.now = c.now.Add(2 * time.Minute)
	l.Allow("b")
	require.Equal(t, 1, l.size())
}

func TestExpiredAttemptsStopCountingBeforeSweep(t *testing.T) {
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(2, time.Minute, c.Now)
	l.Allow("x") // first sweep happens here
	c.now = c.now.Add(30 * time.Second)
	require.True(t, l.Allow("a"))
	require.True(t, l.Allow("a"))
	c.now = c.now.Add(40 * time.Second) // no sweep due, but a's attempts are still fresh
	require.False(t, l.Allow("a"))
	c.now = c.now.Add(25 * time.Second)
	require.True(t, l.Allow("a"))
}

func TestFullReportsTheLimitAndRecordsNothing(t *testing.T) {
	c := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	l := New(2, time.Minute, c.Now)

	for range 5 {
		require.False(t, l.Full("a"), "checking never uses up an attempt")
	}
	require.True(t, l.Allow("a"))
	require.False(t, l.Full("a"))
	require.True(t, l.Allow("a"))
	require.True(t, l.Full("a"))
	require.False(t, l.Full("b"))

	c.now = c.now.Add(2 * time.Minute)
	require.False(t, l.Full("a"), "the window passes")
}
