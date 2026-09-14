package service

import (
	"context"
	"testing"
)

func TestTrainV2IntegrateAsyncReturnsBoundedIdempotentInitiationReceipt(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	_ = testServiceWithDurability(t, s)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}
	first, err := s.TrainV2IntegrateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
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
	waitDurableMutationTerminal(t, s, first.OperationID)
}
