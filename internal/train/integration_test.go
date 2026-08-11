package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func reviewedTrainForIntegration(t *testing.T) model.TrainV2 {
	t.Helper()
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	gate := []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0}}
	proof := &model.TrainV2ImplementationProof{CheckpointHead: strings.Repeat("a", 40), ImplementationSHA: strings.Repeat("b", 40), ReportID: "GTW-TSK179-RUN1-REPORT", GateResults: gate, RecordedAt: now}
	train := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: "GTW-TRN7", ProjectID: "gateway", Revision: 4, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "GTW-TSK179", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("c", 64), Status: model.TrainV2ItemReviewed, AddedAt: now, RunID: "GTW-TSK179-RUN1", AgentID: "agent-1", StartHead: strings.Repeat("9", 40), Proof: proof, Review: &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: "GTW-TSK179-RUN1-REVIEW", ReviewedAt: now}}}}
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	return train
}

func TestRecordFullProofBindsExactLaneAndPlansStrictFastForward(t *testing.T) {
	now := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)
	lane := strings.Repeat("b", 40)
	train, err := RecordFullProof(reviewedTrainForIntegration(t), lane, []model.CompletionGateResult{{ID: model.WorkflowGateTest, ExitCode: 0}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if train.Status != model.TrainV2ReadyForIntegration || train.FullProof == nil || train.FullProof.CandidateHead != lane {
		t.Fatalf("unexpected full proof state: %#v", train)
	}
	plan, err := PlanIntegration(train, strings.Repeat("a", 40), true)
	if err != nil || plan.Status != "fast_forward_required" || plan.Reconciliation {
		t.Fatalf("unexpected strict FF plan: %#v %v", plan, err)
	}
	completed, err := MarkIntegrated(train, lane, lane, now.Add(time.Minute))
	if err != nil || completed.Status != model.TrainV2Completed {
		t.Fatalf("integration completion failed: %#v %v", completed, err)
	}
}

func TestIntegrationTargetDivergenceRequiresReconciliationWithoutMutationPlan(t *testing.T) {
	train, err := RecordFullProof(reviewedTrainForIntegration(t), strings.Repeat("b", 40), []model.CompletionGateResult{{ID: model.WorkflowGateFormat, ExitCode: 0}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanIntegration(train, strings.Repeat("d", 40), false)
	if err != nil || !plan.Reconciliation || plan.Status != "reconciliation_required" || plan.NextAction != "create_train_reconciliation_receipt" {
		t.Fatalf("unexpected reconciliation plan: %#v %v", plan, err)
	}
	if train.Status != model.TrainV2ReadyForIntegration {
		t.Fatal("planning divergence mutated Train state")
	}
}
