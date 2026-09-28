package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
)

func TestProjectConfigurationHubMigrationFailsClosedOnRetiredGateCommands(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	fields["schema_version"] = json.RawMessage(`2`)
	fields["watcher"] = json.RawMessage(`{"enabled":true,"interval_seconds":15}`)
	fields["workflow"] = json.RawMessage(`{"workflow_stage":"transitional_main","integration_branch":"main","wait_for_ci":false,"ci":{"task":"disabled","task_merge":"observe","release":"observe"},"gate_commands":{"test":{"train":{"command":["go","test","./..."]}}}}`)
	retired, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	path := s.projectConfigurationPath("example")
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := s.Hub.Transact(ctx, revision, "test: seed retired Hub ProjectConfiguration fields", func(worktree string) ([]string, error) {
		if err := hub.WriteText(worktree, path, string(retired)); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateHubProjectConfigurations(ctx); err == nil {
		t.Fatal("nonempty retired gate-command authority was migrated without an explicit Procedure mapping")
	}
	currentRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil || currentRevision != seeded.After {
		t.Fatalf("failed migration changed Hub revision: got=%q seeded=%q err=%v", currentRevision, seeded.After, err)
	}
	var current json.RawMessage
	if err := s.Hub.ReadJSON(ctx, path, &current); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(retired)) {
		t.Fatalf("failed migration mutated the legacy configuration: %s", current)
	}
}
