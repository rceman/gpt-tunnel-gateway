package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

func TestRecoverRunningDurableMutationForStartup(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	digest := sha256.Sum256([]byte("recovery"))
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   "mutation-" + hex.EncodeToString(digest[:]),
		Kind:          "train-v2-integrate",
		RequestSHA256: hex.EncodeToString(digest[:]),
		ProjectID:     "example",
		Input:         []byte(`{"train_id":"GTW-TRN1"}`),
		Status:        "running",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := s.writeDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
	if err := s.recoverRunningDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.readDurableMutation(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "accepted" || recovered.RecoveryReason == "" || recovered.Error != "" {
		t.Fatalf("recovered operation=%#v", recovered)
	}
}

func TestRecoverRunningTaskCreateForStartup(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	digest := sha256.Sum256([]byte("task recovery"))
	operation := TaskCreateOperation{
		SchemaVersion: taskCreateOperationSchemaVersion,
		OperationID:   "task-create-" + hex.EncodeToString(digest[:]),
		RequestSHA256: hex.EncodeToString(digest[:]),
		Input: TaskAuthoringCreateInput{
			ProjectID: "example",
			Title:     "Recovery test task",
			Objective: "Verify restart recovery.",
			CreatedBy: "planner",
		},
		Status:    "running",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := fsutil.WriteJSONAtomic(taskCreateOperationPath(s.Config.StateDir, operation.OperationID), operation, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.recoverRunningTaskCreate(operation); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.TaskCreateOperationRead(context.Background(), operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "accepted" || recovered.RecoveryReason == "" || recovered.Error != "" {
		t.Fatalf("recovered task operation=%#v", recovered)
	}
}
