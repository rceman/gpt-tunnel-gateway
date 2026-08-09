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

func TestMirrorReportBranchReachability(t *testing.T) {
	s, _, base := testService(t)
	ctx := context.Background()
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "mirror-proof.txt"), []byte("proof\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "mirror-proof.txt")
	testutil.Git(t, project.Root, "commit", "-m", "mirror proof")
	testutil.Git(t, project.Root, "push", "origin", "main")
	head := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	testutil.Git(t, project.Root, "branch", "feature/mirror")
	testutil.Git(t, project.Root, "push", "origin", "feature/mirror")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	run := model.Run{ProjectID: "example", Branch: "feature/mirror", BaseRevision: base}
	report := mirrorProofReport(t, s, project, run, head)
	if err := s.validateCanonicalReportProof(ctx, report, run, project); err != nil {
		t.Fatalf("published task branch rejected: %v", err)
	}
	testutil.Git(t, project.Root, "push", "origin", "--delete", "feature/mirror")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := s.validateCanonicalReportProof(ctx, report, run, project); err != nil {
		t.Fatalf("deleted task branch with default reachability rejected: %v", err)
	}

	testutil.Git(t, project.Root, "switch", "-c", "feature/unmerged")
	if err := os.WriteFile(filepath.Join(project.Root, "unmerged.txt"), []byte("unmerged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "unmerged.txt")
	testutil.Git(t, project.Root, "commit", "-m", "unmerged proof")
	testutil.Git(t, project.Root, "push", "origin", "feature/unmerged")
	unmergedHead := strings.TrimSpace(testutil.Git(t, project.Root, "rev-parse", "HEAD"))
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	unmergedRun := model.Run{ProjectID: "example", Branch: "feature/unmerged", BaseRevision: base}
	unmergedReport := mirrorProofReport(t, s, project, unmergedRun, unmergedHead)
	testutil.Git(t, project.Root, "push", "origin", "--delete", "feature/unmerged")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	if err := s.validateCanonicalReportProof(ctx, unmergedReport, unmergedRun, project); err == nil || !strings.Contains(err.Error(), "reachable") {
		t.Fatalf("unmerged deleted branch was accepted: %v", err)
	}

	testutil.Git(t, project.Root, "branch", "feature/existing")
	testutil.Git(t, project.Root, "push", "origin", "feature/existing")
	if err := s.Git.Refresh(ctx, project); err != nil {
		t.Fatal(err)
	}
	existingRun := model.Run{ProjectID: "example", Branch: "feature/existing", BaseRevision: base}
	existingReport := mirrorProofReport(t, s, project, existingRun, head)
	if err := s.validateCanonicalReportProof(ctx, existingReport, existingRun, project); err == nil || !strings.Contains(err.Error(), "does not point") {
		t.Fatalf("existing branch at another HEAD was accepted: %v", err)
	}

	absentReport := report
	absentReport.Repository.Head = strings.Repeat("f", 40)
	if err := s.validateCanonicalReportProof(ctx, absentReport, run, project); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("absent report HEAD was accepted: %v", err)
	}
}
