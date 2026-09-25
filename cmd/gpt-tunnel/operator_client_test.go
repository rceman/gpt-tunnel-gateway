package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestOperatorCLIRequestEnforcesBodyBounds(t *testing.T) {
	stateDir := t.TempDir()
	if _, err := service.EnsureOperatorToken(stateDir); err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Store(true)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
		_, _ = w.Write([]byte(strings.Repeat(" ", cliOperatorBodyLimit)))
	}))
	defer server.Close()
	c := config.Config{StateDir: stateDir, ListenAddr: strings.TrimPrefix(server.URL, "http://")}
	if _, err := operatorCLIRequest[map[string]any](context.Background(), c, "/operator/test", map[string]string{"value": strings.Repeat("x", cliOperatorBodyLimit)}); err == nil || !strings.Contains(err.Error(), "request exceeds bounds") {
		t.Fatalf("oversized request error=%v", err)
	}
	if called.Load() {
		t.Fatal("oversized request was sent to the daemon")
	}
	if _, err := operatorCLIRequest[map[string]any](context.Background(), c, "/operator/test", struct{}{}); err == nil || !strings.Contains(err.Error(), "response exceeds bounds") {
		t.Fatalf("oversized response error=%v", err)
	}
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	c.ListenAddr = strings.TrimPrefix(redirector.URL, "http://")
	if _, err := operatorCLIRequest[map[string]any](context.Background(), c, "/operator/test", struct{}{}); err == nil {
		t.Fatal("operator request followed an HTTP redirect")
	}
	if redirected.Load() {
		t.Fatal("operator credential was forwarded across an HTTP redirect")
	}
}
