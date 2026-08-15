package service

import (
	"context"
	"testing"
	"time"
)

func TestTrainV2IntegrateAsyncReturnsBoundedIdempotentInitiationReceipt(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	_ = enableTrainV2ForTest(t, s, hubRevision)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}
	started := time.Now()
	first, err := s.TrainV2IntegrateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("train/integrate initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("train/integrate initiation latency: %s", elapsed)
	}
	second, err := s.TrainV2IntegrateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("train/integrate initiation was not idempotent: first=%#v second=%#v", first, second)
	}
	if _, err := s.TrainV2IntegrateOperationStatus(context.Background(), first.OperationID); err != nil {
		t.Fatal(err)
	}
}
