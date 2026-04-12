package sponsor

import (
	"testing"
	"time"
)

type testClock struct {
	now time.Time
}

func newTestClock(t time.Time) *testClock {
	return &testClock{now: t}
}

func (c *testClock) Now() time.Time {
	return c.now
}

func (c *testClock) Set(t time.Time) {
	c.now = t
}

func (c *testClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestRealClock(t *testing.T) {
	c := realClock{}
	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("realClock.Now() = %v, want between %v and %v", got, before, after)
	}
}

func TestTestClock(t *testing.T) {
	fixed := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	c := newTestClock(fixed)

	if got := c.Now(); !got.Equal(fixed) {
		t.Errorf("Now() = %v, want %v", got, fixed)
	}

	newTime := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	c.Set(newTime)
	if got := c.Now(); !got.Equal(newTime) {
		t.Errorf("after Set(), Now() = %v, want %v", got, newTime)
	}

	c.Advance(24 * time.Hour)
	expected := newTime.Add(24 * time.Hour)
	if got := c.Now(); !got.Equal(expected) {
		t.Errorf("after Advance(24h), Now() = %v, want %v", got, expected)
	}
}

func TestTestClockAdvanceMultiple(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := newTestClock(start)

	c.Advance(time.Hour)
	c.Advance(30 * time.Minute)

	expected := start.Add(90 * time.Minute)
	if got := c.Now(); !got.Equal(expected) {
		t.Errorf("after two Advance calls, Now() = %v, want %v", got, expected)
	}
}
