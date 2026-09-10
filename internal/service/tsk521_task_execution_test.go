package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK521TaskDispatchCreatesOneFrozenTaskLane(t *testing.T) {
	s, _, _ := testService(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	agent, err := s.AgentRead(context.Background(), "example", "coder-example")
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	ctx := context.Background()
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(ctx, "project_configuration", sqlitestore.SharedEntity{ID: "example", Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	agentPayload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(ctx, sqlitestore.LocalAgent{ProjectID: "example", AgentID: agent.AgentID, Payload: agentPayload, UpdatedAt: agent.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	task, _, err := s.taskAuthoringCreateShared(ctx, "tsk521-create", TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Dispatch Task",
		Summary:     "Dispatch summary.",
		Objective:   "Dispatch this Task in one lane.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != model.TaskExecutionDispatched || first.Stage != "code" || first.ExecutionRevision != 1 || len(first.Head) != 8 || first.Head != strings.ToLower(first.Head) || first.Agent != "coder-example" {
		t.Fatalf("dispatch=%#v", first)
	}
	if strings.Contains(first.Worktree, "SA-") || strings.Contains(first.Agent, "SA-") || !strings.HasPrefix(first.Worktree, "WT-TSK") {
		t.Fatalf("public execution leaked session or lane identity: %#v", first)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || state.TaskRevision != task.Revision || state.TaskRevisionSHA256 != task.RevisionSHA256 || state.BaseHead != state.Head {
		t.Fatalf("frozen state=%#v found=%v err=%v", state, found, err)
	}
	second, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       task.ID,
	})
	if err != nil || second != first {
		t.Fatalf("repeat dispatch=%#v err=%v first=%#v", second, err, first)
	}
	if _, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       task.ID,
		Agent:     "other-coder",
	}); err == nil {
		t.Fatal("conflicting explicit Agent was accepted")
	}
}
