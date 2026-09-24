package sqlitestore

import (
	"context"
	"testing"
)

func TestReconstructSharedEntitySequencesFromPortableCurrentState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entities := []struct {
		table string
		id    string
	}{
		{"shared_tasks", "EXM-TSK7"},
		{"shared_adrs", "EXM-ADR4"},
		{"shared_rules", "EXM-RUL8"},
		{"shared_journals", "EXM-JRN9"},
		{"shared_milestones", "EXM-MIL3"},
		{"shared_tracks", "EXM-TRK5"},
	}
	for _, entity := range entities {
		if _, err := db.Shared.Exec(ctx, "INSERT INTO "+entity.table+"(id,revision,payload,updated_at) VALUES(?,1,?,?)", entity.id, []byte(`{"project_id":"example"}`), "2026-09-24T12:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES('track','example','EXM',22)`); err != nil {
		t.Fatal(err)
	}
	if err := db.ReconstructSharedEntitySequences(ctx, "example", "EXM"); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"task": 8, "adr": 5, "rule": 9, "journal": 10, "milestone": 4, "track": 22}
	for entityType, next := range want {
		code, actual, found, err := db.ReadSharedSequence(ctx, entityType, "example")
		if err != nil || !found || code != "EXM" || actual != next {
			t.Fatalf("%s sequence=(%q,%d,%t) err=%v, want next=%d", entityType, code, actual, found, err, next)
		}
	}
}
