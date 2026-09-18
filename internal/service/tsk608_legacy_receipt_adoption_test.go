package service

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK608AdoptsLegacyDurableMutationReceipt(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{name: "completed", status: "completed"},
		{name: "running", status: "running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, db := tsk585Setup(t)
			defer db.Close()
			session := tsk585PlannerSession(t, s)
			ctx := WithAgentSessionID(context.Background(), session)
			in := TrainV2IntegrateInput{
				ProjectID: "example",
				TrainID:   "GTW-TRN999",
			}
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			digest := durableMutationDigest("train-v2-integrate", session, raw)
			now := time.Now().UTC()
			legacyID := "mutation-" + digest
			legacy := durableMutationOperation{
				SchemaVersion: durableMutationSchemaVersion,
				OperationID:   legacyID,
				Kind:          "train-v2-integrate",
				RequestSHA256: digest,
				SessionID:     session,
				ProjectID:     "example",
				Input:         raw,
				Status:        tt.status,
				CreatedAt:     now.Add(-time.Minute),
				UpdatedAt:     now.Add(-time.Second),
			}
			legacy.Result, err = json.Marshal(map[string]string{"operation_id": legacyID})
			if err != nil {
				t.Fatal(err)
			}
			if err := fsutil.WriteJSONAtomic(durableMutationPath(s.Config.StateDir, legacyID), legacy, 0o600); err != nil {
				t.Fatal(err)
			}
			var executions atomic.Int32
			s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
				executions.Add(1)
				return json.RawMessage(`{"adopted":true}`), nil
			}

			adopted, err := s.enqueueTrainV2Integrate(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			if model.ValidateOperationID(adopted.OperationID) != nil || adopted.OperationID == legacyID {
				t.Fatalf("legacy identity was not replaced: %#v", adopted)
			}
			if tt.status == "completed" {
				if adopted.Status != "completed" || executions.Load() != 0 {
					t.Fatalf("completed legacy receipt was replayed: receipt=%#v executions=%d", adopted, executions.Load())
				}
				var result map[string]string
				if err := json.Unmarshal(adopted.Result, &result); err != nil || result["operation_id"] != adopted.OperationID {
					t.Fatalf("adopted result retained legacy operation identity: result=%s operation=%s err=%v", adopted.Result, adopted.OperationID, err)
				}
			} else {
				waitDurableMutationTerminal(t, s, adopted.OperationID)
				if executions.Load() != 1 {
					t.Fatalf("running legacy receipt was not recovered exactly once: executions=%d", executions.Load())
				}
			}
			local, err := db.ReadLocalOperation(ctx, adopted.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if local.MutationID != digest || local.ProjectID != "example" {
				t.Fatalf("legacy receipt was not mapped to the canonical Local record: %#v", local)
			}
			if _, err := s.readDurableMutation(legacyID); err != nil {
				t.Fatalf("legacy receipt was not retained as readable history: %v", err)
			}
		})
	}
}

