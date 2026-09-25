package sqlitestore

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestTSK667Gate20OutboxPayloadWriterInventory(t *testing.T) {
	writers := []struct {
		path              string
		inserts           int
		updates           int
		storageContract   string
		payloadReferences []string
	}{
		{path: "internal/sqlitestore/databases_rule_seed_migration.go", inserts: 1, storageContract: "json.Marshal []byte parameter stored as BLOB", payloadReferences: []string{`"rule-create", payload`}},
		{path: "internal/sqlitestore/databases_shared_adr_summary_migration.go", inserts: 1, storageContract: "json.Marshal []byte parameter stored as BLOB", payloadReferences: []string{`"adr-summary-migration", newPayload`}},
		{path: "internal/sqlitestore/databases_shared_task_priority_migration.go", inserts: 1, storageContract: "json.Marshal []byte parameter stored as BLOB", payloadReferences: []string{`"task-priority-reaudit", newPayload`}},
		{path: "internal/sqlitestore/databases_shared_task_summary_migration.go", inserts: 1, storageContract: "json.Marshal []byte parameter stored as BLOB", payloadReferences: []string{`"task-summary-migration", newPayload`}},
		{path: "internal/sqlitestore/databases_relation_outbox_migration.go", inserts: 2, storageContract: "legacy TEXT trigger is replaced by the active BLOB trigger", payloadReferences: []string{"DROP TRIGGER IF EXISTS shared_relations_hub_outbox_after_insert", "CAST(json_object(", " AS BLOB)"}},
		{path: "internal/sqlitestore/project_configuration_hard_cut_migration.go", updates: 1, storageContract: "validated canonical JSON []byte update stored as BLOB", payloadReferences: []string{"MigrateProjectConfigurationPayload(payload)", "UPDATE hub_outbox SET payload=?", "Args: []any{canonical, id}"}},
		{path: "internal/sqlitestore/shared_lifecycle_event.go", inserts: 1, storageContract: "JSON []byte parameter stored as BLOB", payloadReferences: []string{"request.Kind, request.Payload, recorded"}},
		{path: "internal/sqlitestore/shared_lifecycle_mutation.go", inserts: 1, storageContract: "JSON []byte parameter stored as BLOB", payloadReferences: []string{"request.Kind, payload, created"}},
		{path: "internal/sqlitestore/shared_lifecycle_revision.go", inserts: 1, storageContract: "JSON []byte parameter stored as BLOB", payloadReferences: []string{"request.Kind, request.Payload, recorded"}},
		{path: "internal/sqlitestore/shared_mutation_entity_sequence.go", inserts: 1, storageContract: "JSON []byte parameter stored as BLOB", payloadReferences: []string{"request.Kind, payload, created"}},
		{path: "internal/sqlitestore/shared_mutation_mutation_outbox_core.go", inserts: 2, storageContract: "JSON []byte parameter stored as BLOB", payloadReferences: []string{"mutation.Kind, mutation.Payload, created", "request.Kind, payload, created"}},
		{path: "internal/sqlitestore/shared_relations.go", inserts: 1, storageContract: "json.Marshal []byte parameter stored as BLOB", payloadReferences: []string{`"relation-create", payload`}},
	}
	insertPattern := regexp.MustCompile(`(?i)\bINSERT\s+(?:OR\s+IGNORE\s+)?INTO\s+hub_outbox\b`)
	updatePattern := regexp.MustCompile("(?is)\\bUPDATE\\s+hub_outbox\\s+SET\\b[^`]{0,512}?\\bpayload\\s*=")
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Gate-20 inventory source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../.."))
	want := make(map[string][2]int, len(writers))
	for _, writer := range writers {
		want[writer.path] = [2]int{writer.inserts, writer.updates}
	}
	actual := make(map[string][2]int)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		counts := [2]int{len(insertPattern.FindAll(source, -1)), len(updatePattern.FindAll(source, -1))}
		if counts != [2]int{} {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			actual[filepath.ToSlash(relative)] = counts
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(want) {
		t.Fatalf("Gate-20 outbox writer source inventory has %d files, want %d: actual=%v", len(actual), len(want), actual)
	}
	for _, writer := range writers {
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(writer.path)))
		if err != nil {
			t.Fatal(err)
		}
		if actual[writer.path] != [2]int{writer.inserts, writer.updates} {
			t.Errorf("Gate-20 outbox writer %s has insert/update counts %v, want %d/%d (%s)", writer.path, actual[writer.path], writer.inserts, writer.updates, writer.storageContract)
		}
		for _, reference := range writer.payloadReferences {
			if !strings.Contains(string(source), reference) {
				t.Errorf("Gate-20 outbox writer %s no longer proves %s via %q", writer.path, writer.storageContract, reference)
			}
		}
	}
	for path, counts := range actual {
		if want[path] != counts {
			t.Errorf("unclassified Gate-20 hub_outbox.payload writer %s has insert/update counts %v", path, counts)
		}
	}
}
