package service

import (
	"testing"
	"time"
)

func TestSharedOutboxRetryDelayIsBounded(t *testing.T) {
	if got := sharedOutboxRetryDelay(1); got != time.Second {
		t.Fatalf("first retry delay=%s", got)
	}
	if got := sharedOutboxRetryDelay(5); got != 16*time.Second {
		t.Fatalf("fifth retry delay=%s", got)
	}
	if got := sharedOutboxRetryDelay(50); got != 16*time.Second {
		t.Fatalf("retry delay was unbounded=%s", got)
	}
}
