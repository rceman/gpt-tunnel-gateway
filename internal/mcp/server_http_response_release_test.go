package mcp

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseReleaseRunsOnlyAfterResponseFlush(t *testing.T) {
	queue := &responseReleaseQueue{}
	released := make(chan struct{}, 1)
	ctx := withResponseRelease(context.Background(), queue.add)
	release, ok := responseReleaseFromContext(ctx)
	if !ok {
		t.Fatal("response release hook was not attached")
	}
	release(func() { released <- struct{}{} })
	select {
	case <-released:
		t.Fatal("deferred work ran before response release")
	default:
	}
	queue.release(httptest.NewRecorder())
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("deferred work did not run after response release")
	}
}
