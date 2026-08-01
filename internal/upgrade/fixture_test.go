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
	}
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatal(err)
	}
	if matrix.SourceVersion != "0.2.2" || len(matrix.Cases) != 24 {
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
	if len(matrix.ExpectedBlockers) < 6 {
		t.Fatalf("fixture blocker matrix is incomplete: %#v", matrix.ExpectedBlockers)
	}
}