func TestTSK608AdoptsLegacyTaskCreateReceipt(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	in := TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Legacy adoption task",
		Summary:            "Adopt an existing task/create receipt.",
		Objective:          "Preserve task/create idempotency across the OPR migration.",
		AcceptanceCriteria: []string{"one canonical operation"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
	}
	normalized, err := normalizeTaskCreateInput(in)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := taskCreateRequestDigest(normalized)
	if err != nil {
		t.Fatal(err)
	}
	legacyID := "task-create-" + digest
	now := time.Now().UTC()
	legacyInput := normalized
	legacyInput.Metadata = map[string]string{"gateway_operation_id": legacyID}
	legacy := TaskCreateOperation{
		SchemaVersion: taskCreateOperationSchemaVersion,
		OperationID:   legacyID,
		RequestSHA256: digest,
		Input:         legacyInput,
		Operation: OperationResult{
			OperationID: legacyID,
			ProjectID:   "example",
			Status:      "completed",
		},
		Status:    "completed",
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Second),
	}
	if err := fsutil.WriteJSONAtomic(taskCreateOperationPath(s.Config.StateDir, legacyID), legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	adopted, err := s.TaskAuthoringCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if model.ValidateOperationID(adopted.OperationID) != nil || adopted.OperationID == legacyID || adopted.Status != "completed" {
		t.Fatalf("legacy task/create receipt was not adopted: %#v", adopted)
	}
	if adopted.Operation.OperationID != adopted.OperationID || adopted.Input.Metadata["gateway_operation_id"] != adopted.OperationID {
		t.Fatalf("adopted task/create result retained legacy operation identity: %#v", adopted)
	}
	local, err := db.ReadLocalOperationByMutation(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	if local.OperationID != adopted.OperationID || local.Status != "completed" {
		t.Fatalf("task/create Local record was not adopted: %#v", local)
	}
	if _, err := s.TaskCreateOperationRead(context.Background(), legacyID); err != nil {
		t.Fatalf("legacy task/create receipt was not retained as readable history: %v", err)
	}
}

// waitSharedOutboxDrained blocks until every shared outbox entry is
// published. The standalone-startup tests never stop the restarted service's
// background workers, so the test must not return while a publish transact
// can still write under the TempDir state directory — count unpublished rows
// directly so deferred retries cannot hide in-flight work.
func waitSharedOutboxDrained(t *testing.T, db *sqlitestore.Databases) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := db.Shared.Query(context.Background(), `SELECT COUNT(*) FROM hub_outbox WHERE published_at IS NULL`)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows.Rows) == 1 && len(rows.Rows[0]) == 1 && rows.Rows[0][0] == int64(0) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("shared outbox did not drain within 10s: %#v", rows.Rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTSK608LegacyDurableReceiptAdoptsOnStandaloneStartup(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	session := tsk585PlannerSession(t, s)
	in := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   "GTW-TRN999",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	digest := durableMutationDigest("train-v2-integrate", session, raw)
	legacyID := "mutation-" + digest
	now := time.Now().UTC()
	legacy := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   legacyID,
		Kind:          "train-v2-integrate",
		RequestSHA256: digest,
		SessionID:     session,
		ProjectID:     "example",
		Input:         raw,
		Status:        "running",
		CreatedAt:     now.Add(-time.Minute),
		UpdatedAt:     now.Add(-time.Second),
	}
	if err := fsutil.WriteJSONAtomic(durableMutationPath(s.Config.StateDir, legacyID), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	var executions atomic.Int32
	restarted.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"adopted":true}`), nil
	}
	restarted.StartBackgroundWorkers()

	var adopted sqlitestore.LocalOperation
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		adopted, err = db.ReadLocalOperationByMutation(context.Background(), digest)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("standalone startup did not adopt legacy receipt: %v", err)
	}
	waitDurableMutationTerminal(t, restarted, adopted.OperationID)
	if executions.Load() != 1 {
		t.Fatalf("standalone startup replayed legacy receipt more than once: %d", executions.Load())
	}
	canonical, err := restarted.readDurableMutation(adopted.OperationID)
	if err != nil || canonical.OperationID != adopted.OperationID || canonical.Status != "completed" {
		t.Fatalf("standalone recovery did not finish canonical receipt: %#v err=%v", canonical, err)
	}
	if _, err := restarted.readDurableMutation(legacyID); err != nil {
		t.Fatalf("legacy receipt was not retained as readable history: %v", err)
	}
	waitSharedOutboxDrained(t, db)
}

func TestTSK608LegacyTaskCreateReceiptAdoptsOnStandaloneStartup(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	in := TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Standalone legacy adoption task",
		Summary:            "Adopt a legacy task/create receipt during startup.",
		Objective:          "Preserve the completed result without replay.",
		AcceptanceCriteria: []string{"one canonical operation"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
	}
	normalized, err := normalizeTaskCreateInput(in)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := taskCreateRequestDigest(normalized)
	if err != nil {
		t.Fatal(err)
	}
	legacyID := "task-create-" + digest
	legacyInput := normalized
	legacyInput.Metadata = map[string]string{"gateway_operation_id": legacyID}
	now := time.Now().UTC()
	legacy := TaskCreateOperation{
		SchemaVersion: taskCreateOperationSchemaVersion,
		OperationID:   legacyID,
		RequestSHA256: digest,
		Input:         legacyInput,
		Operation: OperationResult{
			OperationID: legacyID,
			ProjectID:   "example",
			Status:      "completed",
		},
		Status:    "completed",
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Second),
	}
	if err := fsutil.WriteJSONAtomic(taskCreateOperationPath(s.Config.StateDir, legacyID), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := NewWithDurabilityDeferredWorkers(s.Config, db)
	restarted.StartBackgroundWorkers()

	var adopted sqlitestore.LocalOperation
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		adopted, err = db.ReadLocalOperationByMutation(context.Background(), digest)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("standalone startup did not adopt task/create receipt: %v", err)
	}
	if model.ValidateOperationID(adopted.OperationID) != nil || adopted.Status != "completed" {
		t.Fatalf("standalone task/create adoption was not canonical: %#v", adopted)
	}
	canonical, err := restarted.TaskCreateOperationRead(context.Background(), adopted.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Operation.OperationID != adopted.OperationID || canonical.Input.Metadata["gateway_operation_id"] != adopted.OperationID {
		t.Fatalf("standalone task/create result retained legacy operation identity: %#v", canonical)
	}
	if _, err := restarted.TaskCreateOperationRead(context.Background(), legacyID); err != nil {
		t.Fatalf("legacy task/create receipt was not retained as readable history: %v", err)
	}
	waitSharedOutboxDrained(t, db)
}
