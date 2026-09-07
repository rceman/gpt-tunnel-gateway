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

func TestCodeTrainBaseUsesCanonicalStartBaseForMultiItemTrain(t *testing.T) {
	f := newLocalCodeFixture(t)
	train := model.TrainV2{
		ID: "GTW-TRN63", ProjectID: "example",
		Items: []model.TrainV2Item{
			{TaskID: "GTW-TSK1", SuccessfulAttemptNumber: 1, Attempts: []model.TrainV2Attempt{{StartHead: f.base, Status: model.TrainV2AttemptSucceeded}}},
			{TaskID: "GTW-TSK2", SuccessfulAttemptNumber: 1, Attempts: []model.TrainV2Attempt{{StartHead: f.current, Status: model.TrainV2AttemptSucceeded}}},
		},
		Status: model.TrainV2ReadyForIntegration,
	}
	base, err := codeTrainBase(train, f.current)
	if err != nil {
		t.Fatal(err)
	}
	if base != f.base {
		t.Fatalf("multi-item Train base=%q want canonical start base %q", base, f.base)
	}
}

func TestLocalCodeInspectionRejectsDirtyWorktree(t *testing.T) {
	f := newLocalCodeFixture(t)
	selector := "WT-MAIN-" + f.current[:8]
	path := filepath.Join(f.root, "tracked.txt")
	if err := os.WriteFile(path, []byte("uncommitted content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "live-untracked.txt"), []byte("untracked needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testutil.Git(t, f.root, "restore", "tracked.txt")
		_ = os.Remove(filepath.Join(f.root, "live-untracked.txt"))
	})

	_, err := f.service.CodeRead(context.Background(), CodeReadInput{
		ProjectID: "example",
		Worktree:  selector,
		Path:      "tracked.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty worktree was not rejected: %v", err)
	}
	live, err := f.service.CodeDiff(context.Background(), CodeDiffInput{
		ProjectID: "example",
		Worktree:  selector,
		Live:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	allDiff := live.Diff
	for pages := 0; live.Pagination != nil; pages++ {
		if pages > 100 {
			t.Fatal("live diff pagination did not terminate")
		}
		live, err = f.service.CodeDiff(context.Background(), CodeDiffInput{
			ProjectID: "example",
			Worktree:  selector,
			Live:      true,
			Cursor:    live.Pagination.NextCursor,
		})
		if err != nil {
			t.Fatal(err)
		}
		if live.Diff == "" {
			t.Fatal("live diff continuation returned an empty page")
		}
		allDiff += live.Diff
	}
	for _, want := range []string{"uncommitted content", "live-untracked.txt", "untracked needle"} {
		if !strings.Contains(allDiff, want) {
			t.Fatalf("live diff omitted %q: %s", want, allDiff)
		}
	}
}

func TestCodeDiffRejectsOversizedSemanticLineWithoutPagination(t *testing.T) {
	f := newLocalCodeFixture(t)
	f.service.Git.MaxDiffBytes = 64
	if err := os.WriteFile(filepath.Join(f.root, "oversized.txt"), []byte(strings.Repeat("x", 128)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selector := "WT-MAIN-" + f.current[:8]
	_, err := f.service.CodeDiff(context.Background(), CodeDiffInput{
		ProjectID: "example",
		Worktree:  selector,
		Live:      true,
		Paths:     []string{"oversized.txt"},
	})
	if err == nil || !strings.Contains(err.Error(), "internal byte safety limit") {
		t.Fatalf("oversized semantic line was not rejected without pagination: %v", err)
	}
}
