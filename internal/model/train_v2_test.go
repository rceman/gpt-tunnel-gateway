package model

import (
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
