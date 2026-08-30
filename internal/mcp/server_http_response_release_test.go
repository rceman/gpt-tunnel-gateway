package mcp

import (
	"bytes"
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

func TestResponseBoundaryCommitsCompleteBodyBeforeDeferredWorker(t *testing.T) {
	recorder := &responseBoundaryRecorder{ResponseRecorder: httptest.NewRecorder()}
	boundary := (&Server{}).responseBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release, ok := responseReleaseFromContext(r.Context())
		if !ok {
			t.Fatal("response release hook was not attached")
		}
		release(func() {
			if got := recorder.Body.String(); got != "complete response" {
				t.Errorf("deferred worker observed body=%q, want complete response", got)
			}
			if got := recorder.Header().Get("Content-Length"); got != "17" {
				t.Errorf("deferred worker observed Content-Length=%q, want 17", got)
			}
		})
		_, _ = w.Write([]byte("complete response"))
	}))
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/mcp", nil)
	boundary.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "complete response" {
		t.Fatalf("response status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if recorder.flushes != 1 {
		t.Fatalf("deferred response flushes=%d, want 1", recorder.flushes)
	}
}

func TestResponseBoundaryRejectsResponsesOverHardBufferBound(t *testing.T) {
	recorder := &responseBoundaryRecorder{ResponseRecorder: httptest.NewRecorder()}
	workerRan := false
	boundary := (&Server{}).responseBoundary(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release, ok := responseReleaseFromContext(r.Context())
		if !ok {
			t.Fatal("response release hook was not attached")
		}
		release(func() { workerRan = true })
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, maxBufferedMCPResponseBytes+1))
	}))
	boundary.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/mcp", nil))
	if workerRan {
		t.Fatal("deferred worker ran after bounded response overflow")
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("overflow response status=%d, want %d", recorder.Code, http.StatusInternalServerError)
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
