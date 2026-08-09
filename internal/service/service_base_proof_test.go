package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestSyntheticExactBaseFallbackFromPreviousFeature(t *testing.T) {
	s, hubRevision, mainHead := testService(t)
	ctx := context.Background()
	project := s.Config.Projects["example"]
	testutil.Git(t, project.Root, "switch", "-c", "feature/previous-review")
	if err := os.WriteFile(filepath.Join(project.Root, "previous-base.txt"), []byte("previous base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "previous-base.txt")
	testutil.Git(t, project.Root, "commit", "-m", "previous review base")
	testutil.Git(t, project.Root, "push", "origin", "feature/previous-review")
	base := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(project.Root, "previous-review.txt"), []byte("previous review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "previous-review.txt")
	testutil.Git(t, project.Root, "commit", "-m", "previous review continuation")
	testutil.Git(t, project.Root, "push", "origin", "feature/previous-review")
	arbitraryHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	testutil.Git(t, project.Root, "switch", "main")
	if mainHead == base || base == arbitraryHead {
		t.Fatal("previous feature fixture did not create distinct commits")
	}

	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Exact base fallback",
		Objective:          "Accept exact immutable base proof.",
		Slug:               "exact-base-fallback",
		AcceptanceCriteria: []string{"base proof"},
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
		Title:            planString("Exact base fallback"),
		Summary:          planString("Exact base fallback"),
		CurrentObjective: planString("Accept exact immutable base proof."),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.failRun(ctx, run, task, "failed", "synthetic preparation failure", mustHubRevision(t, s)); err != nil {
		t.Fatal(err)
	}
	report, err := s.RunReport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repository.Branch != run.Branch || report.Repository.Head != run.BaseRevision || !report.Repository.BaseAncestor || len(report.Repository.Commits) != 0 || len(report.Repository.ChangedFiles) != 0 || report.Repository.DiffScope != run.BaseRevision+".."+run.BaseRevision {
		t.Fatalf("exact-base proof mismatch: %#v", report.Repository)
	}
	if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
		t.Fatalf("exact-base snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
	}
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	defaultHead, exists, err := s.Git.MirrorBranchHead(ctx, project, project.DefaultBranch)
	if err != nil || !exists {
		t.Fatalf("default branch unavailable: head=%s exists=%v err=%v", defaultHead, exists, err)
	}
	if run.BaseRevision != defaultHead {
		t.Fatalf("canonical run base did not use authoritative default head: run=%s default=%s", run.BaseRevision, defaultHead)
	}

	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	coldParent := t.TempDir()
	cold := filepath.Join(coldParent, "cold")
	testutil.Git(t, coldParent, "clone", "--no-local", "--single-branch", "--branch", "main", remote, cold)
	project.Root = cold
	project.Mirror = filepath.Join(t.TempDir(), "cold-mirror.git")
	s.Config.Projects["example"] = project
	if stored, err := s.RunReport(ctx, run.ID); err != nil || stored.Repository.Head != run.BaseRevision {
		t.Fatalf("cold exact-base report failed: head=%s err=%v", stored.Repository.Head, err)
	}
	if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
		t.Fatalf("cold exact-base snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
	}
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	invalid := mirrorProofReport(t, s, project, run, arbitraryHead)
	if err := s.validateCanonicalReportProof(ctx, invalid, run, project); err == nil || !strings.Contains(err.Error(), "reachable") {
		t.Fatalf("non-base absent-branch proof was accepted: %v", err)
	}
}
