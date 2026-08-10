package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestRunReviewSnapshotRejectsOversizedAggregate(t *testing.T) {
	s, hubRev, _ := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Bounded review",
		Objective:          "Review bounded output.",
		Slug:               "bounded",
		AcceptanceCriteria: []string{"bounded"},
		OperationClass:     "implementation",
		CreatedBy:          "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRev,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        "example",
		Title:            planString("Review"),
		Summary:          planString("Bounded review"),
		CurrentObjective: planString("Review."),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: create.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Config.MaxReadBytes = 100
	if _, err := s.RunReviewSnapshot(ctx, run.ID); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("expected explicit output bound error, got %v", err)
	}
}

func createActiveTailRun(t *testing.T, s *Service, hubRevision, projectHead string) model.Run {
	t.Helper()
	ctx := context.Background()
	revision := hubRevision
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Tail",
		Objective:          "Inspect tail.",
		Slug:               "tail",
		AcceptanceCriteria: []string{"tail"},
		OperationClass:     "implementation",
		CreatedBy:          "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        "example",
		Title:            planString("Tail"),
		Summary:          planString("Tail"),
		CurrentObjective: planString("Tail."),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: create.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestRunAgentTailUsesStoredSessionAndDefaultAndExplicitLines(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+log+"\"\nprintf 'tail text\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := createActiveTailRun(t, s, hubRevision, projectHead)
	s.Airelay.Command = script
	text, err := s.RunAgentTail(context.Background(), run.ID, 0)
	if err != nil || text != "tail text\n" {
		t.Fatalf("default tail=%q err=%v", text, err)
	}
	args, _ := os.ReadFile(log)
	if string(args) != "tail\nexample_master\n--lines\n200\n" {
		t.Fatalf("default argv=%q", args)
	}
	_, err = s.RunAgentTail(context.Background(), run.ID, 9)
	if err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(log)
	if !strings.HasSuffix(string(args), "--lines\n200\n") {
		t.Fatalf("explicit argv=%q", args)
	}
}

func TestRunAgentTailCursorUsesRunScope(t *testing.T) {
	s, hubRevision, projectHead := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	write := func(output string) {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '"+strings.ReplaceAll(output, "\n", "\\n")+"\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		s.Airelay.Command = script
	}
	run := createActiveTailRun(t, s, hubRevision, projectHead)
	write("one\ntwo\n")
	first, err := s.RunAgentTailPage(context.Background(), run.ID, AgentTailInput{Lines: 1})
	if err != nil || first.Text != "two\n" || first.NextCursor == "" {
		t.Fatalf("initial run tail=%#v err=%v", first, err)
	}
	write("one\ntwo\nthree\n")
	next, err := s.RunAgentTailPage(context.Background(), run.ID, AgentTailInput{
		Lines:  1,
		Cursor: first.NextCursor,
	})
	if err != nil || next.Text != "three\n" || next.HasMore {
		t.Fatalf("run delta=%#v err=%v", next, err)
	}
}

func TestRunAgentTailRejectsBoundsTerminalAndForeignBeforeAirelay(t *testing.T) {
	s, revision, _ := testService(t)
	now := time.Now().UTC()
	for _, run := range []model.Run{
		{SchemaVersion: 1, ID: "EXM-TSK99-RUN1", TaskID: "task", TaskSHA256: strings.Repeat("a", 64), ProjectID: "example", GatewayID: s.Config.GatewayID, SessionKey: "terminal_secret", Branch: "feature/tail", BaseRevision: strings.Repeat("a", 40), Status: "succeeded", CreatedAt: now},
		{SchemaVersion: 1, ID: "EXM-TSK99-RUN2", TaskID: "task", TaskSHA256: strings.Repeat("a", 64), ProjectID: "example", GatewayID: "other_gateway", SessionKey: "foreign_secret", Branch: "feature/tail", BaseRevision: strings.Repeat("a", 40), Status: "awaiting_result", CreatedAt: now},
	} {
		tx, err := s.Hub.Transact(context.Background(), revision, "test tail run", func(worktree string) ([]string, error) {
			path := s.runPath(run.ProjectID, run.ID)
			return []string{path}, hub.WriteJSON(worktree, path, run)
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = tx.After
	}
	for _, test := range []struct {
		id    string
		want  string
		lines int
	}{{"EXM-TSK99-RUN1", "run is not active", 4}, {"EXM-TSK99-RUN2", "assigned to gateway", 4}, {"EXM-TSK99-RUN2", "", 201}} {
		if _, err := s.RunAgentTail(context.Background(), test.id, test.lines); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s error=%v", test.id, err)
		}
	}
}
