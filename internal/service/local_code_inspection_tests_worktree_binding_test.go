package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeWorktreeIgnoresHistoricalHotfixIdentityRecord(t *testing.T) {
	f := newLocalCodeFixture(t)
	identityPath := filepath.Join(f.service.Config.StateDir, "hotfix-identities", "example", "missing.json")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte(`{"project_id":"example","hotfix_ref":"refs/heads/hotfix/missing","task_id":"EXM-TSK1","base_sha":"`+f.current+`","created_at":"2026-09-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Kind == "hotfix" {
			t.Fatalf("historical hotfix identity record produced a live lane: %#v", item)
		}
	}
}

func TestCodeWorktreeIgnoresHistoricalHotfixLaneAndRecord(t *testing.T) {
	f := newLocalCodeFixture(t)
	branch := "hotfix/unexpected"
	lane := filepath.Join(t.TempDir(), "unexpected-hotfix")
	testutil.Git(t, f.root, "branch", branch, f.current)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	identityPath := filepath.Join(f.service.Config.StateDir, "hotfix-identities", "example", "unexpected.json")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte(`{"project_id":"example","hotfix_ref":"refs/heads/`+branch+`","task_id":"EXM-TSK1","base_sha":"`+f.current+`","created_at":"2026-09-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Kind == "hotfix" || item.Label == "unexpected" {
			t.Fatalf("historical hotfix lane was enumerated as live: %#v", item)
		}
	}
}

func TestCodeWorktreeEnumeratesGitWorktreesOnceForMultipleAuthoritativeLanes(t *testing.T) {
	f := newLocalCodeFixture(t)
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project
	now := time.Now().UTC()
	for _, id := range []string{"EXM-TSK11", "EXM-TSK12"} {
		lane := filepath.Join(f.service.Config.StateDir, "task-worktrees", "example", id)
		if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
			t.Fatal(err)
		}
		branch := "task/" + id + "-lane"
		testutil.Git(t, f.root, "branch", branch, f.current)
		testutil.Git(t, f.root, "worktree", "add", lane, branch)
		name := id + ".txt"
		if err := os.WriteFile(filepath.Join(lane, name), []byte(id+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, lane, "add", name)
		testutil.Git(t, lane, "commit", "-m", id+" fixture")
		t.Cleanup(func() {
			testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
			testutil.Git(t, f.root, "branch", "-D", branch)
		})
		head := strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
		selector := "WT-TSK" + strings.TrimPrefix(id, "EXM-TSK") + "-" + strings.ToLower(head[:8])
		state := model.TaskExecutionState{
			TaskID: id, ProjectID: "example", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64),
			Status: model.TaskExecutionInProgress, Stage: "code", Worktree: selector,
			BaseHead: f.base, Head: head, Branch: branch, Agent: "gtw-worker",
			ExecutionRevision: 1, UpdatedAt: now,
		}
		if err := f.service.Durability.CreateTaskExecutionState(context.Background(), state); err != nil {
			t.Fatal(err)
		}
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	counter := filepath.Join(t.TempDir(), "worktree-list-count")
	if err := os.WriteFile(counter, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(binDir, "git")
	script := fmt.Sprintf("#!/bin/sh\ncase \" $* \" in *\" worktree list \"*) n=$(awk 'NR==1 {print $1}' %q); echo $((n+1)) > %q;; esac\nexec %q \"$@\"\n", counter, counter, realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err != nil {
		t.Fatal(err)
	}
	countBytes, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(countBytes)); got != "1" {
		t.Fatalf("Git worktree inventory enumerated %s times, want once", got)
	}
}
