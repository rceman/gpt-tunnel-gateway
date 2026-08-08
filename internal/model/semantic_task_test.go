package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSemanticTaskEmptyBaseAndHistoricalBaseRoundTrip(t *testing.T) {
	historical := Task{
		SchemaVersion: SchemaVersion, ID: "EXM-TSK7", ProjectID: "example", Title: "Historical task",
		Objective: "Preserve a previously pinned task.", Branch: "task/EXM-TSK7-historical",
		BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"compatibility"},
		Status: "created", CreatedBy: "test", CreatedAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	}
	hash, err := HashTask(historical)
	if err != nil {
		t.Fatal(err)
	}
	historical.SHA256 = hash
	if err := ValidateTask(historical); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(historical)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Task
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.BaseRevision != historical.BaseRevision || decoded.SHA256 != historical.SHA256 {
		t.Fatalf("historical task changed during read/decode: got=%#v want=%#v", decoded, historical)
	}

	semantic := historical
	semantic.ID = "EXM-TSK8"
	semantic.Branch = "task/EXM-TSK8-semantic"
	semantic.BaseRevision = ""
	semantic.SHA256, err = HashTask(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTask(semantic); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"base_revision"`) {
		t.Fatalf("semantic task serialized a creation-time base: %s", encoded)
	}
}
