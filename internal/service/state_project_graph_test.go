package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
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

func TestStateCheckIncludesManagedProjectsFromOneEffectiveResolution(t *testing.T) {
	s, _, _ := testService(t)
	writeManagedServiceTestRegistry(t, s, map[string]config.ManagedProjectEntry{"managed": managedServiceTestEntry(t.TempDir(), "managed")})
	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.ConfiguredProjectIDs, []string{"example", "managed"}) {
		t.Fatalf("configured project IDs = %v", result.ConfiguredProjectIDs)
	}
	foundMissing := false
	for _, issue := range result.Issues {
		if issue.Code == "CONFIGURED_PROJECT_MISSING" && issue.ProjectID == "managed" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("state check did not validate managed project durable state: %#v", result.Issues)
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
	s.Config.Projects["missing"] = config.ProjectConfig{
		Root:              t.TempDir(),
		Mirror:            filepath.Join(t.TempDir(), "missing-mirror.git"),
		Remote:            "origin",
		DefaultBranch:     "main",
		AirelaySessionKey: "missing_master",
	}
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
	task := model.Task{SchemaVersion: model.SchemaVersion, ID: "11111111-1111-4111-8111-111111111111", ProjectID: "example", Title: "Historical task", Objective: "Preserve immutable history during cutover.", Branch: "feature/historical", BaseRevision: strings.Repeat("b", 40), AcceptanceCriteria: []string{"history remains unchanged"}, CreatedBy: "test", CreatedAt: time.Now().UTC()}
	task.SHA256, err = model.HashTask(task)
	if err != nil {
		t.Fatal(err)
	}
	fixtureText := strings.Replace(string(fixture), `"task_id": "historical-task"`, fmt.Sprintf(`"task_id": "%s"`, task.ID), 1)
	fixtureText = strings.Replace(fixtureText, `"task_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, fmt.Sprintf(`"task_sha256": "%s"`, task.SHA256), 1)
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
		taskPath := s.taskPath("example", task.ID)
		if err := hub.WriteJSON(worktree, taskPath, task); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, planPath, plan); err != nil {
			return nil, err
		}
		if err := hub.WriteText(worktree, runPath, fixtureText); err != nil {
			return nil, err
		}
		return []string{taskPath, planPath, runPath}, nil
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
