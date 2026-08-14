package service

import "testing"

func TestTrainRevisionStatusConflictIsBoundedAndMachineReadable(t *testing.T) {
	err := trainRevisionStatusConflict("precondition", "status", 6, 6, "ready_for_integration")
	conflict, ok := err.(*LifecycleConflictError)
	if !ok || conflict.Code != "TRAIN_REVISION_STATUS_CONFLICT" || conflict.Phase != "precondition" {
		t.Fatalf("conflict=%#v", err)
	}
	if len(conflict.Details) != 4 || conflict.Details["guard"] != "status" || conflict.Details["current_status"] != "ready_for_integration" {
		t.Fatalf("conflict details=%#v", conflict.Details)
	}
}

func TestTrainIntegrationReceiptConflictIsDistinct(t *testing.T) {
	err := trainIntegrationReceiptConflict("receipt", 6, "ready_for_integration", "completed")
	conflict, ok := err.(*LifecycleConflictError)
	if !ok || conflict.Code != "TRAIN_INTEGRATION_RECEIPT_CONFLICT" || conflict.Phase != "receipt" {
		t.Fatalf("conflict=%#v", err)
	}
	if conflict.Details["receipt_status"] != "completed" {
		t.Fatalf("receipt details=%#v", conflict.Details)
	}
}
