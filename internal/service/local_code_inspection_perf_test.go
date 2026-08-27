package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

const localCodePerformanceLimit = time.Second

func TestLocalCodeInspectionPerformanceGate(t *testing.T) {
	f := newLocalCodeFixture(t)
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project

	var content strings.Builder
	for line := 0; line < 512; line++ {
		fmt.Fprintf(&content, "line %d needle\n", line)
	}
	if err := os.WriteFile(filepath.Join(f.root, "large.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "add", "large.txt")
	testutil.Git(t, f.root, "commit", "-m", "performance fixture")
	f.current = strings.TrimSpace(testutil.Git(t, f.root, "rev-parse", "HEAD"))

	stateDir := f.service.Config.StateDir
	trainPath := filepath.Join(stateDir, "work", "EXM", "TRN1")
	if err := os.MkdirAll(filepath.Dir(trainPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.root, "branch", "train/EXM-TRN1", f.current)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", trainPath)
		testutil.Git(t, f.root, "branch", "-D", "train/EXM-TRN1")
	})
	testutil.Git(t, f.root, "worktree", "add", trainPath, "train/EXM-TRN1")

	db := f.service.Durability
	if db == nil {
		t.Fatal("performance fixture requires Shared durability")
	}
	f.service.Durability = db
	now := time.Now().UTC()
	train := model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion,
		ID:            "EXM-TRN1",
		ProjectID:     "example",
		Revision:      1,
		Status:        model.TrainV2Planned,
		CreatedBy:     "performance-test",
		CreatedAt:     now,
		UpdatedAt:     now,
		Items: []model.TrainV2Item{{
			Position:           0,
			TaskID:             "EXM-TSK1",
			TaskRevision:       1,
			TaskRevisionSHA256: strings.Repeat("a", 64),
			Status:             model.TrainV2ItemQueued,
			AddedAt:            now,
		}},
	}
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CommitSharedMutation(context.Background(), sqlitestore.SharedMutation{
		OperationID: "OPR-EXM-PERF-TRAIN1",
		EntityType:  "train",
		EntityID:    train.ID,
		Revision:    1,
		Kind:        "performance-fixture",
		Payload:     payload,
		Create:      true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(f.root, "tracked.txt"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	mainSelector := "WT-MAIN-" + f.current[:8]
	measure := func(name string, call func() error) {
		t.Helper()
		started := time.Now()
		err := call()
		elapsed := time.Since(started)
		t.Logf("%s: %dms", name, elapsed.Milliseconds())
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		if elapsed >= localCodePerformanceLimit {
			t.Fatalf("%s exceeded %s: %s", name, localCodePerformanceLimit, elapsed)
		}
	}

	var worktreeCursor string
	measure("code/worktree first", func() error {
		result, callErr := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example", Limit: 1})
		if callErr == nil && (len(result.Items) == 0 || result.Items[0].Head != f.current || result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated worktree result: %#v", result)
		}
		if result.Pagination != nil {
			worktreeCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/worktree continuation", func() error {
		result, callErr := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example", Limit: 1, Cursor: worktreeCursor})
		if callErr == nil && len(result.Items) != 1 {
			callErr = fmt.Errorf("expected worktree continuation item: %#v", result)
		}
		return callErr
	})

	var treeCursor string
	measure("code/tree first", func() error {
		result, callErr := f.service.CodeTree(context.Background(), CodeTreeInput{ProjectID: "example", Worktree: mainSelector, Live: true, Limit: 1})
		if callErr == nil && (len(result.Paths) != 1 || result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated tree result: %#v", result)
		}
		if result.Pagination != nil {
			treeCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/tree continuation", func() error {
		result, callErr := f.service.CodeTree(context.Background(), CodeTreeInput{ProjectID: "example", Worktree: mainSelector, Live: true, Limit: 1, Cursor: treeCursor})
		if callErr == nil && len(result.Paths) != 1 {
			callErr = fmt.Errorf("expected tree continuation path: %#v", result)
		}
		return callErr
	})

	var searchCursor string
	measure("code/search first", func() error {
		result, callErr := f.service.CodeSearch(context.Background(), CodeSearchInput{ProjectID: "example", Worktree: mainSelector, Live: true, Query: "needle", Limit: 1})
		if callErr == nil && (len(result.Matches) != 1 || result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated search result: %#v", result)
		}
		if result.Pagination != nil {
			searchCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/search continuation", func() error {
		result, callErr := f.service.CodeSearch(context.Background(), CodeSearchInput{ProjectID: "example", Worktree: mainSelector, Live: true, Query: "needle", Limit: 1, Cursor: searchCursor})
		if callErr == nil && len(result.Matches) != 1 {
			callErr = fmt.Errorf("expected search continuation match: %#v", result)
		}
		return callErr
	})

	var readCursor string
	measure("code/read first", func() error {
		result, callErr := f.service.CodeRead(context.Background(), CodeReadInput{ProjectID: "example", Worktree: mainSelector, Live: true, Path: "tracked.txt", LineCount: 1})
		if callErr == nil && (result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated read result: %#v", result)
		}
		if result.Pagination != nil {
			readCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/read continuation", func() error {
		result, callErr := f.service.CodeRead(context.Background(), CodeReadInput{ProjectID: "example", Worktree: mainSelector, Live: true, Path: "tracked.txt", LineCount: 1, Cursor: readCursor})
		if callErr == nil && result.StartLine != 2 {
			callErr = fmt.Errorf("expected read continuation at line 2: %#v", result)
		}
		return callErr
	})

	var diffCursor string
	measure("code/diff first", func() error {
		result, callErr := f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: mainSelector, Live: true, Paths: []string{"tracked.txt"}, LineCount: 8})
		if callErr == nil && (result.Pagination == nil || result.Pagination.NextCursor == "") {
			callErr = fmt.Errorf("expected paginated diff result: %#v", result)
		}
		if result.Pagination != nil {
			diffCursor = result.Pagination.NextCursor
		}
		return callErr
	})
	measure("code/diff continuation", func() error {
		result, callErr := f.service.CodeDiff(context.Background(), CodeDiffInput{ProjectID: "example", Worktree: mainSelector, Live: true, Paths: []string{"tracked.txt"}, LineCount: 8, Cursor: diffCursor})
		if callErr == nil && result.Diff == "" {
			callErr = fmt.Errorf("expected diff continuation bytes: %#v", result)
		}
		return callErr
	})
}
