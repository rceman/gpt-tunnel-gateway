package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func TestTrainV2StartUsesSingleCodingAgentAutoBinding(t *testing.T) {
	s, hubRevision, _ := testService(t)
	delete(s.Config.AgentBindings, config.ProjectAgentBindingKey("example", "coder-example"))
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Auto-bound Train start")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Run.AgentID != "coder-example" || started.Run.SessionKey != "example_master" || started.Record.Status != model.TrainV2StartActive {
		t.Fatalf("Train/start did not preserve the auto-bound host identity: %#v", started)
	}
}

func TestTrainV2StartMaterializesBoundedPacketAndDispatchesExactPaths(t *testing.T) {
	s, hubRevision, _ := testService(t)
	calls := filepath.Join(t.TempDir(), "prompt-calls")
	if err := os.WriteFile(s.Config.AirelayCommand, []byte("#!/bin/sh\nif [ \"$1\" = prompt ]; then printf '%s\\n' \"$@\" > "+calls+"; fi\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'idle\\n' ;;\nprompt) exit 0 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	adrOperation, err := s.ADRCreate(context.Background(), ADRCreateInput{
		ADR: model.ADR{ProjectID: "example", Title: "Train packet context", Status: "accepted", Context: "packet context", Decision: "use the bounded Train packet", Consequences: "the Agent receives one durable packet"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:             "example",
		Title:                 "Materialized Train packet",
		Objective:             "Produce one complete bounded Task packet for the Agent.",
		AcceptanceCriteria:    []string{"objective and acceptance are preserved", "packet remains bounded"},
		Constraints:           []string{"use only the packet and owned worktree", "do not use legacy dispatch"},
		Priority:              "high",
		Metadata:              map[string]string{"packet-contract": "full"},
		Dependencies:          []string{"EXM-TSK1"},
		PreparationReferences: []string{"docs/train-packet.md"},
		ADRRelation:           model.TaskADRImplementsExisting,
		ADRReferences:         []string{"EXM-ADR1"},
		CreatedBy:             "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: adrOperation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, readyOperation, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil || ready.Status != model.TaskAuthoringReady {
		t.Fatalf("ready task failed: %#v %v", ready, err)
	}
	hubRevision = readyOperation.Hub.After
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	packetPath := filepath.Join(s.Config.StateDir, "runs", started.Run.ID, "task-packet.md")
	packet, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatalf("Train start did not materialize packet: %v", err)
	}
	if len(packet) == 0 || len(packet) > 256<<10 {
		t.Fatalf("packet is not bounded: %d bytes", len(packet))
	}
	packetText := string(packet)
	for _, required := range []string{
		task.ID, task.Title, task.Objective, started.Run.ID, train.ID, started.Runtime.WorktreePath,
		started.Run.BaseRevision, "objective and acceptance are preserved", "packet remains bounded",
		"use only the packet and owned worktree", "do not use legacy dispatch", "EXM-TSK1", "docs/train-packet.md",
		model.TaskADRImplementsExisting, "EXM-ADR1",
		"Task status: ready", "Task priority: high", "packet-contract: full", "ready_by=planner",
	} {
		if !strings.Contains(packetText, required) {
			t.Fatalf("packet is missing %q: %s", required, packetText)
		}
	}
	if strings.Contains(packetText, "gpt-tunnel task read") || strings.Contains(packetText, "session.start") {
		t.Fatalf("Train packet retained a legacy CLI/MCP session dependency: %s", packetText)
	}
	callData, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(callData), "\n"), "\n")
	if len(lines) != 3 || lines[0] != "prompt" || lines[1] != "example_master" {
		t.Fatalf("unexpected dispatch argv capture: %q", string(callData))
	}
	wantPrompt := "Read " + packetPath + " and execute."
	if lines[2] != wantPrompt {
		t.Fatalf("dispatch prompt must contain only the exact packet path: got=%q want=%q", lines[2], wantPrompt)
	}
}

