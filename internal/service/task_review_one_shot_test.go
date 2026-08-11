package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskReviewOneShotAcceptedPublishesAndAdvancesAtomically(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	report, operation, err := s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       model.ReviewOutcomeAccepted,
		Findings:      []model.ReviewFinding{},
		ScopeCoverage: []model.ReviewScopeCoverage{{Surface: "implementation", Status: "covered", Detail: "Reviewed exact implementation scope."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != model.ReviewOutcomeAccepted || report.NextAction != "reviewed_merge_ready" || operation.Status != "merge_ready" {
		t.Fatalf("unexpected one-shot result: %#v %#v", report, operation)
	}
	if len(operation.Hub.Paths) != 2 || !strings.HasSuffix(operation.Hub.Paths[0], "/review-report.json") || !strings.HasSuffix(operation.Hub.Paths[1], ".state.json") {
		t.Fatalf("one-shot publication was not one report/state transaction: %#v", operation.Hub.Paths)
	}
	state, err := s.taskState(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "merge_ready" || state.ReviewedHead != report.ReviewedHead {
		t.Fatalf("accepted review did not advance exact reviewed head: %#v", state)
	}
	if _, _, err := s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       model.ReviewOutcomeAccepted,
		Findings:      []model.ReviewFinding{},
		ScopeCoverage: []model.ReviewScopeCoverage{},
	}); err == nil {
		t.Fatal("immutable one-shot report was published twice")
	}
}

func TestTaskReviewOneShotRejectedPublishesWithoutMergeReady(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	report, operation, err := s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       model.ReviewOutcomeRejected,
		Findings:      []model.ReviewFinding{{ID: "F1", Severity: "high", Title: "Correction required", Detail: "The bounded correction is not complete."}},
		ScopeCoverage: []model.ReviewScopeCoverage{{Surface: "implementation", Status: "blocked", Detail: "Correction is required before merge."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != model.ReviewOutcomeRejected || report.NextAction != "create_bounded_correction" || operation.Status != "review_report_finalized" {
		t.Fatalf("unexpected rejected one-shot result: %#v %#v", report, operation)
	}
	state, err := s.taskState(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "completed" || state.ReviewedHead != "" {
		t.Fatalf("rejected review changed merge lifecycle: %#v", state)
	}
}

func TestTaskReviewOneShotRejectsSemanticInvalidityBeforeHubMutation(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       "not-an-outcome",
		Findings:      []model.ReviewFinding{},
		ScopeCoverage: []model.ReviewScopeCoverage{},
	})
	if err == nil {
		t.Fatal("invalid one-shot outcome accepted")
	}
	after, readErr := s.Hub.RemoteRevision(context.Background())
	if readErr != nil || after != before {
		t.Fatalf("invalid one-shot review mutated Hub: before=%s after=%s err=%v", before, after, readErr)
	}
}

func TestTaskIntegrateDerivesHeadsPersistsReceiptAndIsIdempotent(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	if _, _, err := s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       model.ReviewOutcomeAccepted,
		Findings:      []model.ReviewFinding{},
		ScopeCoverage: []model.ReviewScopeCoverage{{Surface: "scope", Status: "covered", Detail: "covered"}},
	}); err != nil {
		t.Fatal(err)
	}
	root := s.Config.Projects[task.ProjectID].Root
	testutil.Git(t, root, "push", "origin", task.Branch)
	calls := 0
	s.taskActivator = func(_ context.Context, _ config.ProjectConfig, source string) (TaskActivationResult, error) {
		calls++
		return TaskActivationResult{
			SourceHead: source,
			Activation: "passed",
			Smoke:      "passed",
		}, nil
	}
	receipt, operation, err := s.TaskIntegrate(context.Background(), TaskIntegrationInput{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Merged || receipt.NextAction != "complete" || operation.Status != "merged" || calls != 1 {
		t.Fatalf("unexpected integration result: %#v %#v calls=%d", receipt, operation, calls)
	}
	state, err := s.taskState(context.Background(), task)
	if err != nil || state.Status != "merged" || state.IntegrationHead != receipt.IntegrationHead {
		t.Fatalf("integration state mismatch: %#v err=%v", state, err)
	}
	repeated, _, err := s.TaskIntegrate(context.Background(), TaskIntegrationInput{TaskID: task.ID})
	if err != nil || repeated.IntegrationHead != receipt.IntegrationHead || calls != 1 {
		t.Fatalf("idempotent integration repeated activation: %#v err=%v calls=%d", repeated, err, calls)
	}
}

func TestTaskIntegrateStopsOnNonFastForwardWithoutChangingTaskState(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	if _, _, err := s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       model.ReviewOutcomeAccepted,
		Findings:      []model.ReviewFinding{},
		ScopeCoverage: []model.ReviewScopeCoverage{{Surface: "scope", Status: "covered", Detail: "covered"}},
	}); err != nil {
		t.Fatal(err)
	}
	root := s.Config.Projects[task.ProjectID].Root
	testutil.Git(t, root, "push", "origin", task.Branch)
	testutil.Git(t, root, "switch", "main")
	if err := os.WriteFile(filepath.Join(root, "divergent.txt"), []byte("divergent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "add", "divergent.txt")
	testutil.Git(t, root, "commit", "-m", "diverge integration branch")
	testutil.Git(t, root, "push", "origin", "main")
	testutil.Git(t, root, "switch", task.Branch)
	_, _, err := s.TaskIntegrate(context.Background(), TaskIntegrationInput{TaskID: task.ID})
	if err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("non-fast-forward integration was not rejected: %v", err)
	}
	state, stateErr := s.taskState(context.Background(), task)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if state.Status != "merge_ready" || state.ReviewedHead == "" {
		t.Fatalf("non-fast-forward integration changed task state: %#v", state)
	}
}

func TestTaskIntegrateRetriesPostActivationWithoutRepeatingReviewOrPush(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	if _, _, err := s.TaskReview(context.Background(), TaskReviewInput{
		TaskID:        task.ID,
		RunID:         run.ID,
		Outcome:       model.ReviewOutcomeAccepted,
		Findings:      []model.ReviewFinding{},
		ScopeCoverage: []model.ReviewScopeCoverage{{Surface: "scope", Status: "covered", Detail: "covered"}},
	}); err != nil {
		t.Fatal(err)
	}
	root := s.Config.Projects[task.ProjectID].Root
	testutil.Git(t, root, "push", "origin", task.Branch)
	calls := 0
	s.taskActivator = func(_ context.Context, _ config.ProjectConfig, source string) (TaskActivationResult, error) {
		calls++
		if calls == 1 {
			return TaskActivationResult{}, fmt.Errorf("post activation unavailable")
		}
		return TaskActivationResult{
			SourceHead: source,
			Activation: "passed",
			Smoke:      "passed",
		}, nil
	}
	if _, _, err := s.TaskIntegrate(context.Background(), TaskIntegrationInput{TaskID: task.ID}); err == nil || !strings.Contains(err.Error(), "post activation") {
		t.Fatalf("post activation failure was not surfaced: %v", err)
	}
	state, err := s.taskState(context.Background(), task)
	if err != nil || state.Status != "merge_ready" {
		t.Fatalf("post activation failure changed state: %#v err=%v", state, err)
	}
	receipt, operation, err := s.TaskIntegrate(context.Background(), TaskIntegrationInput{TaskID: task.ID})
	if err != nil || !receipt.Merged || operation.Status != "merged" || calls != 2 {
		t.Fatalf("post activation retry failed: %#v %#v err=%v calls=%d", receipt, operation, err, calls)
	}
}
