package session

import (
	"testing"
	"time"
)

func TestMonotonicSessionTimestampClampsClockRollback(t *testing.T) {
	previous := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	if got := monotonicSessionTimestamp(previous, previous.Add(-time.Second)); !got.Equal(previous) {
		t.Fatalf("rollback timestamp=%s, want %s", got, previous)
	}
}
