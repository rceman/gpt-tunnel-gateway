package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestReviewSnapshotCLISuccessRenderingPath(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	output(model.ReviewSnapshot{SchemaVersion: 1, ReviewState: "active", NextAction: "wait_for_terminal"})
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"review_state": "active"`) {
		t.Fatalf("unexpected rendering: %s", data)
	}
}

func TestReviewSnapshotCLIErrorRenderingPathIsBounded(t *testing.T) {
	s := service.New(config.Config{})
	_, err := s.RunReviewSnapshot(context.Background(), "missing")
	if err == nil || err.Error() != "read-only hub lock unavailable" || strings.Contains(err.Error(), "state") || strings.Contains(err.Error(), "lock/") {
		t.Fatalf("unexpected CLI error: %v", err)
	}
}

func TestPlanCutoverCLIRouteUsesCurrentFixture(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "plan_v1_current.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(fixture, &legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(context.Background(), registered.Hub.After, "test: install current plan fixture", func(w string) ([]string, error) {
		path := hub.ProtocolRoot + "/projects/example/plan/current.json"
		if err := hub.WriteJSON(w, path, legacy); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "cutover.json")
	if err := os.WriteFile(input, []byte(`{"project_id":"example","updated_by":"owner"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	plan(context.Background(), s, []string{"cutover", "--file", input})
	_ = writer.Close()
	os.Stdout = oldStdout
	outputBytes, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(outputBytes), `"status": "cut over"`) {
		t.Fatalf("CLI cutover output=%s", outputBytes)
	}
	planResult, err := s.PlanRead(context.Background(), "example")
	if err != nil || strings.Join(planResult.Queue, ",") != "P0,P1,P2" {
		t.Fatalf("CLI did not perform exact cutover: err=%v plan=%#v", err, planResult)
	}
}
