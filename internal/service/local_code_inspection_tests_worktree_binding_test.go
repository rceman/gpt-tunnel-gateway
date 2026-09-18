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

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestCodeWorktreeFailsClosedForAuthoritativeHotfixWithoutManagedBinding(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/missing", TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("authoritative hotfix without a managed binding was not rejected: %v", err)
	}
}

func TestCodeWorktreeFailsClosedForAuthoritativeHotfixWithUnexpectedBinding(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	branch := "hotfix/unexpected"
	lane := filepath.Join(t.TempDir(), "unexpected-hotfix")
	testutil.Git(t, f.root, "branch", branch, f.current)
	testutil.Git(t, f.root, "worktree", "add", lane, branch)
	t.Cleanup(func() {
		testutil.Git(t, f.root, "worktree", "remove", "--force", lane)
		testutil.Git(t, f.root, "branch", "-D", branch)
	})
	if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/" + branch, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CodeWorktree(context.Background(), CodeWorktreeInput{ProjectID: "example"}); err == nil || !strings.Contains(err.Error(), "server-owned") {
		t.Fatalf("authoritative hotfix with an unexpected binding was not rejected: %v", err)
	}
}

func TestCodeWorktreeEnumeratesGitWorktreesOnceForMultipleAuthoritativeLanes(t *testing.T) {
	f := newLocalCodeFixture(t)
	runner := gitx.Runner{StateDir: f.service.Config.StateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 100}
	project := f.service.Config.Projects["example"]
	project.ProjectCode = "EXM"
	f.service.Config.Projects["example"] = project
	now := time.Now().UTC()
	addLane := func(kind, id string) string {
		t.Helper()
		branch := kind + "/" + id
		lane := filepath.Join(f.service.Config.StateDir, "hotfix-worktrees", "example", id)
		if kind == "hotfix" {
			if err := os.MkdirAll(filepath.Dir(lane), 0o700); err != nil {
				t.Fatal(err)
			}
		}
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
		return strings.TrimSpace(testutil.Git(t, lane, "rev-parse", "HEAD"))
	}
	for _, id := range []string{"fix-a", "fix-b"} {
		head := addLane("hotfix", id)
		if err := runner.RecordHotfixIdentity(f.service.Config.StateDir, gitx.HotfixIdentity{ProjectID: "example", HotfixRef: "refs/heads/hotfix/" + id, TaskID: "EXM-TSK1", BaseSHA: f.current, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if head == "" {
			t.Fatal("hotfix fixture has no head")
		}
	}
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
