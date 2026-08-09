package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSemanticTaskAllowsOmittedBaseRevision(t *testing.T) {
	semantic := Task{
		SchemaVersion: SchemaVersion,
		ID:            "EXM-TSK1",
		ProjectID:     "example",
		Title:         "Semantic task",
		Objective:     "Resolve the execution base at dispatch.",
		Branch:        "task/EXM-TSK1-semantic-task",
		Status:        "created",
		CreatedBy:     "test",
		CreatedAt:     time.Unix(1, 0).UTC(),
	}
	if err := ValidateTask(semantic); err != nil {
		t.Fatalf("semantic task rejected: %v", err)
	}
	hash, err := HashTask(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("semantic task hash is empty")
	}
	payload, err := json.Marshal(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"base_revision"`) {
		t.Fatalf("semantic task serialized a base revision: %s", payload)
	}
}

func TestHistoricalTaskRetainsExplicitBaseRevision(t *testing.T) {
	historical := Task{
		SchemaVersion: SchemaVersion,
		ID:            "EXM-TSK1",
		ProjectID:     "example",
		Title:         "Historical task",
		Objective:     "Retain the recorded execution base.",
		Branch:        "task/EXM-TSK1-historical-task",
		BaseRevision:  strings.Repeat("a", 40),
		Status:        "created",
		CreatedBy:     "test",
		CreatedAt:     time.Unix(1, 0).UTC(),
	}
	if err := ValidateTask(historical); err != nil {
		t.Fatalf("historical task rejected: %v", err)
	}
	payload, err := json.Marshal(historical)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"base_revision":"`+historical.BaseRevision+`"`) {
		t.Fatalf("historical task lost its explicit base: %s", payload)
	}
}
