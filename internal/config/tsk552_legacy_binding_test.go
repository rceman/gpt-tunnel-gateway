package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTSK552LegacyAgentProfileIsDiscardedOnLoad(t *testing.T) {
	dir := t.TempDir()
	c := baseConfig(dir)
	c.ProjectAgentBindings = map[string]map[string]AgentBinding{
		"example": {"RDX-WORKER": {SessionKey: "runtime_master"}},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"session_key":"runtime_master"`, `"session_key":"runtime_master","profile":"coding"`, 1))
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	binding, found := loaded.ResolveAgentBinding("example", "RDX-WORKER")
	if !found || binding.SessionKey != "runtime_master" {
		t.Fatalf("binding=%#v found=%v", binding, found)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), `"profile"`) {
		t.Fatalf("legacy profile survived migration: %s", persisted)
	}
}
