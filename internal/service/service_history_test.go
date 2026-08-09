package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestReportReadsRecomputeGitProof(t *testing.T) {
	s, hubRevision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Proof task",
		Objective:          "Verify report proof.",
		Slug:               "proof",
		AcceptanceCriteria: []string{"proof"},
		OperationClass:     "implementation",
		CreatedBy:          "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        "example",
		Title:            planString("Proof"),
		Summary:          planString("Proof"),
		CurrentObjective: planString("Verify proof."),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "proof.txt"), []byte("proof\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "proof.txt")
	testutil.Git(t, project.Root, "commit", "-m", "proof")
	testutil.Git(t, project.Root, "push", "-u", "origin", task.Branch)
	completion := model.Completion{SchemaVersion: 1, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "proof", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	final, _, err := s.RunFinalize(ctx, FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: dispatch.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	coldParent := t.TempDir()
	cold := filepath.Join(coldParent, "cold")
	testutil.Git(t, coldParent, "clone", "--no-local", "--single-branch", "--branch", "main", remote, cold)
	missing := exec.Command("git", "cat-file", "-e", final.Repository.Head+"^{commit}")
	missing.Dir = cold
	if err := missing.Run(); err == nil {
		t.Fatal("cold worktree unexpectedly contains the feature commit")
	}
	project.Root = cold
	s.Config.Projects["example"] = project
	stored, err := s.RunReport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil || !snapshot.Report.Available {
		t.Fatalf("cold review snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
	}
	stored.Repository.ChangedFiles = []string{"injected.txt"}
	if _, err := s.Hub.Transact(ctx, final.HubCommit, "test: tamper report proof", func(worktree string) ([]string, error) {
		path := s.reportPath(run.ProjectID, run.ID)
		return []string{path}, hub.WriteJSON(worktree, path, stored)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunReport(ctx, run.ID); err == nil || !strings.Contains(err.Error(), "changed files") {
		t.Fatalf("tampered report was accepted: %v", err)
	}
	snapshot, err = s.RunReviewSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Report.Available || snapshot.Report.Error == "" {
		t.Fatalf("tampered report was exposed by review snapshot: %#v", snapshot.Report)
	}
}

func mirrorProofReport(t *testing.T, s *Service, project config.ProjectConfig, run model.Run, head string) model.Report {
	t.Helper()
	ancestor, err := s.Git.MirrorAncestor(context.Background(), project, run.BaseRevision, head)
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.Git.MirrorChangedFiles(context.Background(), project, run.BaseRevision, head)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := s.Git.MirrorLog(context.Background(), project, run.BaseRevision, head, s.Config.MaxListItems)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(commits))
	for _, commit := range commits {
		ids = append(ids, commit.SHA)
	}
	return model.Report{Repository: model.RepositoryProof{Branch: run.Branch, Head: head, BaseAncestor: ancestor, Commits: ids, ChangedFiles: files, DiffScope: run.BaseRevision + ".." + head}}
}
