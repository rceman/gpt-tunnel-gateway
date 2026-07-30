package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsNonLoopback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"schema_version":1,"gateway_id":"home","listen_addr":"0.0.0.0:1","state_dir":"/tmp/s","max_read_bytes":1,"max_diff_bytes":1,"max_list_items":1,"dispatch_timeout_seconds":1,"run_timeout_seconds":60,"airelay_command":"airelay","hub":{"root":"/tmp/h","remote":"origin","branch":"main","author_name":"x","author_email":"x@y"},"controller":{"tunnel_health_listen_addr":"127.0.0.1:8766"},"projects":{}}`
	_ = os.WriteFile(path, []byte(data), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsRemovedProtocolConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"schema_version":1,"gateway_id":"home","listen_addr":"127.0.0.1:8875","state_dir":"/tmp/s","max_read_bytes":1,"max_diff_bytes":1,"max_list_items":1,"dispatch_timeout_seconds":1,"run_timeout_seconds":60,"airelay_command":"airelay","hub":{"root":"` + dir + `","remote":"origin","branch":"main","protocol_root":"protocol/v4","author_name":"x","author_email":"x@y"},"controller":{"tunnel_health_listen_addr":"127.0.0.1:8766"},"projects":{}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("removed protocol configuration was accepted")
	}
}

func TestValidateRejectsNonLoopbackTunnelHealth(t *testing.T) {
	dir := t.TempDir()
	c := Config{SchemaVersion: 1, GatewayID: "home", ListenAddr: "127.0.0.1:8875", StateDir: "/tmp/s", MaxReadBytes: 1, MaxDiffBytes: 1, MaxListItems: 1, DispatchTimeoutSeconds: 1, RunTimeoutSeconds: 60, AirelayCommand: "airelay", Hub: HubConfig{Root: dir, Remote: "origin", Branch: "main", AuthorName: "x", AuthorEmail: "x@y"}, Controller: ControllerConfig{TunnelHealthListenAddr: "0.0.0.0:8766"}, Projects: map[string]ProjectConfig{}}
	if err := c.Validate(); err == nil {
		t.Fatal("non-loopback tunnel health address accepted")
	}
}
