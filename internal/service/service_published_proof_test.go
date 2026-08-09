package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestSyntheticPublishedBranchProofSelection(t *testing.T) {
	t.Run("exact local head", func(t *testing.T) {
		s, task, run, _ := dispatchedRun(t, "feature/synthetic-exact")
		ctx := context.Background()
		project := s.Config.Projects["example"]
		if err := os.WriteFile(filepath.Join(project.Root, "published.txt"), []byte("published\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, project.Root, "add", "published.txt")
		testutil.Git(t, project.Root, "commit", "-m", "published synthetic proof")
		testutil.Git(t, project.Root, "push", "origin", task.Branch)
		publishedHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
		if _, err := s.failRun(ctx, run, task, "failed", "synthetic failure", mustHubRevision(t, s)); err != nil {
			t.Fatal(err)
		}
		report, err := s.RunReport(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if report.Repository.Head != publishedHead || !report.Repository.WorktreeClean {
			t.Fatalf("exact published proof mismatch: %#v", report.Repository)
		}
		if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
			t.Fatalf("exact published proof snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
		}
	})

	t.Run("published branch behind local head", func(t *testing.T) {
		s, task, run, _ := dispatchedRun(t, "feature/synthetic-behind")
		ctx := context.Background()
		project := s.Config.Projects["example"]
		if err := os.WriteFile(filepath.Join(project.Root, "published.txt"), []byte("published\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, project.Root, "add", "published.txt")
		testutil.Git(t, project.Root, "commit", "-m", "published synthetic proof")
		testutil.Git(t, project.Root, "push", "origin", task.Branch)
		publishedHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
		if err := os.WriteFile(filepath.Join(project.Root, "local-only.txt"), []byte("local\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, project.Root, "add", "local-only.txt")
		testutil.Git(t, project.Root, "commit", "-m", "unpublished local proof")
		localHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
		if localHead == publishedHead {
			t.Fatal("test did not create a local commit ahead of the published branch")
		}
		if _, err := s.failRun(ctx, run, task, "failed", "synthetic failure", mustHubRevision(t, s)); err != nil {
			t.Fatal(err)
		}
		report, err := s.RunReport(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if report.Repository.Head != publishedHead || report.Repository.WorktreeClean {
			t.Fatalf("published-behind proof mismatch: %#v", report.Repository)
		}
		foundRisk := false
		for _, risk := range report.RemainingRisks {
			if strings.Contains(risk, "local unpublished commits") {
				foundRisk = true
			}
		}
		if !foundRisk {
			t.Fatalf("published-behind risk missing: %#v", report.RemainingRisks)
		}
		if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
			t.Fatalf("published-behind snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
		}
		remote := strings.TrimSpace(testutil.Git(t, project.Root, "remote", "get-url", "origin"))
		coldParent := t.TempDir()
		cold := filepath.Join(coldParent, "cold")
		testutil.Git(t, coldParent, "clone", "--no-local", "--single-branch", "--branch", "main", remote, cold)
		project.Root = cold
		project.Mirror = filepath.Join(t.TempDir(), "cold-mirror.git")
		s.Config.Projects["example"] = project
		if stored, err := s.RunReport(ctx, run.ID); err != nil || stored.Repository.Head != publishedHead {
			t.Fatalf("cold published-behind report failed: head=%s err=%v", stored.Repository.Head, err)
		}
		if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
			t.Fatalf("cold published-behind snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
		}
	})

	t.Run("published branch with dirty local state", func(t *testing.T) {
		s, task, run, _ := dispatchedRun(t, "feature/synthetic-dirty")
		ctx := context.Background()
		project := s.Config.Projects["example"]
		if err := os.WriteFile(filepath.Join(project.Root, "published.txt"), []byte("published\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, project.Root, "add", "published.txt")
		testutil.Git(t, project.Root, "commit", "-m", "published synthetic proof")
		testutil.Git(t, project.Root, "push", "origin", task.Branch)
		publishedHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
		if err := os.WriteFile(filepath.Join(project.Root, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.failRun(ctx, run, task, "failed", "synthetic failure", mustHubRevision(t, s)); err != nil {
			t.Fatal(err)
		}
		report, err := s.RunReport(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if report.Repository.Head != publishedHead || report.Repository.WorktreeClean {
			t.Fatalf("dirty published proof mismatch: %#v", report.Repository)
		}
		foundRisk := false
		for _, risk := range report.RemainingRisks {
			if strings.Contains(risk, "worktree was dirty") {
				foundRisk = true
			}
		}
		if !foundRisk {
			t.Fatalf("dirty-state risk missing: %#v", report.RemainingRisks)
		}
		if snapshot, err := s.RunReviewSnapshot(ctx, run.ID); err != nil || !snapshot.Report.Available {
			t.Fatalf("dirty published proof snapshot failed: available=%v err=%v", snapshot.Report.Available, err)
		}
	})
}
