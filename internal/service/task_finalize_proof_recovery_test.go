package service

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func proofRecoveryFixture() (model.TrainV2AttemptReport, model.TrainV2, model.TrainV2Item) {
	when := time.Date(2026, time.August, 14, 9, 0, 0, 0, time.UTC)
	head := "0123456789abcdef0123456789abcdef01234567"
	projectID := "gpt-tunnel-gateway"
	trainID := "GTW-TRN7"
	taskID := "GTW-TSK224"
	item := model.TrainV2Item{
		Position:                0,
		TaskID:                  taskID,
		Status:                  model.TrainV2ItemFinalized,
		SuccessfulAttemptNumber: 1,
		Attempts: []model.TrainV2Attempt{{
			Number:     1,
			Status:     model.TrainV2AttemptSucceeded,
			StartedAt:  when,
			FinishedAt: &when,
		}},
	}
	train := model.TrainV2{ID: trainID, ProjectID: projectID, Items: []model.TrainV2Item{item}}
	report := model.TrainV2AttemptReport{
		SchemaVersion: 1,
		TrainID:       trainID,
		TaskID:        taskID,
		ItemPosition:  0,
		AttemptNumber: 1,
		ProjectID:     projectID,
		Status:        "succeeded",
		Summary:       "completed",
		Repository: model.RepositoryProof{
			Branch:        "train/GTW-TRN7",
			Head:          head,
			WorktreeClean: true,
			BaseAncestor:  true,
			DiffScope:     "implementation",
		},
		ServerGateResults: []model.CompletionGateResult{
			{ID: model.WorkflowGateFormat, ExitCode: 0},
			{ID: model.WorkflowGateCheck, ExitCode: 0},
			{ID: model.WorkflowGateTest, ExitCode: 0},
		},
		FinishedAt: when,
	}
	return report, train, item
}

func TestValidateProofRecoveryReportRequiresExactSuccessfulEvidence(t *testing.T) {
	report, train, item := proofRecoveryFixture()
	if err := validateProofRecoveryReport(report, train.ProjectID, train, item, "reports/attempt.json"); err != nil {
		t.Fatalf("valid recovery report rejected: %v", err)
	}

	tests := map[string]func(*model.TrainV2AttemptReport, *model.TrainV2Item){
		"wrong project":      func(r *model.TrainV2AttemptReport, _ *model.TrainV2Item) { r.ProjectID = "other-project" },
		"wrong attempt":      func(r *model.TrainV2AttemptReport, _ *model.TrainV2Item) { r.AttemptNumber = 2 },
		"failed server gate": func(r *model.TrainV2AttemptReport, _ *model.TrainV2Item) { r.ServerGateResults[0].ExitCode = 1 },
		"missing ancestry":   func(r *model.TrainV2AttemptReport, _ *model.TrainV2Item) { r.Repository.BaseAncestor = false },
		"missing checkpoint": func(r *model.TrainV2AttemptReport, _ *model.TrainV2Item) { r.Repository.Head = "" },
		"wrong attempt status": func(_ *model.TrainV2AttemptReport, i *model.TrainV2Item) {
			i.Attempts[0].Status = model.TrainV2AttemptFailed
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := report
			candidate.ServerGateResults = append([]model.CompletionGateResult(nil), report.ServerGateResults...)
			candidateItem := item
			candidateItem.Attempts = append([]model.TrainV2Attempt(nil), item.Attempts...)
			mutate(&candidate, &candidateItem)
			if err := validateProofRecoveryReport(candidate, train.ProjectID, train, candidateItem, "reports/attempt.json"); err == nil {
				t.Fatal("invalid recovery evidence was accepted")
			}
		})
	}
}

func TestValidateStoredTrainItemProofMatchesReport(t *testing.T) {
	report, train, item := proofRecoveryFixture()
	item.Proof = &model.TrainV2ImplementationProof{
		CheckpointHead:    report.Repository.Head,
		ImplementationSHA: report.Repository.Head,
		ReportID:          "reports/attempt.json",
		GateResults:       append([]model.CompletionGateResult(nil), report.ServerGateResults...),
		RecordedAt:        report.FinishedAt,
	}
	if err := validateStoredTrainItemProof(report, train, item, item.Proof.ReportID, item.TaskID); err != nil {
		t.Fatalf("matching stored proof rejected: %v", err)
	}
	item.Proof.CheckpointHead = "fedcba9876543210fedcba9876543210fedcba98"
	if err := validateStoredTrainItemProof(report, train, item, item.Proof.ReportID, item.TaskID); err == nil {
		t.Fatal("mismatched stored proof was accepted")
	}
}
