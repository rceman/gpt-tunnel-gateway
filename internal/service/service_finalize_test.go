package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestSyntheticFailureUsesDurableBaseProof(t *testing.T) {
	s, hubRevision, _ := testService(t)
	ctx := context.Background()
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Durable failure",
		Objective:          "Keep synthetic proof durable.",
		Slug:               "durable-failure",
		AcceptanceCriteria: []string{"durable"},
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
		Title:            planString("Durable failure"),
		Summary:          planString("Durable failure"),
		CurrentObjective: planString("Use durable proof."),
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
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "unpublished.txt"), []byte("unpublished\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "unpublished.txt")
	testutil.Git(t, project.Root, "commit", "-m", "unpublished local commit")
	expected, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.failRun(ctx, run, task, "failed", "synthetic timeout", expected); err != nil {
		t.Fatal(err)
	}
	report, err := s.RunReport(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Repository.Head != run.BaseRevision || len(report.Repository.Commits) != 0 || len(report.Repository.ChangedFiles) != 0 {
		t.Fatalf("synthetic report used non-durable proof: %#v", report.Repository)
	}
	foundRisk := false
	for _, risk := range report.RemainingRisks {
		if strings.Contains(risk, "published task branch was absent") || strings.Contains(risk, "unpublished") {
			foundRisk = true
		}
	}
	if !foundRisk {
		t.Fatalf("synthetic report omitted bounded durability risk: %#v", report.RemainingRisks)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil || !snapshot.Report.Available {
		t.Fatalf("synthetic report failed review snapshot validation: available=%v err=%v", snapshot.Report.Available, err)
	}
	project = s.Config.Projects["example"]
	remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
	coldParent := t.TempDir()
	cold := filepath.Join(coldParent, "cold")
	testutil.Git(t, coldParent, "clone", "--no-local", "--single-branch", "--branch", "main", remote, cold)
	project.Root = cold
	project.Mirror = filepath.Join(t.TempDir(), "cold-mirror.git")
	s.Config.Projects["example"] = project
	if stored, err := s.RunReport(ctx, run.ID); err != nil || stored.Repository.Head != run.BaseRevision {
		t.Fatalf("cold base fallback report failed: head=%s err=%v", stored.Repository.Head, err)
	}
	if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
		t.Fatalf("cold base fallback snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
	}
}
