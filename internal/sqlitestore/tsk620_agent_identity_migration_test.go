package sqlitestore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func tsk620ExecutionState(taskID, status string) model.TaskExecutionState {
	now := time.Now().UTC()
	return model.TaskExecutionState{
		TaskID:             taskID,
		ProjectID:          "gpt-tunnel-gateway",
		TaskRevision:       1,
		TaskRevisionSHA256: strings.Repeat("a", 64),
		Status:             status,
		Stage:              "code",
		Worktree:           "WT-" + strings.TrimPrefix(taskID, "GTW-") + "-cccccccc",
		BaseHead:           strings.Repeat("b", 40),
		Head:               strings.Repeat("c", 40),
		Branch:             "task/" + taskID + "-identity-migration",
		Agent:              "gpt-review-planner",
		ExecutionRevision:  1,
		UpdatedAt:          now,
	}
}

func tsk620Agent(projectID, agentID string) model.Agent {
	now := time.Now().UTC()
	return model.Agent{
		SchemaVersion:        model.AgentSchemaVersion,
		ProjectID:            projectID,
		AgentID:              agentID,
		Role:                 model.AgentRoleCoding,
		Enabled:              true,
		RecommendedReasoning: model.ReasoningHigh,
		Capabilities:         []string{"coding"},
		CreatedAt:            now.Add(-time.Hour),
		UpdatedAt:            now,
	}
}

func TestTSK620NonterminalExecutionIdentityMigrationPreservesTerminalHistoryAcrossRestart(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	active := tsk620ExecutionState("GTW-TSK620", model.TaskExecutionDispatched)
	terminal := tsk620ExecutionState("GTW-TSK621", model.TaskExecutionFailed)
	for _, state := range []model.TaskExecutionState{active, terminal} {
		if err := db.CreateTaskExecutionState(ctx, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MigrateTaskExecutionAgentIdentity(ctx, "gpt-tunnel-gateway", "gpt-review-planner", "gtw-worker"); err != nil {
		t.Fatal(err)
	}
	migrated, found, err := db.ReadTaskExecutionState(ctx, active.ProjectID, active.TaskID)
	if err != nil || !found {
		t.Fatalf("migrated active state=%#v found=%v err=%v", migrated, found, err)
	}
	if migrated.Agent != "gtw-worker" || migrated.Status != active.Status || migrated.ExecutionRevision != active.ExecutionRevision {
		t.Fatalf("active execution continuity was not preserved: %#v", migrated)
	}
	preserved, found, err := db.ReadTaskExecutionState(ctx, terminal.ProjectID, terminal.TaskID)
	if err != nil || !found {
		t.Fatalf("terminal state=%#v found=%v err=%v", preserved, found, err)
	}
	if preserved.Agent != "gpt-review-planner" || preserved.Status != terminal.Status {
		t.Fatalf("terminal historical identity was rewritten: %#v", preserved)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MigrateTaskExecutionAgentIdentity(ctx, "gpt-tunnel-gateway", "gpt-review-planner", "gtw-worker"); err != nil {
		t.Fatal(err)
	}
	restarted, found, err := db.ReadTaskExecutionState(ctx, active.ProjectID, active.TaskID)
	if err != nil || !found || restarted.Agent != "gtw-worker" {
		t.Fatalf("restart lost migrated execution authority: %#v found=%v err=%v", restarted, found, err)
	}
}

func TestTSK620LocalAgentIdentityMigrationRejectsCollisionAndPreservesContinuity(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	db, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	legacy := tsk620Agent("gpt-tunnel-gateway", "gpt-review-planner")
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(ctx, LocalAgent{
		ProjectID: legacy.ProjectID,
		AgentID:   legacy.AgentID,
		Payload:   legacyPayload,
		UpdatedAt: legacy.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateLocalAgentIdentity(ctx, legacy.ProjectID, legacy.AgentID, "gtw-worker"); err != nil {
		t.Fatal(err)
	}
	migrated, err := db.ReadLocalAgent(ctx, legacy.ProjectID, "gtw-worker")
	if err != nil {
		t.Fatal(err)
	}
	var migratedAgent model.Agent
	if err := json.Unmarshal(migrated.Payload, &migratedAgent); err != nil {
		t.Fatal(err)
	}
	if migratedAgent.AgentID != "gtw-worker" || migratedAgent.ProjectID != legacy.ProjectID || migrated.UpdatedAt != legacy.UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("migrated Local Agent continuity=%#v payload=%#v", migrated, migratedAgent)
	}
	if err := db.MigrateLocalAgentIdentity(ctx, legacy.ProjectID, legacy.AgentID, "gtw-worker"); err != nil {
		t.Fatal(err)
	}
	canonical := tsk620Agent(legacy.ProjectID, "gtw-worker")
	canonicalPayload, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(ctx, LocalAgent{
		ProjectID: legacy.ProjectID,
		AgentID:   legacy.AgentID,
		Payload:   legacyPayload,
		UpdatedAt: legacy.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(ctx, LocalAgent{
		ProjectID: canonical.ProjectID,
		AgentID:   canonical.AgentID,
		Payload:   canonicalPayload,
		UpdatedAt: canonical.UpdatedAt.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateLocalAgentIdentity(ctx, legacy.ProjectID, legacy.AgentID, "gtw-worker"); err == nil {
		t.Fatal("Local Agent identity collision was accepted")
	}
	if _, err := db.ReadLocalAgent(ctx, legacy.ProjectID, legacy.AgentID); err != nil {
		t.Fatal("collision removed legacy Local Agent", err)
	}
	if _, err := db.ReadLocalAgent(ctx, legacy.ProjectID, "gtw-worker"); err != nil {
		t.Fatal("collision removed canonical Local Agent", err)
	}
}
