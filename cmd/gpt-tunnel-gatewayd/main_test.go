package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestGatewayHTTPServerAllowsBoundedDelayedActionResponse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("ok"))
	})
	server := newGatewayHTTPServer("127.0.0.1:0", handler)
	if server.WriteTimeout != 0 {
		t.Fatalf("gateway transport has a fixed write timeout: %s", server.WriteTimeout)
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + listener.Addr().String())
		if err != nil {
			done <- result{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		done <- result{
			body: string(body),
			err:  err,
		}
	}()
	<-started
	close(release)
	select {
	case got := <-done:
		if got.err != nil || got.body != "ok" {
			t.Fatalf("delayed action response failed: body=%q err=%v", got.body, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("delayed action response did not complete")
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}
