package train

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func runningTrainForFinalizeTest(t *testing.T) (model.TrainV2, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	task := readyAdmissionTask(t, "GTW-TSK184", now)
	train, err := New("gateway", "GTW-TRN9", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	train.Status = model.TrainV2Running
	train.Items[0].Status = model.TrainV2ItemRunning
	train.Items[0].RunID = "GTW-TSK184-RUN1"
	train.Items[0].AgentID = "agent-1"
	train.Items[0].StartHead = strings.Repeat("a", 40)
	train.Revision++
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	return train, now
}

func finalizeTestItem(train model.TrainV2, taskID string) (model.TrainV2Item, bool) {
	for _, item := range train.Items {
		if item.TaskID == taskID {
			return item, true
		}
	}
	return model.TrainV2Item{}, false
}

func TestFinalizeRecordsTrainOwnedProofWithoutTaskOrPlanState(t *testing.T) {
	train, now := runningTrainForFinalizeTest(t)
	gates := []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0, Execution: "executed"}}
	updated, err := RecordImplementationProof(train, "GTW-TSK184", "GTW-TSK184-RUN1", "agent-1", strings.Repeat("a", 40), strings.Repeat("b", 40), "GTW-TSK184-RUN1-report", gates, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	item, ok := finalizeTestItem(updated, "GTW-TSK184")
	if !ok || item.Status != model.TrainV2ItemFinalized || item.Proof == nil || item.Proof.ImplementationSHA != strings.Repeat("b", 40) {
		t.Fatalf("Train proof was not recorded: %#v", updated)
	}
	if updated.Status != model.TrainV2ReadyForIntegration {
		t.Fatalf("single finalized item did not make Train integration-ready: %s", updated.Status)
	}
}

func TestFinalizeReviewBindsImmutableItemProofAndBlocksRejectedItem(t *testing.T) {
	train, now := runningTrainForFinalizeTest(t)
	gates := []model.CompletionGateResult{{ID: model.WorkflowGateCheck, ExitCode: 0, Execution: "executed"}}
	finalized, err := RecordImplementationProof(train, "GTW-TSK184", "GTW-TSK184-RUN1", "agent-1", strings.Repeat("a", 40), strings.Repeat("b", 40), "GTW-TSK184-RUN1-report", gates, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := RecordReview(finalized, "GTW-TSK184", model.ReviewOutcomeRejected, "GTW-TSK184-RUN1-REPORT", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	item, ok := finalizeTestItem(reviewed, "GTW-TSK184")
	if !ok || item.Status != model.TrainV2ItemBlocked || item.Proof == nil || item.Review == nil || item.Proof.ImplementationSHA != strings.Repeat("b", 40) {
		t.Fatalf("rejected review did not preserve proof and block item: %#v", reviewed)
	}
}
