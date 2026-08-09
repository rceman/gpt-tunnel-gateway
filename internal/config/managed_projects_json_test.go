package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedProjectRegistryNestedDuplicateAndTrailingFieldsRejected(t *testing.T) {
	stateDir := t.TempDir()
	root := filepath.Join(stateDir, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("create root: %v", err)
	}
	entry := `{"root":"` + root + `","repository_url":"git@github.com:example/demo.git","remote":"origin","default_branch":"main","airelay_session_key":"demo_master"}`
	for name, data := range map[string]string{
		"duplicate_entry_field": `{"schema_version":1,"revision":0,"projects":{"demo":` + strings.Replace(entry, `"remote":"origin"`, `"remote":"origin","remote":"origin"`, 1) + `}}`,
		"unknown_entry_field":   `{"schema_version":1,"revision":0,"projects":{"demo":` + strings.TrimSuffix(entry, "}") + `,"unknown":true}}`,
		"entry_trailing":        `{"schema_version":1,"revision":0,"projects":{"demo":` + strings.TrimSuffix(entry, "}") + `}{}}`,
	} {
		path := filepath.Join(stateDir, name+".json")
		writeManagedTestFile(t, path, []byte(data))
		requireManagedLoadError(t, path)
	}

	// Keep encoding/json in the test dependency set tied to the strict JSON
	// fixtures rather than relying on hand-written escaping for all cases.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(`{"schema_version":1}`), &decoded); err != nil {
		t.Fatalf("sanity JSON fixture: %v", err)
	}
}
