package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type responseBoundaryRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (r *responseBoundaryRecorder) Flush() {
	r.flushes++
	r.ResponseRecorder.Flush()
}

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
	recorder := &responseBoundaryRecorder{ResponseRecorder: httptest.NewRecorder()}
	queue.release(recorder)
	if recorder.flushes != 1 {
		t.Fatalf("deferred response flushes=%d, want 1", recorder.flushes)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("deferred work did not run after response release")
	}
}

func TestResponseBoundaryDoesNotFlushOrdinaryMCPResponses(t *testing.T) {
	server := newSessionTestServer(t)
	recorder := &responseBoundaryRecorder{ResponseRecorder: httptest.NewRecorder()}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`))
	request.Host = "127.0.0.1:1"
	request.RemoteAddr = "127.0.0.1:1234"
	server.Router().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.flushes != 0 {
		t.Fatalf("ordinary MCP response status=%d flushes=%d", recorder.Code, recorder.flushes)
	}
}
