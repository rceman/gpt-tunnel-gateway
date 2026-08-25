package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func seedReviewEvidence(t *testing.T, raw []byte) trainv2.EvidenceStore {
	t.Helper()
	store := persistence.NewLocalEvidenceStore(t.TempDir())
	if _, err := store.WriteAttemptReport(model.TrainV2AttemptReport{TrainID: "GTW-TRN999", TaskID: "GTW-TSK999", AttemptNumber: 1}); err != nil {
		t.Fatal(err)
	}
	path, err := store.AttemptReportID("GTW-TRN999", "GTW-TSK999", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return store
}

func reviewBackfillFixture(t *testing.T) (model.TrainV2, []byte) {
	t.Helper()
	now := nowUTC()
	head := strings.Repeat("a", 40)
	trainID := "GTW-TRN999"
	taskID := "GTW-TSK999"
	reportPath := "/projects/example/train-attempts/" + trainID + "/item-0/attempt-1/report.json"
	report := model.TrainV2AttemptReport{
		SchemaVersion: 1, TrainID: trainID, TaskID: taskID, ItemPosition: 0, AttemptNumber: 1, ProjectID: "example", Status: "succeeded", Summary: "completed",
		GateResults: []model.CompletionGateResult{{ID: model.WorkflowGateFormat}, {ID: model.WorkflowGateCheck}, {ID: model.WorkflowGateTest}},
		Repository:  model.RepositoryProof{Head: head, Branch: "train/example", WorktreeClean: true}, FinishedAt: now,
	}
	item := model.TrainV2Item{Position: 0, TaskID: taskID, TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("b", 64), Status: model.TrainV2ItemFinalized, AddedAt: now, SuccessfulAttemptNumber: 1,
		Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "gpt-review-planner", AirelaySessionKey: "gpt-tunnel-gateway_master", GatewayID: "home_pc", StartHead: head, StartedAt: now, FinishedAt: &now, ReportID: reportPath}},
		Proof:    &model.TrainV2ImplementationProof{CheckpointHead: head, ImplementationSHA: head, ReportID: reportPath, GateResults: report.GateResults, RecordedAt: now}}
	train := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: trainID, ProjectID: "example", Revision: 1, Items: []model.TrainV2Item{item}, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now}
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return train, raw
}

func TestBuildTrainV2ReviewBackfillPlanValidatesImmutableProof(t *testing.T) {
	train, raw := reviewBackfillFixture(t)
	evidence := seedReviewEvidence(t, raw)
	items, err := buildTrainV2ReviewBackfillPlan(train, 0, 0, evidence)
	if err != nil || len(items) != 1 || items[0].ReportSHA256 != digestBytes(raw) || items[0].ReviewedHead == "" {
		t.Fatalf("valid backfill plan failed: %#v err=%v", items, err)
	}
	corrupt := append([]byte{}, raw...)
	corrupt[len(corrupt)-2] ^= 1
	if _, err := buildTrainV2ReviewBackfillPlan(train, 0, 0, seedReviewEvidence(t, corrupt)); err == nil {
		t.Fatal("corrupt report was accepted")
	}
}

func TestApplyTrainV2ReviewBackfillIsIdempotentlyVerifiable(t *testing.T) {
	train, raw := reviewBackfillFixture(t)
	evidence := seedReviewEvidence(t, raw)
	items, err := buildTrainV2ReviewBackfillPlan(train, 0, 0, evidence)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := applyTrainV2ReviewBackfill(&train, items, now); err != nil {
		t.Fatal(err)
	}
	if train.Items[0].Status != model.TrainV2ItemReviewed || train.Items[0].Attempts[0].ReviewID == "" {
		t.Fatalf("review metadata was not applied: %#v", train.Items[0])
	}
	if _, err := buildTrainV2ReviewBackfillPlan(train, 0, 0, evidence); err == nil {
		t.Fatal("already reviewed item was accepted as a fresh migration")
	}
}
