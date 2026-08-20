package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func prepareCanonicalRecoveryIntegration(t *testing.T, trainID string) (*Service, TrainV2IntegrateInput, string) {
	t.Helper()
	s, revision, projectHead := testServiceWithoutIdentifiers(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := reviewBackfillFixture(t)
	train.ID = trainID
	train.Items[0].Status = model.TrainV2ItemReviewed
	train.Items[0].Attempts[0].StartHead = projectHead
	train.Items[0].Proof.CheckpointHead = projectHead
	train.Items[0].Proof.ImplementationSHA = projectHead
	train.Items[0].Review = &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: train.Items[0].Proof.ReportID, ReviewedAt: nowUTC()}
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	branch := "train/" + trainID
	if err := s.Git.CreateTrainWorktree(context.Background(), project, s.Config.StateDir, "example", trainID, branch, projectHead); err != nil {
		t.Fatal(err)
	}
	worktree := trainv2.ExpectedWorktreePath(s.Config.StateDir, "example", train.ID)
	now := nowUTC()
	start := model.TrainV2StartRecord{
		SchemaVersion:             model.TrainV2StartSchemaVersion,
		ProjectID:                 "example",
		TrainID:                   train.ID,
		Status:                    model.TrainV2StartActive,
		IntegrationBranch:         "main",
		BaseRevision:              projectHead,
		LaneBranch:                branch,
		CurrentItemPosition:       0,
		CurrentAttemptNumber:      1,
		CurrentTaskID:             train.Items[0].TaskID,
		CurrentTaskRevision:       train.Items[0].TaskRevision,
		CurrentTaskRevisionSHA256: train.Items[0].TaskRevisionSHA256,
		StartedAt:                 now,
	}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "example", TrainID: train.ID, WorktreePath: worktree, AgentID: "agent", SessionKey: "session", ItemPosition: 0, TaskID: train.Items[0].TaskID, AttemptNumber: 1, StartedAt: now}
	proved, err := trainv2.RecordFullProof(train, projectHead, []model.CompletionGateResult{
		{ID: model.WorkflowGateFormat, ExitCode: 0},
		{ID: model.WorkflowGateCheck, ExitCode: 0},
		{ID: model.WorkflowGateTest, ExitCode: 0},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	startPath := hub.ProtocolRoot + "/projects/example/train-v2-starts/" + train.ID + ".json"
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed canonical recovery integration", func(worktree string) ([]string, error) {
		trainPath := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, trainPath, proved); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, startPath, start); err != nil {
			return nil, err
		}
		return []string{trainPath, startPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, "example", train.ID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	input := TrainV2IntegrateInput{
		ProjectID: "example",
		TrainID:   train.ID,
	}
	seedMigratedRecoveryEvidence(t, s, tx.After, train.ID, projectHead, projectHead)
	return s, input, projectHead
}
