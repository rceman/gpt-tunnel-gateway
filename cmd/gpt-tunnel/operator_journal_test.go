package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func captureOperatorCLI(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	fn()
	_ = writer.Close()
	os.Stdout = old
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOperatorCLIRecordCheckpointAndHistoryRoutes(t *testing.T) {
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
	if _, _, err := s.ProjectIdentifiersAdopt(context.Background(), service.ProjectIdentifiersAdoptInput{ProjectID: "example", ProjectCode: "EXM", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}}); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(dir, "operator.json")
	input := `{"project_id":"example","session_id":null,"kind":"user_talk","summary":"cli context","content":{"decisions":[],"commitments":[],"facts":["fact"],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":[]},"references":{"plan_sections":[],"adrs":[],"tasks":[],"runs":[],"commits":[],"identities":[]},"actor":"owner"}`
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	record := captureOperatorCLI(t, func() { operator(context.Background(), s, []string{"record", "--file", inputPath}) })
	if !strings.Contains(record, `"id": "EXM-O1"`) || !strings.Contains(record, `"status": "recorded"`) {
		t.Fatalf("unexpected operator record CLI output: %s", record)
	}
	checkpointInput := `{"project_id":"example","session_id":null,"summary":"cli checkpoint","content":{"decisions":[],"commitments":["keep"],"facts":[],"assumptions":[],"blockers":[],"unresolved":[],"next_actions":[]},"references":{"plan_sections":[],"adrs":[],"tasks":[],"runs":[],"commits":[],"identities":[]},"actor":"owner"}`
	if err := os.WriteFile(inputPath, []byte(checkpointInput), 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoint := captureOperatorCLI(t, func() { operator(context.Background(), s, []string{"checkpoint", "--file", inputPath}) })
	if !strings.Contains(checkpoint, `"id": "EXM-O2"`) || !strings.Contains(checkpoint, `"status": "checkpointed"`) {
		t.Fatalf("unexpected operator checkpoint CLI output: %s", checkpoint)
	}
	history := captureOperatorCLI(t, func() { operator(context.Background(), s, []string{"history", "example", "--limit", "1"}) })
	if !strings.Contains(history, `"has_more": true`) || !strings.Contains(history, `"next_after_event_id": "EXM-O1"`) {
		t.Fatalf("unexpected operator history CLI output: %s", history)
	}
}
