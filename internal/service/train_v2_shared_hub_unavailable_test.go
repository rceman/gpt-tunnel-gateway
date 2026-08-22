package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func sharedActiveTrainFixture(t *testing.T) (*Service, *sqlitestore.Databases, model.TrainV2, model.TaskAuthoring) {
	t.Helper()
	ctx := context.Background()
	s, _, _ := testServiceWithoutIdentifiers(t)
	configuration, err := s.ProjectConfigurationRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	configuration.ExecutionModel = "train_v2"
	configuration.Revision++
	configuration.UpdatedAt = time.Now().UTC()
	if err := model.ValidateProjectConfiguration(configuration); err != nil {
		t.Fatal(err)
	}
	projectConfig := s.Config.Projects["example"]
	projectConfig.ProjectCode = "EXM"
	s.Config.Projects["example"] = projectConfig
	// Explicit local Agent authority; the fixture must not rely on host-config
	// role synthesis.
	s.Config.AgentBindings["example/coder-example"] = config.AgentBinding{SessionKey: "example_master"}
	if _, err := session.NewStore(s.Config.StateDir).Create(session.CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: session.RoleAgent, SessionType: session.SessionTypeChatGPT, SessionRef: stringPtr("example_master")}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	markSharedBootstrapCompleteForTest(t, db)
	put := func(entityType, id string, revision int, value any) {
		t.Helper()
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if putErr := db.PutSharedProjection(ctx, entityType, sqlitestore.SharedEntity{ID: id, Revision: int64(revision), Payload: payload, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); putErr != nil {
			t.Fatal(putErr)
		}
	}
	put("project_configuration", "example", configuration.Revision, configuration)
	now := time.Now().UTC()
	task, err := trainv2.NewTask("example", "EXM-TSK1", trainv2.AuthoringDraft{Title: "shared task", Objective: "shared execution", ADRRelation: model.TaskADRNoRequired}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	task, err = trainv2.ReadyTask(task, "planner", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	put("task", task.ID, task.Revision, task)
	train, err := trainv2.New("example", "EXM-TRN1", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := now.Add(time.Minute)
	train.Status = model.TrainV2Running
	train.Items[0].Status = model.TrainV2ItemRunning
	train.Items[0].Attempts = []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "coder-example", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: strings.Repeat("a", 40), StartedAt: now, DispatchedAt: &dispatched}}
	train.Items[0].ActiveAttemptNumber = 1
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	put("train", train.ID, train.Revision, train)
	worktree, err := trainv2.CompactWorktreePath(s.Config.StateDir, "EXM", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "example", ProjectCode: "EXM", TrainID: train.ID, WorktreePath: worktree, AgentID: "coder-example", SessionKey: "example_master", ItemPosition: 0, TaskID: task.ID, AttemptNumber: 1, StartedAt: now}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(s.Config.StateDir, "example", train.ID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	return s, db, train, task
}

func TestSharedTrainStartAndTaskWorkDoNotReadHub(t *testing.T) {
	s, db, train, task := sharedActiveTrainFixture(t)
	defer db.Close()
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{ProjectID: "example", TrainID: train.ID, StartedBy: "planner"})
	if err != nil {
		t.Fatalf("Shared Train start read Hub while Hub was unavailable: %v", err)
	}
	if started.Attempt.Number != 1 || started.Attempt.AirelaySessionKey != "example_master" {
		t.Fatalf("unexpected Shared start result: %#v", started)
	}
	work, err := s.TaskWork(context.Background(), TaskWorkInput{ProjectID: "example", TaskID: task.ID})
	if err != nil {
		t.Fatalf("Shared TaskWork read Hub while Hub was unavailable: %v", err)
	}
	if work.TrainID != train.ID || work.AttemptNumber != 1 || work.Text == "" {
		t.Fatalf("unexpected Shared TaskWork result: %#v", work)
	}
}

func TestSharedTrainStartRejectsSecondTrainUsingSameSession(t *testing.T) {
	s, db, first, _ := sharedActiveTrainFixture(t)
	defer db.Close()
	now := time.Now().UTC()
	task, err := trainv2.NewTask("example", "EXM-TSK2", trainv2.AuthoringDraft{Title: "second", Objective: "collision", ADRRelation: model.TaskADRNoRequired}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	task, err = trainv2.ReadyTask(task, "planner", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := trainv2.New("example", "EXM-TRN2", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := now.Add(time.Minute)
	second.Status = model.TrainV2Running
	second.Items[0].Status = model.TrainV2ItemRunning
	second.Items[0].Attempts = []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "coder-example", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: strings.Repeat("b", 40), StartedAt: now, DispatchedAt: &dispatched}}
	second.Items[0].ActiveAttemptNumber = 1
	payload, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{ID: second.ID, Revision: int64(second.Revision), Payload: payload, UpdatedAt: now.Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSessionAvailableForTrainAttemptLocalFirst(context.Background(), "example_master", first.ID); err == nil || !strings.Contains(err.Error(), "already owns the project session") {
		t.Fatalf("same-session Shared Train collision was not rejected: %v", err)
	}
}
