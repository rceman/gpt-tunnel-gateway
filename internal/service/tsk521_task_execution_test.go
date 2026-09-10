package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
	lanePath, err := gitx.TaskWorktreePath(s.Config.StateDir, "example", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	actualHead, actualBranch, clean, err := s.Git.CurrentHead(ctx, config.ProjectConfig{Root: lanePath})
	if err != nil || !clean || actualHead != state.BaseHead || actualBranch != state.Branch || state.Worktree != taskExecutionWorktree(task.ID, strings.ToLower(actualHead[:8])) {
		t.Fatalf("server-owned lane identity path=%q head=%q branch=%q clean=%v state=%#v err=%v", lanePath, actualHead, actualBranch, clean, state, err)
	}
	if _, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       task.ID,
		Agent:     "other-coder",
	}); err == nil {
		t.Fatal("conflicting explicit Agent was accepted")
	}
}

func seedTSK521Agent(t *testing.T, s *Service, agentID string) {
	t.Helper()
	snapshot, err := s.Hub.ReadSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	revision := snapshot.Revision()
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	agent := model.Agent{SchemaVersion: model.AgentSchemaVersion, ProjectID: "example", AgentID: agentID, Role: model.AgentRoleCoding, Enabled: true, RecommendedReasoning: model.ReasoningHigh, CreatedAt: now, UpdatedAt: now}
	path := s.agentPath("example", agentID)
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed TSK521 Agent "+agentID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, path, agent); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.Config.AgentBindings == nil {
		s.Config.AgentBindings = map[string]config.AgentBinding{}
	}
	s.Config.AgentBindings[config.ProjectAgentBindingKey("example", agentID)] = config.AgentBinding{SessionKey: agentID + "_master", Profile: "coding"}
}

func TestTSK521TaskDispatchAgentSelectionFailsClosedAndExplicitlySelects(t *testing.T) {
	t.Run("zero attached", func(t *testing.T) {
		s, _, _ := testServiceSerial(t)
		s.Config.Projects["example"] = func() config.ProjectConfig {
			project := s.Config.Projects["example"]
			project.AirelaySessionKey = ""
			return project
		}()
		delete(s.Config.AgentBindings, config.ProjectAgentBindingKey("example", "coder-example"))
		if _, err := s.ResolveAgent(context.Background(), AgentResolveInput{
			ProjectID:       "example",
			Role:            model.AgentRoleCoding,
			RequireUnique:   true,
			RequireAttached: true,
		}); err == nil || !strings.Contains(err.Error(), "AGENT_NOT_AVAILABLE") {
			t.Fatalf("zero attached Agent error=%v", err)
		}
	})

	t.Run("multiple requires explicit and explicit selects", func(t *testing.T) {
		s, _, _ := testServiceSerial(t)
		seedTSK521Agent(t, s, "coder-second")
		if _, err := s.ResolveAgent(context.Background(), AgentResolveInput{
			ProjectID:       "example",
			Role:            model.AgentRoleCoding,
			RequireUnique:   true,
			RequireAttached: true,
		}); err == nil || !strings.Contains(err.Error(), "explicit agent is required") {
			t.Fatalf("multiple attached Agents error=%v", err)
		}
		resolved, err := s.ResolveAgent(context.Background(), AgentResolveInput{
			ProjectID:       "example",
			Role:            model.AgentRoleCoding,
			AgentID:         "coder-second",
			RequireAttached: true,
		})
		if err != nil || resolved.AgentID != "coder-second" || strings.Contains(resolved.SessionKey, "SA-") {
			t.Fatalf("explicit Agent resolution=%#v err=%v", resolved, err)
		}
	})
}

func TestTSK521ConcurrentDispatchConvergesToOneLane(t *testing.T) {
	s, _, _ := testServiceSerial(t)
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
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{ID: "example", Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	agentPayload, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLocalAgent(context.Background(), sqlitestore.LocalAgent{ProjectID: "example", AgentID: agent.AgentID, Payload: agentPayload, UpdatedAt: agent.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	task, _, err := s.taskAuthoringCreateShared(context.Background(), "tsk521-concurrent", TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Concurrent dispatch",
		Summary:     "Concurrent dispatch summary.",
		Objective:   "Dispatch concurrently.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan TaskExecutionPublicOutput, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			result, dispatchErr := s.TaskExecutionDispatch(context.Background(), TaskExecutionDispatchInput{
				ProjectID: "example",
				Key:       task.ID,
			})
			results <- result
			errs <- dispatchErr
		}()
	}
	close(start)
	first, second := <-results, <-results
	err1, err2 := <-errs, <-errs
	if err1 != nil || err2 != nil || first != second {
		t.Fatalf("concurrent dispatch first=%#v second=%#v errors=%v,%v", first, second, err1, err2)
	}
	if _, found, err := db.ReadTaskExecutionState(context.Background(), "example", task.ID); err != nil || !found {
		t.Fatalf("execution state found=%v err=%v", found, err)
	}
	if _, err := s.TaskExecutionDispatch(context.Background(), TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       task.ID,
		Agent:     "other-coder",
	}); err == nil {
		t.Fatal("conflicting explicit Agent after concurrent winner was accepted")
	}
}
