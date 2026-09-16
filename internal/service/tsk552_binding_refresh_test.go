package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestTSK552BindingWriteRestoresBytesWhenRefreshFails(t *testing.T) {
	s, _, _ := testService(t)
	path := filepath.Join(t.TempDir(), "config.json")
	persisted := s.Config
	persisted.Controller.TunnelHealthListenAddr = "127.0.0.1:8876"
	persisted.ProjectAgentBindings = map[string]map[string]config.AgentBinding{}
	data, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	refreshErr := fmt.Errorf("injected refresh failure")
	err = s.persistAgentBindingWithRefresh(path, "example", "coder-example", config.AgentBinding{SessionKey: "runtime-updated"}, func() error {
		return refreshErr
	})
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte(refreshErr.Error())) {
		t.Fatalf("refresh failure err=%v", err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("host config bytes changed after failed refresh: before=%q after=%q", original, restored)
	}
}

func TestTSK552RunningServiceRefreshesHostAgentBindingWithoutRestart(t *testing.T) {
	s, _, _ := testService(t)
	path := filepath.Join(t.TempDir(), "config.json")
	persisted := s.Config
	persisted.Controller.TunnelHealthListenAddr = "127.0.0.1:8876"
	persisted.ProjectAgentBindings = map[string]map[string]config.AgentBinding{
		"example": {"coder-example": {SessionKey: "runtime-updated"}},
	}
	data, err := json.Marshal(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s.ConfigPath = path
	s.EnableHostConfigRefresh()
	resolved, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID: "example",
		Role:      "coding",
		AgentID:   "coder-example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SessionKey != "runtime-updated" {
		t.Fatalf("resolved stale host binding=%#v", resolved)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveAgent(context.Background(), AgentResolveInput{
		ProjectID: "example",
		Role:      "coding",
		AgentID:   "coder-example",
	}); err == nil {
		t.Fatal("invalidated live host configuration remained usable")
	}
}
