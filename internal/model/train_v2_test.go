package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTrainV2ValidationAndIDAllocation(t *testing.T) {
	id, err := FormatTrainV2ID("GTW", 7)
	if err != nil || id != "GTW-TRN7" {
		t.Fatalf("unexpected train ID: %q %v", id, err)
	}
	code, number, err := ParseTrainV2ID(id)
	if err != nil || code != "GTW" || number != 7 {
		t.Fatalf("unexpected parsed train ID: %q %d %v", code, number, err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	train := TrainV2{SchemaVersion: TrainV2SchemaVersion, ID: id, ProjectID: "gateway", Revision: 1, Status: TrainV2Planned, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []TrainV2Item{{Position: 0, TaskID: "GTW-TSK179", TaskRevision: 2, TaskRevisionSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: TrainV2ItemQueued, AddedAt: now}}}
	if err := ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	train.Items[0].Position = 1
	if err := ValidateTrainV2(train); err == nil {
		t.Fatal("non-contiguous train item position was accepted")
	}
}

func TestTrainV2StartRecordKeepsHostBindingsLocal(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	record := TrainV2StartRecord{
		SchemaVersion:             TrainV2StartSchemaVersion,
		ProjectID:                 "gateway",
		TrainID:                   "GTW-TRN7",
		Status:                    TrainV2StartActive,
		IntegrationBranch:         "main",
		BaseRevision:              strings.Repeat("a", 40),
		LaneBranch:                "train/GTW-TRN7",
		RunID:                     "GTW-TSK179-RUN1",
		CurrentTaskID:             "GTW-TSK179",
		CurrentTaskRevision:       1,
		CurrentTaskRevisionSHA256: strings.Repeat("b", 64),
		StartedAt:                 now,
	}
	if err := ValidateTrainV2StartRecord(record); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"worktree_path", "session_key"} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("portable start record contains host binding %q: %s", forbidden, encoded)
		}
	}
	record.RunID = "GTW-TSK180-RUN1"
	if err := ValidateTrainV2StartRecord(record); err == nil {
		t.Fatal("start record accepted a run for a different current task")
	}
}
