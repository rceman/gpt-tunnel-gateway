package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func newPMTTestService(t *testing.T) (*Service, *sqlitestore.Databases) {
	t.Helper()
	dir := t.TempDir()
	command := filepath.Join(dir, "airelay")
	script := "#!/bin/sh\nif [ \"$1\" = prompt ]; then exit 0; fi\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{StateDir: filepath.Join(dir, "state"), AirelayCommand: command, DispatchTimeoutSeconds: 5, Projects: map[string]config.ProjectConfig{
		"example": {Root: dir, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_master"},
	}}
	db, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewWithDurability(c, db), db
}

func TestAgentPromptCreatesPMTAndSendsReferenceOnly(t *testing.T) {
	dir := t.TempDir()
	messagePath := filepath.Join(dir, "message")
	command := filepath.Join(dir, "airelay")
	script := "#!/bin/sh\nif [ \"$1\" = prompt ]; then printf '%s' \"$3\" > '" + messagePath + "'; fi\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{StateDir: filepath.Join(dir, "state"), AirelayCommand: command, DispatchTimeoutSeconds: 5, Projects: map[string]config.ProjectConfig{
		"example": {Root: dir, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_master"},
	}}
	db, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := NewWithDurability(c, db)
	result, err := s.AgentPrompt(WithAgentSessionID(context.Background(), "SP-ABCDEFGH"), "example", "secret instruction")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Queued || result.Delivered || result.PMTID != "EXM-PMT1" {
		t.Fatalf("result=%#v", result)
	}
	wire, err := os.ReadFile(messagePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), "read prompt: gpt-tunnel prompt EXM-PMT1") || strings.Contains(string(wire), "secret instruction") {
		t.Fatalf("wire=%q", wire)
	}
	read, err := s.PMTRead(context.Background(), result.PMTID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Instruction != "secret instruction" || read.State != "fetched" {
		t.Fatalf("read=%#v", read)
	}
	repeat, err := s.PMTRead(context.Background(), result.PMTID)
	if err != nil || repeat.Instruction != read.Instruction || repeat.ReadCount != read.ReadCount+1 {
		t.Fatalf("repeat=%#v err=%v", repeat, err)
	}
}

