package upgrade

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviousVersionFixtureCoversUpgradeMatrix(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "upgrades", "v0.2.2")
	data, err := os.ReadFile(filepath.Join(root, "matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		SourceVersion    string   `json:"source_version"`
		Cases            []string `json:"cases"`
		LegacyPlans      []string `json:"legacy_plans"`
		ExpectedBlockers []string `json:"expected_blockers"`
		HistoryOnly      struct {
			ProjectID      string `json:"project_id"`
			TaskID         string `json:"task_id"`
			RunID          string `json:"run_id"`
			ActiveTaskID   string `json:"active_task_id"`
			ActiveRunID    string `json:"active_run_id"`
			TaskState      string `json:"task_state"`
			RunShape       string `json:"run_shape"`
			RepairedStatus string `json:"repaired_task_state"`
		} `json:"history_only_dispatched_state"`
	}
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatal(err)
	}
	if matrix.SourceVersion != "0.2.2" || len(matrix.Cases) != 25 {
		t.Fatalf("fixture matrix is incomplete: %#v", matrix)
	}
	for _, project := range matrix.LegacyPlans {
		path := filepath.Join(root, "plans", project, "current.json")
		plan, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(plan, &object); err != nil {
			t.Fatal(err)
		}
		if _, ok := object["body"]; !ok {
			t.Fatalf("legacy fixture %s has no body field", project)
		}
	}
	for _, relative := range []string{
		"config.json",
		"projects/gpt-review-planner/project.json",
		"projects/gpt-tunnel-gateway/project.json",
		"runs/history-only-run.json",
		"tasks/dispatched-with-run.json",
		"tasks/terminal.json",
	} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("fixture missing %s: %v", relative, err)
		}
	}
	if matrix.HistoryOnly.ProjectID != "gpt-review-planner" || matrix.HistoryOnly.TaskID != "legacy-task-planner" || matrix.HistoryOnly.RunID != "legacy-run-planner" || matrix.HistoryOnly.ActiveTaskID != "" || matrix.HistoryOnly.ActiveRunID != "" || matrix.HistoryOnly.TaskState != "dispatched" || matrix.HistoryOnly.RunShape != "HistoricalRunV1" || matrix.HistoryOnly.RepairedStatus != "cancelled" {
		t.Fatalf("history-only mutable-state fixture is incomplete: %#v", matrix.HistoryOnly)
	}
	for _, relative := range []string{"state-repair/plan.json", "state-repair/task.json", "state-repair/task.state.json", "state-repair/run.json"} {
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("history-only repair fixture missing %s: %v", relative, err)
		}
	}
	if len(matrix.ExpectedBlockers) < 7 {
		t.Fatalf("fixture blocker matrix is incomplete: %#v", matrix.ExpectedBlockers)
	}
}
