package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK666Gate20HubCurrentConfigurationMigration(t *testing.T) {
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
	fields["watcher"] = json.RawMessage(`{"enabled":true,"interval_seconds":15}`)
	var workflow map[string]json.RawMessage
	if err := json.Unmarshal(fields["workflow"], &workflow); err != nil {
		t.Fatal(err)
	}
	var gateCommands map[string]json.RawMessage
	if err := json.Unmarshal(workflow["gate_commands"], &gateCommands); err != nil {
		t.Fatal(err)
	}
	var testGate map[string]json.RawMessage
	if err := json.Unmarshal(gateCommands["test"], &testGate); err != nil {
		t.Fatal(err)
	}
	testGate["train"] = json.RawMessage(`{"command":["go","test","./..."]}`)
	gateCommands["test"], err = json.Marshal(testGate)
	if err != nil {
		t.Fatal(err)
	}
	workflow["gate_commands"], err = json.Marshal(gateCommands)
	if err != nil {
		t.Fatal(err)
	}
	fields["workflow"], err = json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := s.MigrateHubProjectConfigurations(ctx); err != nil {
		t.Fatal(err)
	}
	migratedRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil || migratedRevision == seeded.After {
		t.Fatalf("Hub migration revision=%q seeded=%q err=%v", migratedRevision, seeded.After, err)
	}
	var migratedRaw json.RawMessage
	if err := s.Hub.ReadJSON(ctx, path, &migratedRaw); err != nil {
		t.Fatal(err)
	}
	migrated, err := sqlitestore.DecodeCanonicalProjectConfigurationPayload(migratedRaw)
	if err != nil || migrated.ProjectID != "example" || migrated.Revision != configuration.Revision || strings.Contains(string(migratedRaw), `"watcher"`) || strings.Contains(string(migratedRaw), `"train"`) {
		t.Fatalf("Hub ProjectConfiguration=%s err=%v", migratedRaw, err)
	}
	if err := s.MigrateHubProjectConfigurations(ctx); err != nil {
		t.Fatalf("idempotent Hub migration: %v", err)
	}
	finalRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil || finalRevision != migratedRevision {
		t.Fatalf("second Hub migration changed revision: got=%q want=%q err=%v", finalRevision, migratedRevision, err)
	}
}
