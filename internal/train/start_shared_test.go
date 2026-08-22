package train

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestStartSharedAdmitsAttemptWithoutHubRefresh(t *testing.T) {
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	mirror := filepath.Join(stateDir, "mirror.git")
	airelayPath := filepath.Join(stateDir, "airelay")
	if err := os.WriteFile(airelayPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	timeNow := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("gateway", "GTW-TSK1", AuthoringDraft{Title: "Shared start", Objective: "admit locally", ADRRelation: model.TaskADRNoRequired}, "planner", timeNow)
	if err != nil {
		t.Fatal(err)
	}
	task, err = ReadyTask(task, "planner", timeNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	train, err := New("gateway", "GTW-TRN1", "planner", []model.TaskAuthoring{task}, timeNow)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	payload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{ID: train.ID, Revision: int64(train.Revision), Payload: payload, UpdatedAt: train.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	projectConfig := config.ProjectConfig{Root: projectRoot, Mirror: mirror, Remote: "origin", DefaultBranch: "main", ProjectCode: "GTW"}
	gitRunner := gitx.Runner{StateDir: stateDir, MaxReadBytes: 1 << 20}
	if err := gitRunner.EnsureMirror(context.Background(), projectConfig); err != nil {
		t.Fatal(err)
	}
	deps := StartDependencies{
		Shared:        db,
		OperationID:   "mutation-train-start-shared",
		Git:           gitRunner,
		Airelay:       airelay.Client{Command: airelayPath, Timeout: time.Second},
		ProjectConfig: projectConfig,
		Project:       model.Project{ID: "gateway", DefaultBranch: "main"},
		Policy:        model.ProjectWorkflowPolicy{IntegrationBranch: "main"},
		Train:         train,
		GatewayID:     "gateway",
		ProjectCode:   "GTW",
		StateDir:      stateDir,
		MaterializePacket: func(_ context.Context, _ model.TrainV2, _ model.TrainV2Item, _ model.TrainV2Attempt, runtime RuntimeBinding) (AgentTaskPacket, error) {
			return AgentTaskPacket{Path: filepath.Join(runtime.WorktreePath, "packet.md"), WorktreePath: runtime.WorktreePath}, nil
		},
		ReadTask: func(context.Context, string, string) (model.TaskAuthoring, error) { return task, nil },
		Now:      func() time.Time { return timeNow },
	}
	started, err := Start(context.Background(), StartInput{ProjectID: "gateway", TrainID: train.ID, StartedBy: "planner", ResolvedAgentID: "agent-1", SessionKey: "gateway_master"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if started.Attempt.Number != 1 || started.Attempt.DispatchedAt == nil {
		t.Fatalf("unexpected shared Attempt: %#v", started.Attempt)
	}
	shared, err := db.ReadSharedEntity(context.Background(), "train", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if shared.Revision != 3 {
		t.Fatalf("shared Train revision=%d, want 3 after admission+dispatch", shared.Revision)
	}
	deps.Train, err = readSharedTrain(context.Background(), db, "gateway", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), StartInput{ProjectID: "gateway", TrainID: train.ID, StartedBy: "planner", ResolvedAgentID: "agent-1", SessionKey: "gateway_master"}, deps); err != nil {
		t.Fatalf("already-running Attempt was not reused: %v", err)
	}
	outbox, err := db.PendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbox) != 2 || strings.Contains(string(payload), "run_id") {
		t.Fatalf("unexpected Shared outbox or legacy Run data: count=%d payload=%s", len(outbox), payload)
	}
}
