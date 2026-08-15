package service

import (
	"context"
	"testing"
	"time"
)

func TestProjectRemoveUsesBoundedDurableReceipt(t *testing.T) {
	s, _, revision := registerManagedRemovalProject(t)
	ctx := trustedWorkflowPolicyContext(context.Background(), "planner")
	in := ProjectRemoveInput{ProjectID: "removable", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}

	started := time.Now()
	first, err := s.ProjectRemoveAsync(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("project/remove initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("project/remove initiation latency: %s", elapsed)
	}
	second, err := s.ProjectRemoveAsync(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("project/remove initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed ProjectRemoveReceipt
	for time.Now().Before(deadline) {
		completed, err = s.ProjectRemoveOperationStatus(ctx, first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Result == nil || completed.Result.ProjectID != "removable" {
		t.Fatalf("project/remove worker did not complete: %#v", completed)
	}
}
