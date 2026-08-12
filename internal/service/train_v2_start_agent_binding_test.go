package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
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
