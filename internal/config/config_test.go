package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsNonLoopback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data := `{"schema_version":1,"gateway_id":"home","listen_addr":"0.0.0.0:1","state_dir":"/tmp/s","max_read_bytes":1,"max_diff_bytes":1,"max_list_items":1,"dispatch_timeout_seconds":1,"run_timeout_seconds":60,"airelay_command":"airelay","hub":{"root":"/tmp/h","remote":"origin","branch":"main","protocol_root":"protocol/v4","author_name":"x","author_email":"x@y"},"controller":{},"projects":{}}`
	_ = os.WriteFile(path, []byte(data), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}
