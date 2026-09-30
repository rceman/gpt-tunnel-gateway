package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func tsk603RemoteRef(t *testing.T, s *Service, branch string) (string, bool) {
	t.Helper()
	project := s.Config.Projects["example"]
	out := strings.TrimSpace(testutil.Git(t, project.Root, "ls-remote", project.Remote, "refs/heads/"+branch))
	if out == "" {
		return "", false
	}
	fields := strings.Split(out, "\t")
	if len(fields) != 2 || fields[1] != "refs/heads/"+branch {
		t.Fatalf("remote lane ref resolved ambiguously: %q", out)
	}
	return fields[0], true
}

func tsk603LaneHead(t *testing.T, s *Service, key string) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, tsk585LanePath(t, s, key), "rev-parse", "HEAD"))
}

func TestTSK603SubmitPublishesExactOriginArtifact(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk603-publish", "Origin publish fixture")
	tsk585Dispatch(t, s, task.ID)
	lanePath := tsk585LanePath(t, s, task.ID)
	tsk585LaneWrite(t, s, task.ID, "candidate.txt", "submitted bytes\n")
	candidate := tsk603LaneHead(t, s, task.ID)
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("dispatched state found=%v err=%v", found, err)
	}
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
		t.Fatalf("submit code: %v", err)
	}
	remote, exists := tsk603RemoteRef(t, s, state.Branch)
	if !exists || remote != candidate {
		t.Fatalf("origin lane ref=%q exists=%v want %q", remote, exists, candidate)
	}
	remoteTree := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", remote+"^{tree}"))
	laneTree := strings.TrimSpace(testutil.Git(t, lanePath, "rev-parse", candidate+"^{tree}"))
	if remoteTree != laneTree {
		t.Fatalf("origin lane tree=%q want %q", remoteTree, laneTree)
	}
	submitted, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || submitted.Status != model.TaskExecutionAwaitingReview || submitted.Head != candidate {
		t.Fatalf("submitted state=%#v found=%v err=%v", submitted, found, err)
	}
	review, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
	})
	if err != nil || review.Head != candidate[:8] {
		t.Fatalf("origin-backed review=%#v err=%v", review, err)
	}
}

func TestTSK603SubmitFinalizesDirtyLaneCommit(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk603-finalize", "Dirty lane finalization fixture")
	tsk585Dispatch(t, s, task.ID)
	lanePath := tsk585LanePath(t, s, task.ID)
	tsk585LaneWriteUncommitted(t, s, task.ID, "uncommitted.txt", "not yet committed\n")
	baseHead := tsk603LaneHead(t, s, task.ID)
	state, _, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID)
	if err != nil {
		t.Fatalf("submit code with dirty lane: %v", err)
	}
	candidate := tsk603LaneHead(t, s, task.ID)
	if candidate == baseHead || out.Head != candidate[:8] {
		t.Fatalf("finalized head=%q output=%#v want a new commit", candidate, out)
	}
	remote, exists := tsk603RemoteRef(t, s, state.Branch)
	if !exists || remote != candidate {
		t.Fatalf("origin lane ref=%q exists=%v want %q", remote, exists, candidate)
	}
	status := testutil.Git(t, lanePath, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("lane is not clean after finalized submission: %q", status)
	}
}

func TestTSK603SubmitRemoteDivergenceFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk603-diverged", "Remote divergence fixture")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	state, _, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	remoteURL, err := s.Git.RemoteURL(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD^{tree}"))
	foreign := strings.TrimSpace(testutil.Git(t, project.Root, "-c", "user.name=T", "-c", "user.email=t@x", "commit-tree", tree, "-m", "foreign lane head"))
	testutil.Git(t, project.Root, "push", remoteURL, foreign+":refs/heads/"+state.Branch)
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err == nil || !strings.Contains(err.Error(), "origin Task lane diverged") {
		t.Fatalf("diverged remote submit error=%v", err)
	}
	current, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || current.Status != model.TaskExecutionDispatched {
		t.Fatalf("diverged submit changed state=%#v found=%v err=%v", current, found, err)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil || len(phases) != 0 {
		t.Fatalf("diverged submit recorded phases=%v err=%v", phases, err)
	}
}

func TestTSK603SubmitReconcilesLandedRemote(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk603-reconcile", "Landed push reconciliation fixture")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	candidate := tsk603LaneHead(t, s, task.ID)
	state, _, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	remoteURL, err := s.Git.RemoteURL(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, tsk585LanePath(t, s, task.ID), "push", remoteURL, candidate+":refs/heads/"+state.Branch)
	out, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID)
	if err != nil {
		t.Fatalf("submit with landed remote: %v", err)
	}
	if out.Head != candidate[:8] || out.Status != model.TaskExecutionAwaitingReview {
		t.Fatalf("reconciled submission output=%#v", out)
	}
}

