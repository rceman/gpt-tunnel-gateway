package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func seedFullProofTrain(t *testing.T, s *Service, expected, head string) (model.TrainV2, string) {
	t.Helper()
	now := time.Now().UTC()
	finished := now.Add(time.Minute)
	reviewID := "GTW-TRN999-ITEM0-ATTEMPT1-REVIEW"
	gates := []model.CompletionGateResult{{ID: model.WorkflowGateFormat, ExitCode: 0}, {ID: model.WorkflowGateCheck, ExitCode: 0}, {ID: model.WorkflowGateTest, ExitCode: 0}}
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion,
		ID:            "GTW-TRN999",
		ProjectID:     "example",
		Revision:      1,
		Status:        model.TrainV2Running,
		CreatedBy:     "planner",
		CreatedAt:     now,
		UpdatedAt:     now,
		Items: []model.TrainV2Item{{
			Position:                0,
			TaskID:                  "GTW-TSK277",
			TaskRevision:            1,
			TaskRevisionSHA256:      strings.Repeat("a", 64),
			Status:                  model.TrainV2ItemReviewed,
			AddedAt:                 now,
			SuccessfulAttemptNumber: 1,
			Attempts: []model.TrainV2Attempt{{
				Number:            1,
				Status:            model.TrainV2AttemptSucceeded,
				AgentID:           "agent",
				AirelaySessionKey: "session",
				GatewayID:         "test_gateway",
				StartHead:         head,
				StartedAt:         now,
				FinishedAt:        &finished,
				ReviewID:          reviewID,
			}},
			Proof: &model.TrainV2ImplementationProof{
				CheckpointHead:    head,
				ImplementationSHA: head,
				ReportID:          "report-1",
				GateResults:       gates,
				RecordedAt:        finished,
			},
			Review: &model.TrainV2ItemReview{Outcome: model.ReviewOutcomeAccepted, ReportID: reviewID, ReviewedAt: finished},
		}},
	}
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	worktree := trainv2.ExpectedWorktreePath(s.Config.StateDir, "example", train.ID)
	if err := os.MkdirAll(filepath.Dir(worktree), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, s.Config.StateDir, "clone", s.Config.Projects["example"].Root, worktree)
	start := model.TrainV2StartRecord{
		SchemaVersion:             model.TrainV2StartSchemaVersion,
		ProjectID:                 "example",
		TrainID:                   train.ID,
		Status:                    model.TrainV2StartActive,
		IntegrationBranch:         "main",
		BaseRevision:              head,
		LaneBranch:                "main",
		CurrentItemPosition:       0,
		CurrentAttemptNumber:      1,
		CurrentTaskID:             "GTW-TSK277",
		CurrentTaskRevision:       1,
		CurrentTaskRevisionSHA256: strings.Repeat("a", 64),
		StartedAt:                 now,
	}
	startPath := hub.ProtocolRoot + "/projects/example/train-v2-starts/" + train.ID + ".json"
	tx, err := s.Hub.Transact(context.Background(), expected, "test: seed full-proof Train", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, s.trainV2Path("example", train.ID), train); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, startPath, start); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path("example", train.ID), startPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "example", TrainID: train.ID, WorktreePath: worktree, AgentID: "agent", SessionKey: "session", ItemPosition: 0, TaskID: "GTW-TSK277", AttemptNumber: 1, StartedAt: now}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, "example", train.ID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	return train, tx.After
}

func TestTrainV2FullProofAsyncTransitionsTerminalReviewedTrain(t *testing.T) {
	s, revision, projectHead := testServiceWithoutIdentifiers(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := seedFullProofTrain(t, s, revision, projectHead)
	in := TrainV2FullProofInput{
		ProjectID: "example",
		TrainID:   train.ID,
	}
	first, err := s.TrainV2FullProofAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TrainV2FullProofAsync(context.Background(), in)
	if err != nil || first.OperationID != second.OperationID {
		t.Fatalf("same full-proof state was not idempotent: %#v %#v %v", first, second, err)
	}
	waitDurableMutationTerminal(t, s, first.OperationID)
	status, err := s.TrainV2FullProofOperationStatus(context.Background(), first.OperationID)
	if err != nil || status.Status != "completed" {
		t.Fatalf("full-proof receipt=%#v err=%v", status, err)
	}
	updated, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil || updated.Status != model.TrainV2ReadyForIntegration || updated.FullProof == nil {
		t.Fatalf("full-proof did not transition Train: %#v err=%v", updated, err)
	}
	if updated.FullProof.CandidateHead != projectHead {
		t.Fatalf("full-proof candidate=%q want=%q", updated.FullProof.CandidateHead, projectHead)
	}
	restarted := New(s.Config)
	if _, err := restarted.TrainV2FullProofOperationStatus(context.Background(), first.OperationID); err != nil {
		t.Fatalf("receipt was not readable after service restart: %v", err)
	}
	changed, err := s.TrainV2FullProofAsync(context.Background(), in)
	if err != nil || changed.OperationID == first.OperationID {
		t.Fatalf("changed Hub state reused stale full-proof receipt: %#v %v", changed, err)
	}
	waitDurableMutationTerminal(t, s, changed.OperationID)
}

func TestTrainV2FullProofCandidateRejectsIncompleteOrInconsistentEvidence(t *testing.T) {
	now := time.Now().UTC()
	base := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: "GTW-TRN999", ProjectID: "example", Revision: 1, Status: model.TrainV2Running, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "GTW-TSK277", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("b", 64), Status: model.TrainV2ItemReviewed, AddedAt: now}}}
	if _, err := trainV2FullProofCandidate(base); err == nil {
		t.Fatal("missing proof was accepted")
	}
	base.Items[0].Status = model.TrainV2ItemQueued
	if _, err := trainV2FullProofCandidate(base); err == nil {
		t.Fatal("queued item was accepted")
	}
}
