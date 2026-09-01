package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestHotfixLifecycleUsesRecordedBaseAndExactRetryIsNoOp(t *testing.T) {
	s, hubRevision, base := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Hotfix-bound Task", Objective: "Exercise the mandatory hotfix Task binding.",
		ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.HotfixCreate(context.Background(), "example", HotfixCreateInput{Slug: "repair", TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if created.TaskID != task.ID {
		t.Fatalf("created=%#v lost Task binding", created)
	}
	bound, err := s.TaskAuthoringRead(context.Background(), "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Execution != model.TaskExecutionHotfix || bound.Revision != task.Revision+1 || operation.Hub.After == "" {
		t.Fatalf("hotfix Task binding=%#v operation=%#v", bound, operation)
	}
	if created.BaseSHA != base || created.HeadSHA != base {
		t.Fatalf("created=%#v want base=%s", created, base)
	}
	if _, err := s.HotfixIntegrate(context.Background(), "example", HotfixIntegrateInput{HotfixRef: created.HotfixRef, ReviewedSHA: created.BaseSHA}); err == nil {
		t.Fatal("reviewed base was accepted as an integration commit")
	}
	laneRoot := filepath.Join(s.Config.StateDir, "hotfix-worktrees", "example", "repair")
	if err := os.WriteFile(filepath.Join(laneRoot, "fix.txt"), []byte("fix\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, laneRoot, "add", "fix.txt")
	testutil.Git(t, laneRoot, "commit", "-m", "hotfix")
	reviewed := strings.TrimSpace(testutil.Git(t, laneRoot, "rev-parse", "HEAD"))
	input := HotfixIntegrateInput{HotfixRef: created.HotfixRef, ReviewedSHA: reviewed}
	first, err := s.HotfixIntegrate(context.Background(), "example", input)
	if err != nil {
		t.Fatal(err)
	}
	if first.MainAfter != reviewed || first.BaseSHA != base {
		t.Fatalf("first integration=%#v", first)
	}
	second, err := s.HotfixIntegrate(context.Background(), "example", input)
	if err != nil {
		t.Fatal(err)
	}
	if second.MainBefore != reviewed || second.MainAfter != reviewed || second.BaseSHA != base {
		t.Fatalf("retry integration=%#v", second)
	}
}

func TestHotfixCreateRequiresExistingTaskBinding(t *testing.T) {
	s, _, _ := testService(t)
	if _, err := s.HotfixCreate(context.Background(), "example", HotfixCreateInput{Slug: "unbound"}); err == nil {
		t.Fatal("hotfix/create accepted a missing Task binding")
	}
	if _, err := s.HotfixCreate(context.Background(), "example", HotfixCreateInput{Slug: "unknown", TaskID: "EXM-TSK999"}); err == nil {
		t.Fatal("hotfix/create accepted a non-existent Task binding")
	}
}

func TestHotfixCreateRollsBackTaskBindingWhenIdentityWriteFails(t *testing.T) {
	s, hubRevision, base := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Hotfix rollback Task", Objective: "Verify failed identity persistence does not bind the Task.",
		ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: hubRevision},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := gitx.HotfixIdentity{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/repair", TaskID: task.ID,
		BaseSHA: base, CreatedAt: time.Now().UTC(),
	}
	if err := s.Git.RecordHotfixIdentity(s.Config.StateDir, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HotfixCreate(context.Background(), "example", HotfixCreateInput{Slug: "repair", TaskID: task.ID}); err == nil {
		t.Fatal("hotfix/create unexpectedly succeeded with an existing identity")
	}
	restored, err := s.TaskAuthoringRead(context.Background(), "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Execution != "" {
		t.Fatalf("failed hotfix/create left Task bound: %#v", restored)
	}
	laneRoot := filepath.Join(s.Config.StateDir, "hotfix-worktrees", "example", "repair")
	if _, err := os.Stat(laneRoot); !os.IsNotExist(err) {
		t.Fatalf("failed hotfix/create left lane at %s: %v", laneRoot, err)
	}
}