func TestTrainV2AutoAdvanceMaterializesNextTaskPacketAndDispatchesPacketOnly(t *testing.T) {
	s, hubRevision, _ := testService(t)
	calls := filepath.Join(t.TempDir(), "prompt-calls")
	if err := os.WriteFile(s.Config.AirelayCommand, []byte("#!/bin/sh\nif [ \"$1\" = prompt ]; then printf '%s\\n' \"$@\" > "+calls+"; fi\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'idle\\n' ;;\nprompt) exit 0 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	first, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "First auto-advance packet")
	second, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Second auto-advance packet")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{first.ID, second.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	currentTrain, err := s.TrainV2Read(context.Background(), "example", train.ID)
	if err != nil {
		t.Fatal(err)
	}
	nextItem := currentTrain.Items[1]
	nextID := second.ID + "-RUN1"
	now := time.Now().UTC()
	nextRun, err := trainv2.BuildNextRun(trainv2.NextRunInput{
		Current: started.Run, Next: nextItem, RunID: nextID, BaseRevision: started.Run.BaseRevision,
		StateDir: s.Config.StateDir, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedTrain, err := watcher.StartNextItem(currentTrain, watcher.AdvancePlan{
		Current: currentTrain.Items[0], Next: nextItem, AgentID: started.Run.AgentID,
		SessionKey: started.Run.SessionKey, WorktreePath: started.Runtime.WorktreePath, LaneBranch: started.Run.LaneBranch,
	}, nextID, started.Run.BaseRevision, now)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seed, err := s.Hub.Transact(context.Background(), expected, "test: seed next Train item", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, s.trainV2Path("example", train.ID), updatedTrain); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.runPath("example", nextID), nextRun); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path("example", train.ID), s.runPath("example", nextID)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.dispatchTrainV2Continuation(context.Background(), started.Runtime, nextRun, seed.After, now); err != nil {
		t.Fatal(err)
	}
	packetPath := filepath.Join(s.Config.StateDir, "runs", nextID, "task-packet.md")
	packet, err := os.ReadFile(packetPath)
	if err != nil {
		t.Fatalf("next Train item did not materialize packet: %v", err)
	}
	packetText := string(packet)
	for _, required := range []string{second.ID, second.Title, second.Objective, nextID, train.ID, started.Runtime.WorktreePath, nextRun.BaseRevision} {
		if !strings.Contains(packetText, required) {
			t.Fatalf("next packet is missing %q: %s", required, packetText)
		}
	}
	nextRunState, err := s.RunRead(context.Background(), nextID)
	if err != nil || nextRunState.Status != "dispatched" {
		t.Fatalf("next Train Run was not dispatched: %#v err=%v", nextRunState, err)
	}
	callData, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(callData), "\n"), "\n")
	if len(lines) != 3 || lines[0] != "prompt" || lines[1] != "example_master" {
		t.Fatalf("unexpected next dispatch argv capture: %q", string(callData))
	}
	wantPrompt := "Read " + packetPath + " and execute."
	if lines[2] != wantPrompt || strings.Contains(lines[2], started.Runtime.WorktreePath) {
		t.Fatalf("next dispatch must contain only the exact packet path: got=%q want=%q", lines[2], wantPrompt)
	}
}

func TestTrainV2StartRecoversLegacyDispatchedRunWithOnePacketReprompt(t *testing.T) {
	s, hubRevision, _ := testService(t)
	calls := filepath.Join(t.TempDir(), "prompt-calls")
	if err := os.WriteFile(s.Config.AirelayCommand, []byte("#!/bin/sh\nif [ \"$1\" = prompt ]; then printf '%s\\n' \"$@\" > "+calls+"; fi\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'idle\\n' ;;\nprompt) exit 0 ;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Recover packetized Train Run")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	packetPath := filepath.Join(s.Config.StateDir, "runs", started.Run.ID, "task-packet.md")
	if err := os.Remove(packetPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(trainv2.RuntimePath(s.Config.StateDir, "example", train.ID) + ".dispatch.json"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	legacyMessage := "Resume train item " + started.Run.TaskID + ". Use the existing server-owned Train worktree."
	expected, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Hub.Transact(context.Background(), expected, "test: seed legacy Train dispatch", func(worktree string) ([]string, error) {
		var current model.Run
		if err := readWorktreeJSON(worktree, s.runPath("example", started.Run.ID), &current); err != nil {
			return nil, err
		}
		current.DispatchMessage = legacyMessage
		if err := hub.WriteJSON(worktree, s.runPath("example", started.Run.ID), current); err != nil {
			return nil, err
		}
		return []string{s.runPath("example", started.Run.ID)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: legacy.After,
		},
	}); err != nil {
		t.Fatalf("legacy dispatched Run was not packetized: %v", err)
	}
	recovered, err := s.RunRead(context.Background(), started.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedMessage := "Read " + packetPath + " and execute."
	if recovered.Status != "dispatched" || recovered.DispatchMessage != expectedMessage {
		t.Fatalf("legacy dispatch was not durably upgraded: %#v", recovered)
	}
	if _, err := os.Stat(packetPath); err != nil {
		t.Fatalf("recovery did not recreate packet: %v", err)
	}
	callData, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(callData), "\n"), "\n")
	if len(lines) != 3 || lines[2] != expectedMessage {
		t.Fatalf("recovery prompt was not packet-only: %q", string(callData))
	}
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
	}); err != nil {
		t.Fatalf("repeat packetized Train start failed: %v", err)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("repeat Train start reprompted after durable packet dispatch: err=%v", err)
	}
}
