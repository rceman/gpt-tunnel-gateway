package service

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestExistingTrainAttemptOwnsStartAgentAndSession(t *testing.T) {
	stateDir := t.TempDir()
	s := &Service{Config: config.Config{StateDir: stateDir}}
	now := time.Now().UTC()
	task, err := trainv2.NewTask("example", "EXM-TSK1", trainv2.AuthoringDraft{Title: "authority", Objective: "resume", ADRRelation: model.TaskADRNoRequired}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	task, err = trainv2.ReadyTask(task, "planner", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	train, err := trainv2.New("example", "EXM-TRN1", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "coder-example", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: strings.Repeat("a", 40), StartedAt: now}
	train.Status = model.TrainV2Running
	train.Items[0].Status = model.TrainV2ItemRunning
	train.Items[0].Attempts = []model.TrainV2Attempt{attempt}
	train.Items[0].ActiveAttemptNumber = 1
	worktreePath, err := trainv2.CompactWorktreePath(stateDir, "EXM", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := trainv2.RuntimeBinding{SchemaVersion: 1, ProjectID: "example", ProjectCode: "EXM", TrainID: train.ID, WorktreePath: worktreePath, AgentID: "coder-example", SessionKey: "example_master", ItemPosition: 0, TaskID: task.ID, AttemptNumber: 1, StartedAt: now}
	if err := fsutil.WriteJSONAtomic(trainv2.RuntimePath(stateDir, "example", train.ID), runtime, 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, existing, err := s.deriveExistingTrainAttemptAuthority("example", train.ID, train, "")
	if err != nil || !existing {
		t.Fatalf("derive existing Attempt authority: existing=%v err=%v", existing, err)
	}
	if resolved.AgentID != attempt.AgentID || resolved.SessionKey != attempt.AirelaySessionKey {
		t.Fatalf("authority was not derived from Attempt: %#v", resolved)
	}
}
