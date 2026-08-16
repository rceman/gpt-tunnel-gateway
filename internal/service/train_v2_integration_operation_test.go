package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func seedTrainIntegrationOperation(t *testing.T, s *Service, expected string, operation trainv2.IntegrationOperation) {
	t.Helper()
	if _, err := s.Hub.Transact(context.Background(), expected, "test: seed Train integration operation", func(worktree string) ([]string, error) {
		path := trainV2IntegrationOperationPath(operation.ProjectID, operation.TrainID)
		if err := hub.WriteJSON(worktree, path, operation); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationOperationReplacesUntouchedStalePrePendingWithCurrentIdentity(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
	}
	oldSource := strings.Repeat("a", 40)
	oldTarget := strings.Repeat("b", 40)
	currentSource := strings.Repeat("c", 40)
	currentTarget := strings.Repeat("d", 40)
	oldDigest, oldOperationID, err := integrationRequestDigest(in, oldSource, "main", oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	old := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   oldOperationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		RequestSHA256: oldDigest,
		SourceHead:    oldSource,
		TargetBranch:  "main",
		TargetBefore:  oldTarget,
		Phase:         trainv2.IntegrationPhasePrePending,
		UpdatedAt:     time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, old)

	if _, err := s.integrationOperation(context.Background(), in, currentSource, "main", currentTarget, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "recovery_required") {
		t.Fatalf("stale operation did not fail closed: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), in.ProjectID, in.TrainID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OperationID == old.OperationID || current.SourceHead != currentSource || current.TargetBefore != currentTarget || current.Phase != trainv2.IntegrationPhasePrePending {
		t.Fatalf("current recovery operation=%#v", current)
	}
	if current.SupersedesOperationID != old.OperationID {
		t.Fatalf("current operation did not identify superseded operation: %#v", current)
	}
	history, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(in.ProjectID, in.TrainID, old.OperationID))
	if err != nil {
		t.Fatal(err)
	}
	var archived trainv2.IntegrationOperation
	if err := json.Unmarshal(history, &archived); err != nil {
		t.Fatal(err)
	}
	if archived.OperationID != old.OperationID || archived.SourceHead != oldSource || archived.RequestSHA256 != oldDigest {
		t.Fatalf("archived stale operation=%#v", archived)
	}
	resumed, err := s.integrationOperation(context.Background(), in, currentSource, "main", currentTarget, time.Now().UTC())
	if err != nil {
		t.Fatalf("current operation did not resume after guarded recovery: %v", err)
	}
	if resumed.OperationID != current.OperationID || resumed.SupersedesOperationID != old.OperationID {
		t.Fatalf("resumed operation=%#v", resumed)
	}
}

func TestIntegrationOperationDoesNotRotateStalePrePendingWithPersistedHookEvidence(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN998",
	}
	oldSource := strings.Repeat("a", 40)
	oldTarget := strings.Repeat("b", 40)
	digest, operationID, err := integrationRequestDigest(in, oldSource, "main", oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	old := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   operationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		RequestSHA256: digest,
		SourceHead:    oldSource,
		TargetBranch:  "main",
		TargetBefore:  oldTarget,
		Phase:         trainv2.IntegrationPhasePrePending,
		PreResult:     "pre-hook evidence",
		UpdatedAt:     time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, old)

	if _, err := s.integrationOperation(context.Background(), in, strings.Repeat("c", 40), "main", strings.Repeat("d", 40), time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "durable identity does not match") {
		t.Fatalf("operation with persisted hook evidence was not rejected: %v", err)
	}
	current, err := s.readIntegrationOperation(context.Background(), in.ProjectID, in.TrainID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OperationID != old.OperationID || current.SourceHead != oldSource || current.PreResult != old.PreResult {
		t.Fatalf("unsafe stale operation mutation=%#v", current)
	}
	if _, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath(in.ProjectID, in.TrainID, old.OperationID)); err == nil {
		t.Fatal("unsafe stale operation created recovery history")
	}
}

func TestIntegrationOperationResumesPostPendingAfterTargetAdvanced(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN997",
	}
	source := strings.Repeat("c", 40)
	oldTarget := strings.Repeat("b", 40)
	digest, operationID, err := integrationRequestDigest(in, source, "main", oldTarget)
	if err != nil {
		t.Fatal(err)
	}
	operation := trainv2.IntegrationOperation{
		SchemaVersion: 1,
		OperationID:   operationID,
		ProjectID:     in.ProjectID,
		TrainID:       in.TrainID,
		RequestSHA256: digest,
		SourceHead:    source,
		TargetBranch:  "main",
		TargetBefore:  oldTarget,
		Phase:         trainv2.IntegrationPhasePostPending,
		PreResult:     "pre-hook evidence",
		UpdatedAt:     time.Now().UTC(),
	}
	seedTrainIntegrationOperation(t, s, revision, operation)

	resumed, err := s.integrationOperation(context.Background(), in, source, "main", source, time.Now().UTC())
	if err != nil {
		t.Fatalf("post-pending operation did not resume after target advance: %v", err)
	}
	if resumed.OperationID != operationID || resumed.RequestSHA256 != digest || resumed.TargetBefore != oldTarget || resumed.PreResult != operation.PreResult || resumed.Phase != trainv2.IntegrationPhasePostPending {
		t.Fatalf("post-pending operation was rewritten: %#v", resumed)
	}
}
