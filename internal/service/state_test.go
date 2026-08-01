package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectRegisterCreatesCanonicalIdlePlan(t *testing.T) {
	s, _, _ := testService(t)
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if plan.SchemaVersion != model.PlanSchemaVersion || plan.Revision != 1 {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if plan.ActiveTaskID != "" || plan.ActiveRunID != "" || len(plan.Queue) != 0 || len(plan.Sections) != 0 {
		t.Fatalf("registration created non-idle plan: %#v", plan)
	}
}

func TestStateCheckReportsLegacyPlanAndGraphIssuesTogether(t *testing.T) {
	s, hubRevision, _ := testService(t)
	legacy := `{"schema_version":1,"project_id":"example","revision":1,"summary":"legacy","body":"# legacy","updated_by":"test","updated_at":"2026-08-01T00:00:00Z"}`
	_, err := s.Hub.Transact(context.Background(), hubRevision, "test: install invalid plan", func(worktree string) ([]string, error) {
		path := s.planPath("example")
		return []string{path}, hub.WriteText(worktree, path, legacy)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Projects["missing"] = s.Config.Projects["example"]
	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid {
		t.Fatal("invalid graph was accepted")
	}
	var codes []string
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
	}
	joined := strings.Join(codes, ",")
	if !strings.Contains(joined, "LEGACY_PLAN_BODY") || !strings.Contains(joined, "CONFIGURED_PROJECT_MISSING") {
		t.Fatalf("preflight did not report all independent blockers: %s", joined)
	}
}

func TestStateRepairClearsOnlyObsoletePointerAndPreservesHistory(t *testing.T) {
	s, hubRevision, _ := testService(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	plan.ActiveTaskID = "historical-task"
	plan.ActiveRunID = "11111111-1111-4111-8111-111111111111"
	plan.Revision++
	plan.UpdatedBy = "test"
	tx, err := s.Hub.Transact(context.Background(), hubRevision, "test: install obsolete pointer", func(worktree string) ([]string, error) {
		planPath := s.planPath("example")
		runPath := s.runPath("example", "11111111-1111-4111-8111-111111111111")
		if err := hub.WriteJSON(worktree, planPath, plan); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, runPath, string(fixture)); err != nil {
			return nil, err
		}
		return []string{planPath, runPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeRun, err := s.Hub.ReadFile(context.Background(), s.runPath("example", "11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.StateRepair(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.OldHubSHA != tx.After || len(result.Actions) != 1 {
		t.Fatalf("unexpected repair result: %#v", result)
	}
	var repaired model.Plan
	if err := s.Hub.ReadJSON(context.Background(), s.planPath("example"), &repaired); err != nil {
		t.Fatal(err)
	}
	if repaired.ActiveTaskID != "" || repaired.ActiveRunID != "" {
		t.Fatalf("obsolete pointers remain: %#v", repaired)
	}
	afterRun, err := s.Hub.ReadFile(context.Background(), s.runPath("example", "11111111-1111-4111-8111-111111111111"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRun) != string(beforeRun) {
		t.Fatal("immutable history record was changed by repair")
	}
}
