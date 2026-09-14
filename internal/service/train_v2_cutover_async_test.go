package service

import (
	"context"
	"testing"
	"time"
)

func TestTrainV2CutoverReturnsBoundedReceipt(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	receipt, err := s.TrainV2CutoverAsync(context.Background(), TrainV2CutoverInput{
		ProjectID: "example",
		UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		completed, statusErr := s.TrainV2CutoverOperationStatus(context.Background(), receipt.OperationID)
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("train/cutover worker did not reach a terminal receipt")
}