func TestTSK603ReworkResubmissionPublishesNewHead(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk603-rework", "Rework resubmission fixture")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "first candidate")
	firstHead := tsk603LaneHead(t, s, task.ID)
	state, _, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	branch := state.Branch
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionReviewDecide(ctx, TaskExecutionReviewDecisionInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
		Decision:  "reject",
		Comment:   "rework",
	}); err != nil {
		t.Fatal(err)
	}
	tsk585LaneCommit(t, s, task.ID, "rework candidate")
	secondHead := tsk603LaneHead(t, s, task.ID)
	if secondHead == firstHead {
		t.Fatal("rework did not produce a new candidate head")
	}
	out, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID)
	if err != nil {
		t.Fatalf("rework submit: %v", err)
	}
	if out.Head != secondHead[:8] {
		t.Fatalf("rework submission output=%#v", out)
	}
	remote, exists := tsk603RemoteRef(t, s, branch)
	if !exists || remote != secondHead {
		t.Fatalf("origin lane after rework=%q exists=%v want %q", remote, exists, secondHead)
	}
	phases, err := db.ReadTaskExecutionPhases(ctx, "example", task.ID, "code")
	if err != nil {
		t.Fatal(err)
	}
	submissions := 0
	for _, phase := range phases {
		if phase.EventKind == "submission" {
			submissions++
		}
	}
	if submissions != 2 {
		t.Fatalf("submission phases=%d want 2 immutable submissions", submissions)
	}
}

func TestTSK603ReviewRequiresOriginBackedHead(t *testing.T) {
	ctx := context.Background()
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk603-review-origin", "Origin review authority fixture")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	candidate := tsk603LaneHead(t, s, task.ID)
	state, _, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionSubmitCode(ctx, "example", task.ID); err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	remoteURL, err := s.Git.RemoteURL(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, remoteURL, "update-ref", "-d", "refs/heads/"+state.Branch)
	if _, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
	}); err == nil || !strings.Contains(err.Error(), "origin Task lane") {
		t.Fatalf("review without origin lane error=%v", err)
	}
	if _, err := s.CodeRead(ctx, CodeReadInput{
		ProjectID: "example",
		Worktree:  state.Worktree,
		Path:      "README.md",
	}); err == nil {
		t.Fatal("local-only artifact was readable as submitted authority")
	}
	testutil.Git(t, remoteURL, "update-ref", "refs/heads/"+state.Branch, candidate)
	if _, err := s.TaskExecutionReview(ctx, TaskExecutionReviewInput{
		ProjectID: "example",
		Key:       task.ID,
		Stage:     "code",
	}); err != nil {
		t.Fatalf("review after restoring origin lane: %v", err)
	}
}

func TestTSK603PreSubmitHookFailurePublishesNothing(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk603-hook-block", "Pre-submit Hook publication fixture")
	tsk585Dispatch(t, s, task.ID)
	tsk585LaneCommit(t, s, task.ID, "candidate")
	candidate := tsk603LaneHead(t, s, task.ID)
	state, _, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	plannerCtx := procedurePlannerContext(t, s)
	root := s.Config.Projects["example"].Root
	script := procedureTestScript(t, root, "#!/bin/sh\nexit 17\n")
	if _, err := s.ConfigProcedureCreate(plannerCtx, ConfigProcedureCreateInput{
		ProjectID: "example",
		Name:      "tsk603_reject_submit",
		Definition: model.ProjectProcedureDefinition{
			Script:  script,
			Summary: "Reject Task submission",
			Guide:   "Blocks the pre-submit transition.",
			Input: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"project": map[string]any{"$ref": "EntityKeyAndReference"}},
				"required":             []any{"project"},
				"additionalProperties": false,
			},
			Output: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		},
		Reason: "Cover pre-submit Hook publication blocking.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigHookBind(plannerCtx, ConfigHookBindInput{
		ProjectID: "example",
		Hook:      model.HookPreTaskSubmit,
		Procedure: "tsk603_reject_submit",
		Reason:    "Cover pre-submit Hook publication blocking.",
	}); err != nil {
		t.Fatal(err)
	}
	workerCtx := tsk585WorkerContext(t, s)
	receipt, err := s.TaskExecutionSubmitAsync(workerCtx, "example", "code")
	if err != nil {
		t.Fatal(err)
	}
	operation := tsk585WaitOperation(t, s, receipt.OperationID)
	if operation.Status != "failed" {
		t.Fatalf("blocked submit operation=%#v", operation)
	}
	if remote, exists := tsk603RemoteRef(t, s, state.Branch); exists {
		t.Fatalf("blocked submit published origin lane ref=%q", remote)
	}
	current, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || current.Status != model.TaskExecutionDispatched || current.Head == candidate {
		t.Fatalf("blocked submit state=%#v found=%v err=%v", current, found, err)
	}
}

func tsk585LaneWriteUncommitted(t *testing.T, s *Service, key, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tsk585LanePath(t, s, key), name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
