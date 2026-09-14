package service

import (
	"context"
	"testing"
	"time"
)

func TestTrainV2AdmissionMutationsReturnBoundedReceipts(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	ctx := context.Background()
	create := TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{"EXM-TSK1"},
		CreatedBy: "planner",
	}

	first, err := s.TrainV2CreateAsync(ctx, create)
	if err != nil {
		t.Fatal(err)
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
	addFirst, err := s.TrainV2AddAsync(ctx, add)
	if err != nil {
		t.Fatal(err)
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
