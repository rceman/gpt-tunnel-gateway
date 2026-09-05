package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func createHotfixTaskForDispatchTest(t *testing.T, s *Service, revision string) (model.TaskAuthoring, HotfixCreateResult) {
	t.Helper()
	task, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Hotfix dispatch contract", Objective: "Verify the resolved base Agent session is used.",
		ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.HotfixCreate(context.Background(), "example", HotfixCreateInput{Slug: "dispatch-contract", TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	return task, created
}

func TestTaskHotfixWorkUsesResolvedBaseSessionAndExactWorktree(t *testing.T) {
	s, revision, _ := testService(t)
	s.Config.AgentBindings[config.ProjectAgentBindingKey("example", "coder-example")] = config.AgentBinding{SessionKey: "example_master", Profile: "coding"}
	installServiceExecutionSessionFixture(t, s, filepath.Join(t.TempDir(), "prompts"))
	revision = enableTrainV2ForTest(t, s, revision)
	task, created := createHotfixTaskForDispatchTest(t, s, revision)
	if _, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	receipts, err := filepath.Glob(filepath.Join(s.Config.StateDir, "hotfix-execution", "example", task.ID, "*.json"))
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipt files=%v err=%v", receipts, err)
	}
	payload, err := os.ReadFile(receipts[0])
	if err != nil {
		t.Fatal(err)
	}
	var receipt hotfixExecutionReceipt
	if err := json.Unmarshal(payload, &receipt); err != nil {
		t.Fatal(err)
	}
	expectedPath := filepath.Clean(filepath.Join(s.Config.StateDir, "hotfix-worktrees", "example", "dispatch-contract"))
	if receipt.SessionKey != "example_master" || strings.HasPrefix(receipt.SessionKey, "gtw_lane_") {
		t.Fatalf("dispatch used derived session instead of resolved base: %#v", receipt)
	}
	if receipt.WorktreePath != expectedPath || !strings.Contains(receipt.Message, expectedPath) || !strings.Contains(receipt.Message, "only") {
		t.Fatalf("dispatch confinement missing: %#v", receipt)
	}
	if receipt.HotfixRef != created.HotfixRef || receipt.State != "delivered" {
		t.Fatalf("unexpected receipt=%#v", receipt)
	}
}

func TestTaskHotfixWorkMissingUsableBaseAgentFailsClosedWithoutLaunch(t *testing.T) {
	s, revision, _ := testService(t)
	s.Config.AgentBindings[config.ProjectAgentBindingKey("example", "coder-example")] = config.AgentBinding{}
	installServiceExecutionSessionFixture(t, s, filepath.Join(t.TempDir(), "prompts"))
	revision = enableTrainV2ForTest(t, s, revision)
	task, _ := createHotfixTaskForDispatchTest(t, s, revision)
	if _, err := s.TaskWork(context.Background(), TaskWorkInput{TaskID: task.ID}); err == nil {
		t.Fatal("hotfix work accepted without a usable base Agent session")
	}
	receipts, err := filepath.Glob(filepath.Join(s.Config.StateDir, "hotfix-execution", "example", task.ID, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 0 {
		t.Fatalf("failed dispatch wrote receipts: %v", receipts)
	}
}