func TestPMTQueueCancelSupersedeAndStaleReference(t *testing.T) {
	s, _ := newPMTTestService(t)
	ctx := WithAgentSessionID(context.Background(), "SP-ABCDEFGH")
	first, err := s.AgentPrompt(ctx, "example", "first instruction")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AgentPrompt(ctx, "example", "second instruction")
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.AgentPrompt(ctx, "example", "third instruction")
	if err != nil {
		t.Fatal(err)
	}
	queue, err := s.PMTQueue(context.Background(), "example", 8)
	if err != nil || queue.Queue.QueueCount != 3 || len(queue.Queue.Entries) != 3 {
		t.Fatalf("initial queue=%#v err=%v", queue, err)
	}
	cancelled, err := s.PMTCancel(ctx, "example", second.PMTID)
	if err != nil || !cancelled.Cancelled || cancelled.Queue.QueueCount != 2 {
		t.Fatalf("cancel=%#v err=%v", cancelled, err)
	}
	replacement, err := s.PMTSupersede(ctx, PMTSupersedeInput{
		ProjectID: "example",
		OldIDs:    []string{first.PMTID},
		Title:     "replacement",
		Message:   "replacement instruction",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.PMTID == first.PMTID || replacement.Queue == nil || replacement.Queue.QueueCount != 2 {
		t.Fatalf("replacement=%#v", replacement)
	}
	if replacement.Queue.Entries[0].ID != third.PMTID || replacement.Queue.Entries[1].ID != replacement.PMTID {
		t.Fatalf("replacement queue=%#v", replacement.Queue)
	}
	stale, err := s.PMTRead(context.Background(), first.PMTID)
	if err != nil || !stale.Tombstone || stale.State != model.PMTStateSuperseded || stale.Instruction != "" {
		t.Fatalf("stale=%#v err=%v", stale, err)
	}
}

func TestPMTReadRejectsWrongSessionAndStaleExecution(t *testing.T) {
	s, db := newPMTTestService(t)
	ctx := context.Background()
	wrongSession := testPMTForService()
	wrongSession, err := db.CreatePMT(ctx, wrongSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PMTRead(WithAgentSessionID(ctx, "SP-WRONG123"), wrongSession.ID); err == nil {
		t.Fatal("wrong session was accepted")
	}
	stale := testPMTForService()
	stale.TrainID = "EXM-TRN1"
	stale.TaskID = "EXM-TSK1"
	stale.AttemptNumber = 1
	stale, err = db.CreatePMT(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PMTRead(ctx, stale.ID); err == nil {
		t.Fatal("stale execution was accepted")
	}
}

func TestPMTExpiredReadReturnsTombstone(t *testing.T) {
	s, db := newPMTTestService(t)
	expires := time.Now().UTC().Add(-time.Minute)
	pmt := testPMTForService()
	pmt.ExpiresAt = &expires
	pmt, err := db.CreatePMT(context.Background(), pmt)
	if err != nil {
		t.Fatal(err)
	}
	read, err := s.PMTRead(context.Background(), pmt.ID)
	if err != nil || !read.Tombstone || read.State != model.PMTStateExpired || read.Instruction != "" {
		t.Fatalf("expired read=%#v err=%v", read, err)
	}
}

func testPMTForService() model.PMT {
	return model.PMT{
		SchemaVersion: model.PMTSchemaVersion, ProjectID: "example", ProjectCode: "EXM",
		Title: "bounded title", Instruction: "durable instruction", PlannerSessionID: "SP-ABCDEFGH",
		TargetAirelaySessionKey: "example_master", TargetAgentID: "coding", CreatedAt: time.Now().UTC(),
		State: model.PMTStateUnread, Reference: "pending",
	}
}

func TestInterruptReplacementQueuesPMTReference(t *testing.T) {
	s, hubRevision, _ := testService(t)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Interrupt replacement")
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
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	t.Cleanup(func() { _ = db.Close() })
	projectConfig := s.Config.Projects["example"]
	projectConfig.ProjectCode = "EXM"
	s.Config.Projects["example"] = projectConfig
	if err := s.BootstrapSharedFromHub(context.Background()); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	messagePath := filepath.Join(dir, "message")
	command := filepath.Join(dir, "airelay")
	script := "#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: running\\n' ;;\ninterrupt) printf '{\"outcome\":\"interrupt_acknowledged\",\"requested\":true}\\n' ;;\nprompt) printf '%s' \"$3\" > '" + messagePath + "' ;;\nesac\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Config.AirelayCommand = command
	s.Airelay.Command = command
	result, err := s.AgentInterrupt(WithAgentSessionID(context.Background(), "SP-ABCDEFGH"), AgentInterruptInput{
		OperationID:   "EXM-INT1",
		ProjectID:     "example",
		TrainID:       train.ID,
		ItemPosition:  0,
		TaskID:        task.ID,
		AttemptNumber: started.Attempt.Number,
		AgentID:       started.Attempt.AgentID,
		Message:       "replacement body must stay local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "completed" || result.PromptOutcome != "queued" || result.PromptDelivered {
		t.Fatalf("interrupt result=%#v", result)
	}
	wire, err := os.ReadFile(messagePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), "read prompt: gpt-tunnel prompt EXM-PMT1") || strings.Contains(string(wire), "replacement body") {
		t.Fatalf("interrupt wire=%q", wire)
	}
	read, err := db.ReadPMT(context.Background(), "EXM-PMT1")
	if err != nil || read.Instruction != "replacement body must stay local" || read.State != model.PMTStateUnread {
		t.Fatalf("replacement PMT=%#v err=%v", read, err)
	}
}
