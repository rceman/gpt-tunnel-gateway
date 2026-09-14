package service

import (
	"context"
	"testing"
	"time"
)

func TestTaskExecutionMutationsReturnBoundedReceipts(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	ctx := context.Background()

	work, err := s.TaskWorkAsync(ctx, TaskWorkInput{
		ProjectID: "example",
		TaskID:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}

	finalize, err := s.TaskFinalizeAsync(ctx, TaskFinalizeInput{
		ProjectID: "example",
		TaskID:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		id   string
		kind string
	}{
		{id: work.OperationID, kind: "work"},
		{id: finalize.OperationID, kind: "finalize"},
	} {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var done bool
			switch operation.kind {
			case "work":
				receipt, statusErr := s.TaskWorkOperationStatus(ctx, operation.id)
				if statusErr != nil {
					t.Fatal(statusErr)
				}
				done = receipt.Status == "completed" || receipt.Status == "failed"
			case "finalize":
				receipt, statusErr := s.TaskFinalizeOperationStatus(ctx, operation.id)
				if statusErr != nil {
					t.Fatal(statusErr)
				}
				done = receipt.Status == "completed" || receipt.Status == "failed"
			}
			if done {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
