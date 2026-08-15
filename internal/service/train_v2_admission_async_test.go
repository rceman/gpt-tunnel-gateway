package service

import (
	"context"
	"testing"
	"time"
)

func TestTrainV2AdmissionMutationsReturnBoundedReceipts(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()
	create := TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{"EXM-TSK1"},
		CreatedBy: "planner",
	}

	started := time.Now()
	first, err := s.TrainV2CreateAsync(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("train/create initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/create initiation latency: %s", elapsed)
	}
	second, err := s.TrainV2CreateAsync(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("train/create initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	add := TrainV2AddInput{
		ProjectID:        "example",
		TrainID:          "EXM-TRN1",
		TaskIDs:          []string{"EXM-TSK1"},
		ExpectedRevision: 1,
		AddedBy:          "planner",
	}
	started = time.Now()
	addFirst, err := s.TrainV2AddAsync(ctx, add)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("train/add initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/add initiation latency: %s", elapsed)
	}
	addSecond, err := s.TrainV2AddAsync(ctx, add)
	if err != nil {
		t.Fatal(err)
	}
	if addFirst.OperationID == "" || addFirst.OperationID != addSecond.OperationID {
		t.Fatalf("train/add initiation was not idempotent: first=%#v second=%#v", addFirst, addSecond)
	}
	for _, operation := range []struct {
		id   string
		kind string
	}{
		{id: first.OperationID, kind: "train-v2-create"},
		{id: addFirst.OperationID, kind: "train-v2-add"},
	} {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			receipt, statusErr := s.TrainV2AdmissionOperationStatus(ctx, operation.id, operation.kind)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if receipt.Status == "completed" || receipt.Status == "failed" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
