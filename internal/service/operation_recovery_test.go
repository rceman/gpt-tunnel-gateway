package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestRecoveredDurableMutationUsesBoundedContextAndPersistsUnknownOutcome(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	s.asyncMutationTimeout = 20 * time.Millisecond
	digest := sha256.Sum256([]byte("bounded recovery"))
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
	s.durableMutationExecutor = func(ctx context.Context, _ durableMutationOperation) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.processDurableMutation(operation.OperationID)
	got, err := s.readDurableMutation(operation.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "outcome_unknown" || got.RecoveryReason == "" {
		t.Fatalf("bounded recovery status=%#v", got)
	}
}

func TestDurableMutationReadsBoundedCapturedStateCompatibility(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	operationID := "mutation-captured-state"
	raw := []byte(`{"schema_version":1,"operation_id":"mutation-captured-state","kind":"train-v2-advance","request_sha256":"` + strings.Repeat("a", 64) + `","project_id":"example","input":{"train_id":"GTW-TRN11"},"status":"failed","captured_state":"revision=unbound;train=GTW-TRN11;item=16;task=GTW-TSK273;attempt=1","created_at":"2026-08-18T00:00:00Z","updated_at":"2026-08-18T00:00:01Z"}`)
	if err := os.MkdirAll(filepath.Dir(durableMutationPath(s.Config.StateDir, operationID)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(durableMutationPath(s.Config.StateDir, operationID), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, operationID)) })
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.CapturedState == "" || operation.Status != "failed" {
		t.Fatalf("captured state compatibility was lost: %#v", operation)
	}
	bad := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"unrelated":true}`)...)
	if err := os.WriteFile(durableMutationPath(s.Config.StateDir, operationID), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readDurableMutation(operationID); err == nil {
		t.Fatal("unrelated durable mutation field was accepted")
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
