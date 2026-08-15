package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestADRCreateAsyncIsBoundedAndIdempotent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	revision = adoptAuthoringIdentifiersForTest(t, s, revision)
	in := ADRCreateInput{
		ADR: model.ADR{
			ProjectID:    "example",
			Title:        "Async decision",
			Status:       "accepted",
			Context:      "context",
			Decision:     "decision",
			Consequences: "consequences",
		},
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	}

	started := time.Now()
	first, err := s.ADRCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("ADR create initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("ADR create initiation latency: %s", elapsed)
	}
	second, err := s.ADRCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("ADR create initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed ADRCreateReceipt
	for time.Now().Before(deadline) {
		completed, err = s.ADRCreateOperationStatus(context.Background(), first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Operation == nil {
		t.Fatalf("ADR create worker did not complete: %#v", completed)
	}
}
