package service

import (
	"context"
	"strings"
	"testing"
	"time"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK608OperationReadAwaitTimeoutAndOutcomeUnknown(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	session := tsk585PlannerSession(t, s)
	ctx := WithAgentSessionID(context.Background(), session)
	now := time.Now().UTC()
	operation, err := db.AllocateLocalOperation(ctx, "example", "EXM", strings.Repeat("1", 64), "task-execution-test", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(operation.OperationID, "EXM-OPR") || strings.Contains(operation.OperationID, strings.Repeat("1", 64)) {
		t.Fatalf("public operation identity leaked mutation hash: %#v", operation)
	}
	if _, err := s.OperationRead(ctx, "mutation-"+strings.Repeat("1", 64)); err == nil {
		t.Fatal("operation/read accepted an internal mutation identity")
	}
	durable := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operation.OperationID,
		MutationID:    operation.MutationID,
		Kind:          operation.Kind,
		RequestSHA256: operation.MutationID,
		SessionID:     session,
		ProjectID:     operation.ProjectID,
		Input:         []byte(`{}`),
		Status:        "accepted",
		CreatedAt:     operation.CreatedAt,
		UpdatedAt:     operation.UpdatedAt,
	}
	if err := s.writeDurableMutation(durable); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.OperationRead(ctx, operation.OperationID)
	if err != nil || accepted.Status != "accepted" {
		t.Fatalf("JSON write did not synchronize Local receipt: %#v err=%v", accepted, err)
	}
	operation.Status = "running"
	operation.UpdatedAt = now.Add(time.Millisecond)
	if err := db.UpdateLocalOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	timedOut, err := s.OperationAwait(ctx, operation.OperationID, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if timedOut.Status != "running" {
		t.Fatalf("timeout result=%#v", timedOut)
	}
	completedOperation := operation
	go func() {
		completedOperation.Status = "completed"
		completedOperation.ResultPayload = []byte(`{"ok":true}`)
		completedOperation.UpdatedAt = time.Now().UTC()
		_ = db.UpdateLocalOperation(context.Background(), completedOperation)
	}()
	completed, err := s.OperationAwait(ctx, operation.OperationID, time.Second)
	if err != nil || completed.Status != "completed" || string(completed.Result) != `{"ok":true}` {
		t.Fatalf("running-to-terminal result=%#v err=%v", completed, err)
	}
	terminal, err := s.OperationAwait(ctx, operation.OperationID, time.Second)
	if err != nil || terminal.Status != "completed" {
		t.Fatalf("already-terminal result=%#v err=%v", terminal, err)
	}
	operation.Status = "outcome_unknown"
	operation.Error = "uncertain"
	operation.RecoveryReason = "recovered"
	operation.UpdatedAt = time.Now().UTC()
	if err := db.UpdateLocalOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	unknown, err := s.OperationRead(ctx, operation.OperationID)
	if err != nil || unknown.Status != "outcome_unknown" || unknown.RecoveryReason != "recovered" {
		t.Fatalf("outcome_unknown result=%#v err=%v", unknown, err)
	}
}

func TestTSK608OperationReadPreservesProjectIsolation(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	exampleSession := tsk585PlannerSession(t, s)
	other, err := durableSession.NewStoreWithDurability(db).Create(durableSession.CreateInput{
		ProjectID: "other", ProjectCode: "OTH", Role: durableSession.RolePlanner, SessionType: durableSession.SessionTypeChatGPT,
	})
	if err != nil {
		t.Fatal(err)
	}
	exampleOp, err := db.AllocateLocalOperation(context.Background(), "example", "EXM", strings.Repeat("2", 64), "task-execution-test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OperationRead(WithAgentSessionID(context.Background(), other.ID), exampleOp.OperationID); err == nil {
		t.Fatal("cross-project operation/read succeeded")
	}
	if _, err := s.OperationRead(WithAgentSessionID(context.Background(), exampleSession), exampleOp.OperationID); err != nil {
		t.Fatal(err)
	}
}
