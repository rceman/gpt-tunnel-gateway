package model

import (
	"strings"
	"testing"
	"time"
)

func testTrainAttempt() TrainV2Attempt {
	now := time.Now().UTC()
	finished := now.Add(time.Minute)
	return TrainV2Attempt{
		Number: 1, Status: TrainV2AttemptSucceeded, AgentID: "agent-one",
		AirelaySessionKey: "project_master", GatewayID: "gateway-one",
		StartHead: strings.Repeat("a", 40), StartedAt: now, FinishedAt: &finished,
		LegacyRunRef: &TrainV2LegacyRunRef{RunID: "GTW-TSK1-RUN1", RecordSHA256: strings.Repeat("b", 64), Path: "gpt-tunnel/v1/projects/example/runs/GTW-TSK1-RUN1/run.json"},
	}
}

func TestTrainV2AttemptIsItemLocalAndValidated(t *testing.T) {
	now := time.Now().UTC()
	train := TrainV2{
		SchemaVersion: TrainV2SchemaVersion, ID: "GTW-TRN1", ProjectID: "example", Revision: 1,
		Status: TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
		Items: []TrainV2Item{{Position: 0, TaskID: "GTW-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("c", 64), Status: TrainV2ItemFinalized, AddedAt: now, Attempts: []TrainV2Attempt{testTrainAttempt()}, SuccessfulAttemptNumber: 1}},
	}
	if err := ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	train.Items[0].Attempts[0].Number = 2
	if err := ValidateTrainV2(train); err == nil {
		t.Fatal("non-contiguous item-local attempt was accepted")
	}
}

func TestTrainV2NonQueuedItemWithoutAttemptsIsInvalid(t *testing.T) {
	now := time.Now().UTC()
	train := TrainV2{
		SchemaVersion: TrainV2SchemaVersion, ID: "GTW-TRN1", ProjectID: "example", Revision: 1,
		Status: TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now,
		Items: []TrainV2Item{{Position: 0, TaskID: "GTW-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("c", 64), Status: TrainV2ItemFinalized, AddedAt: now}},
	}
	if err := ValidateTrainV2(train); err == nil {
		t.Fatal("missing Attempts was accepted after hard cutover")
	}
}

func TestTrainV2LegacyReferenceRequiresPathAndDigest(t *testing.T) {
	attempt := testTrainAttempt()
	attempt.LegacyRunRef = &TrainV2LegacyRunRef{RunID: "GTW-TSK1-RUN1"}
	if err := ValidateTrainV2Attempt(attempt); err == nil {
		t.Fatal("incomplete legacy evidence reference was accepted")
	}
}
