package service

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const (
	orphanTestProject = "example"
	orphanTestTask    = "EXM-TSK185"
	orphanTestRun     = "EXM-TSK185-RUN1"
)

func installOrphanRun(t *testing.T, s *Service, revision string) (string, string) {
	t.Helper()
	run := model.Run{
		SchemaVersion:  model.SchemaVersion,
		ID:             orphanTestRun,
		TaskID:         orphanTestTask,
		TaskSHA256:     strings.Repeat("a", 64),
		ProjectID:      orphanTestProject,
		GatewayID:      s.Config.GatewayID,
		SessionKey:     "example_master",
		Branch:         "feature/orphan-recovery",
		BaseRevision:   strings.Repeat("b", 40),
		HubRevision:    revision,
		Status:         "dispatched",
		CompletionPath: "/tmp/orphan-recovery-completion.json",
		CreatedAt:      time.Now().UTC(),
	}
	path := s.runPath(orphanTestProject, orphanTestRun)
	tx, err := s.Hub.Transact(context.Background(), revision, "test: install orphan run", func(worktree string) ([]string, error) {
		return []string{path}, hub.WriteJSON(worktree, path, run)
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return tx.After, string(raw)
}

func orphanInput(apply bool, revision string) OrphanRunReconcileInput {
	return OrphanRunReconcileInput{
		ProjectID:           orphanTestProject,
		RunID:               orphanTestRun,
		ExpectedHubRevision: revision,
		Actor:               "test-agent",
		Session:             "test-session",
		Reason:              "test explicit orphan reconciliation",
		Apply:               apply,
	}
}

func TestReconcileOrphanRunDryRunDoesNotMutate(t *testing.T) {
	s, revision, _ := testService(t)
	revision, before := installOrphanRun(t, s, revision)
	result, err := s.ReconcileOrphanRun(context.Background(), orphanInput(false, revision))
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied || !result.DryRun || result.State != model.OrphanRunQuarantined {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if got, err := s.Hub.RemoteRevision(context.Background()); err != nil || got != revision {
		t.Fatalf("dry run changed Hub revision: got=%s want=%s err=%v", got, revision, err)
	}
	if got, err := s.Hub.ReadFile(context.Background(), s.runPath(orphanTestProject, orphanTestRun)); err != nil || string(got) != before {
		t.Fatalf("dry run changed orphan run: err=%v", err)
	}
	if _, err := s.Hub.ReadFile(context.Background(), s.orphanRecoveryPath(orphanTestProject, orphanTestRun)); err == nil {
		t.Fatal("dry run created recovery record")
	}
}

func TestReconcileOrphanRunApplyPreservesEvidenceAndPassesStateCheck(t *testing.T) {
	s, revision, _ := testService(t)
	revision, before := installOrphanRun(t, s, revision)
	result, err := s.ReconcileOrphanRun(context.Background(), orphanInput(true, revision))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.AlreadyReconciled || !result.Check.Valid {
		t.Fatalf("unexpected apply result: %#v", result)
	}
	wantPaths := map[string]bool{
		s.runPath(orphanTestProject, orphanTestRun):                   true,
		s.orphanRecoveryPath(orphanTestProject, orphanTestRun):        true,
		s.orphanRecoveryReceiptPath(orphanTestProject, orphanTestRun): true,
	}
	if len(result.ChangedPaths) != len(wantPaths) {
		t.Fatalf("unexpected changed paths: %#v", result.ChangedPaths)
	}
	for _, path := range result.ChangedPaths {
		if !wantPaths[path] {
			t.Fatalf("unrelated path changed: %s", path)
		}
		delete(wantPaths, path)
	}
	if len(wantPaths) != 0 {
		t.Fatalf("missing changed paths: %#v", wantPaths)
	}
	if _, err := s.Hub.ReadFile(context.Background(), s.runPath(orphanTestProject, orphanTestRun)); err == nil {
		t.Fatal("orphan operational run was not removed")
	}
	recoveryRaw, err := s.Hub.ReadFile(context.Background(), s.orphanRecoveryPath(orphanTestProject, orphanTestRun))
	if err != nil {
		t.Fatal(err)
	}
	var recovery model.OrphanRunRecovery
	if err := decodeStrict(recoveryRaw, &recovery); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateOrphanRunRecovery(recovery); err != nil {
		t.Fatal(err)
	}
	evidence, err := base64.StdEncoding.DecodeString(recovery.OriginalRunJSONB64)
	if err != nil || string(evidence) != before {
		t.Fatalf("original run evidence changed: err=%v", err)
	}
	receiptRaw, err := s.Hub.ReadFile(context.Background(), s.orphanRecoveryReceiptPath(orphanTestProject, orphanTestRun))
	if err != nil {
		t.Fatal(err)
	}
	var receipt model.OrphanRunRecoveryReceipt
	if err := decodeStrict(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateOrphanRunRecoveryReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.HubRevisionBefore != revision || receipt.HubRevisionAfter == "" {
		t.Fatalf("missing revision audit: %#v", receipt)
	}
	if _, err := s.TaskReadRecord(context.Background(), orphanTestTask); err == nil {
		t.Fatal("reconciliation created a missing task")
	}
}

func TestReconcileOrphanRunIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	revision, _ = installOrphanRun(t, s, revision)
	first, err := s.ReconcileOrphanRun(context.Background(), orphanInput(true, revision))
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ReconcileOrphanRun(context.Background(), orphanInput(true, current))
	if err != nil {
		t.Fatal(err)
	}
	if !second.Applied || !second.AlreadyReconciled || second.OriginalSHA256 != first.OriginalSHA256 {
		t.Fatalf("unexpected idempotent result: %#v", second)
	}
	if got, err := s.Hub.RemoteRevision(context.Background()); err != nil || got != current {
		t.Fatalf("idempotent reconcile changed Hub: got=%s want=%s err=%v", got, current, err)
	}
}

func TestReconcileOrphanRunRejectsWrongRevisionWithoutMutation(t *testing.T) {
	s, revision, _ := testService(t)
	revision, before := installOrphanRun(t, s, revision)
	result, err := s.ReconcileOrphanRun(context.Background(), orphanInput(true, strings.Repeat("0", 40)))
	if err == nil || !strings.Contains(err.Error(), "HUB_REVISION_CONFLICT") {
		t.Fatalf("wrong revision was not rejected: result=%#v err=%v", result, err)
	}
	if got, err := s.Hub.RemoteRevision(context.Background()); err != nil || got != revision {
		t.Fatalf("wrong revision changed Hub: got=%s want=%s err=%v", got, revision, err)
	}
	if got, err := s.Hub.ReadFile(context.Background(), s.runPath(orphanTestProject, orphanTestRun)); err != nil || string(got) != before {
		t.Fatalf("wrong revision changed orphan run: err=%v", err)
	}
}

func TestReconcileOrphanRunRejectsChangedContent(t *testing.T) {
	s, revision, _ := testService(t)
	revision, before := installOrphanRun(t, s, revision)
	var changed model.Run
	raw, err := s.Hub.ReadFile(context.Background(), s.runPath(orphanTestProject, orphanTestRun))
	if err != nil {
		t.Fatal(err)
	}
	if err := decodeStrict(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed.DispatchMessage = "changed after the caller captured its digest"
	tx, err := s.Hub.Transact(context.Background(), revision, "test: change orphan run content", func(worktree string) ([]string, error) {
		path := s.runPath(orphanTestProject, orphanTestRun)
		return []string{path}, hub.WriteJSON(worktree, path, changed)
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ReconcileOrphanRun(context.Background(), OrphanRunReconcileInput{
		ProjectID:              orphanTestProject,
		RunID:                  orphanTestRun,
		ExpectedHubRevision:    tx.After,
		ExpectedOriginalSHA256: digestBytes([]byte(before)),
		Actor:                  "test-agent",
		Reason:                 "test changed-content rejection",
		Apply:                  true,
	})
	if err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("changed content was not rejected: result=%#v err=%v", result, err)
	}
	if _, err := s.Hub.ReadFile(context.Background(), s.orphanRecoveryPath(orphanTestProject, orphanTestRun)); err == nil {
		t.Fatal("changed-content rejection created recovery record")
	}
}

func TestReconcileOrphanRunCompletesDurablePendingReceipt(t *testing.T) {
	s, revision, _ := testService(t)
	revision, before := installOrphanRun(t, s, revision)
	sourcePath := s.runPath(orphanTestProject, orphanTestRun)
	recoveryPath := s.orphanRecoveryPath(orphanTestProject, orphanTestRun)
	receiptPath := s.orphanRecoveryReceiptPath(orphanTestProject, orphanTestRun)
	digest := digestBytes([]byte(before))
	recovery := model.OrphanRunRecovery{
		SchemaVersion:      model.OrphanRunRecoverySchemaVersion,
		State:              model.OrphanRunQuarantined,
		ProjectID:          orphanTestProject,
		RunID:              orphanTestRun,
		SourcePath:         sourcePath,
		OriginalSHA256:     digest,
		OriginalRunJSONB64: base64.StdEncoding.EncodeToString([]byte(before)),
		Actor:              "test-agent",
		Reason:             "test pending receipt recovery",
		HubRevisionBefore:  revision,
		CreatedAt:          time.Now().UTC(),
	}
	receipt := model.OrphanRunRecoveryReceipt{
		SchemaVersion:     model.OrphanRunRecoverySchemaVersion,
		State:             model.OrphanRunQuarantined,
		ReceiptStatus:     model.OrphanReceiptPending,
		ProjectID:         orphanTestProject,
		RunID:             orphanTestRun,
		SourcePath:        sourcePath,
		OriginalSHA256:    digest,
		Actor:             "test-agent",
		Reason:            "test pending receipt recovery",
		HubRevisionBefore: revision,
		CreatedAt:         time.Now().UTC(),
	}
	if err := model.ValidateOrphanRunRecovery(recovery); err != nil {
		t.Fatal(err)
	}
	if err := model.ValidateOrphanRunRecoveryReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	tx, err := s.Hub.Transact(context.Background(), revision, "test: install pending orphan recovery", func(worktree string) ([]string, error) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(sourcePath))); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, recoveryPath, recovery); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{sourcePath, recoveryPath, receiptPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ReconcileOrphanRun(context.Background(), orphanInput(true, tx.After))
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyReconciled || !result.Applied {
		t.Fatalf("pending receipt was not recovered: %#v", result)
	}
	receiptRaw, err := s.Hub.ReadFile(context.Background(), receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	var completed model.OrphanRunRecoveryReceipt
	if err := decodeStrict(receiptRaw, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.ReceiptStatus != model.OrphanReceiptCompleted || completed.HubRevisionAfter == "" {
		t.Fatalf("pending receipt was not completed: %#v", completed)
	}
}
