package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestTaskExecutionMutationsReturnBoundedReceipts(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()

	started := time.Now()
	work, err := s.TaskWorkAsync(ctx, TaskWorkInput{
		ProjectID: "example",
		TaskID:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("task/work initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("task/work initiation latency: %s", elapsed)
	}

	started = time.Now()
	finalize, err := s.TaskFinalizeAsync(ctx, TaskFinalizeInput{
		ProjectID: "example",
		TaskID:    "EXM-TSK1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("task/finalize initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("task/finalize initiation latency: %s", elapsed)
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

func TestTaskFinalizeDoesNotReacceptTerminalReceipt(t *testing.T) {
	for _, status := range []string{"completed", "failed", "outcome_unknown"} {
		t.Run(status, func(t *testing.T) {
			s, _, _ := testServiceWithoutIdentifiers(t)
			input := TaskFinalizeInput{
				ProjectID: "example",
				TaskID:    "EXM-TSK1",
			}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			digest := durableMutationDigestWithIdentity("task-finalize", "", raw, nil)
			now := time.Now().UTC()
			operation := durableMutationOperation{
				SchemaVersion: durableMutationSchemaVersion,
				OperationID:   "mutation-" + digest,
				Kind:          "task-finalize",
				RequestSHA256: digest,
				ProjectID:     "example",
				Input:         raw,
				Status:        status,
				Error:         "terminal evidence",
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			if err := s.writeDurableMutation(operation); err != nil {
				t.Fatal(err)
			}
			receipt, err := s.TaskFinalizeAsync(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Status != status {
				t.Fatalf("terminal status was reaccepted: got=%q want=%q", receipt.Status, status)
			}
			stored, err := s.readDurableMutation(operation.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != status {
				t.Fatalf("terminal receipt changed: got=%q want=%q", stored.Status, status)
			}
		})
	}
}
