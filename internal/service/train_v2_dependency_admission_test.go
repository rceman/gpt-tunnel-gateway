package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func dependencyIntegrationFixture(status string) (model.TrainV2, trainv2.IntegrationReceipt) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	head := strings.Repeat("a", 40)
	reviewID := "GTW-TRN16-ITEM0-REVIEW"
	item := model.TrainV2Item{
		Position: 0, TaskID: "GTW-TSK324", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("b", 64),
		Status: model.TrainV2ItemReviewed, AddedAt: now,
		Attempts:                []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptSucceeded, AgentID: "planner", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: head, StartedAt: now, FinishedAt: &now, ReviewID: reviewID}},
		SuccessfulAttemptNumber: 1,
		Proof:                   &model.TrainV2ImplementationProof{CheckpointHead: head, ImplementationSHA: head, ReportID: reviewID, GateResults: []model.CompletionGateResult{{ID: model.WorkflowGateFormat}, {ID: model.WorkflowGateCheck}, {ID: model.WorkflowGateTest}}, RecordedAt: now},
		Review:                  &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: reviewID, ReviewedAt: now},
	}
	train := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: "GTW-TRN16", ProjectID: "example", Revision: 1, Status: status, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{item}, FullProof: &model.TrainV2FullProof{CandidateHead: head, GateResults: item.Proof.GateResults, RecordedAt: now}}
	if status == model.TrainV2Retired {
		train.Retirement = &model.TrainV2Retirement{PreviousStatus: model.TrainV2Planned, Classification: "historical", Reason: "fixture", ActorSessionID: "SP-TEST1234", RetiredAt: now}
	}
	receipt := trainv2.IntegrationReceipt{SchemaVersion: 1, ProjectID: "example", TrainID: train.ID, BaseRevision: strings.Repeat("c", 40), LaneHead: head, TargetBefore: strings.Repeat("d", 40), IntegrationHead: head, RuntimeHead: head, ProofCandidate: head, PreActivation: "ok", PreSmoke: "ok", PostActivation: "ok", PostSmoke: "ok", Status: "completed", NextAction: "complete", UpdatedAt: now}
	return train, receipt
}

func writeDependencyIntegrationFixture(t *testing.T, status string, withReceipt bool) (string, *Service) {
	t.Helper()
	worktree := t.TempDir()
	s := &Service{}
	train, receipt := dependencyIntegrationFixture(status)
	trainPath := filepath.Join(worktree, filepath.FromSlash(s.trainV2Path("example", train.ID)))
	if err := os.MkdirAll(filepath.Dir(trainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trainPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if withReceipt {
		receiptPath := filepath.Join(worktree, filepath.FromSlash("gpt-tunnel/v1/projects/example/trains-v2/"+train.ID+".integration.json"))
		data, err = json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(receiptPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return worktree, s
}

func TestTrainV2DependencyRequiresCanonicalCompletedIntegration(t *testing.T) {
	task := model.TaskAuthoring{ID: "GTW-TSK330", Dependencies: []string{"GTW-TSK324"}}
	for _, tc := range []struct {
		name          string
		status        string
		withReceipt   bool
		wantSatisfied bool
	}{
		{name: "completed integration", status: model.TrainV2Completed, withReceipt: true, wantSatisfied: true},
		{name: "ready for integration", status: model.TrainV2ReadyForIntegration, withReceipt: true},
		{name: "retired historical record", status: model.TrainV2Retired, withReceipt: true},
		{name: "failed integration", status: model.TrainV2ReadyForIntegration, withReceipt: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worktree, s := writeDependencyIntegrationFixture(t, tc.status, tc.withReceipt)
			err := s.validateTaskDependenciesInWorktree(worktree, "example", []model.TaskAuthoring{task})
			if tc.wantSatisfied {
				if err != nil {
					t.Fatalf("canonical integration rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "dependency-not-integrated") {
				t.Fatalf("dependency admission was not fail-closed: %v", err)
			}
		})
	}
}

func TestTrainV2DependencyDigestMismatchFailsClosed(t *testing.T) {
	worktree, s := writeDependencyIntegrationFixture(t, model.TrainV2Completed, true)
	receiptPath := filepath.Join(worktree, filepath.FromSlash("gpt-tunnel/v1/projects/example/trains-v2/GTW-TRN16.integration.json"))
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"integration_head":"`+strings.Repeat("a", 40), `"integration_head":"`+strings.Repeat("e", 40), 1))
	if err := os.WriteFile(receiptPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	err = s.validateTaskDependenciesInWorktree(worktree, "example", []model.TaskAuthoring{{ID: "GTW-TSK330", Dependencies: []string{"GTW-TSK324"}}})
	if err == nil || !strings.Contains(err.Error(), "dependency-not-integrated") {
		t.Fatalf("digest/source mismatch was not rejected: %v", err)
	}
}
