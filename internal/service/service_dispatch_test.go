package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskPlanDispatchReadFinalize(t *testing.T) {
	s, hubRev, _ := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Implement feature",
		Objective:          "Implement exact behavior.",
		Slug:               "example",
		AcceptanceCriteria: []string{"feature works"},
		Constraints:        []string{"no redesign"},
		RequiredGates:      []string{"go test ./..."},
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
		Title:            planString("Implementation"),
		Summary:          planString("Implement feature"),
		CurrentObjective: planString("Execute the prepared task."),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "gpt",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: create.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatch, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "awaiting_result" {
		t.Fatalf("status=%s", run.Status)
	}
	wantDispatchMessage := "Read task and execute it. Run: gpt-tunnel task read " + task.ID + ". Do not stop at reading or summarizing: implement the task, run its required gates, write completion, and finalize until TASK_FINALIZED; if execution is blocked, report the explicit blocker."
	if run.DispatchMessage != wantDispatchMessage {
		t.Fatalf("dispatch message = %q, want %q", run.DispatchMessage, wantDispatchMessage)
	}
	packet, err := s.TaskRead(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Run.ID != run.ID || packet.FinalizeCommand == "" {
		t.Fatalf("bad packet: %#v", packet)
	}
	publicPacket, err := json.Marshal(PublicTaskPacketView(packet))
	if err != nil {
		t.Fatal(err)
	}
	configuredRoot := s.Config.Projects["example"].Root
	if !strings.Contains(string(publicPacket), configuredRoot) || strings.Contains(string(publicPacket), run.CompletionPath) || !strings.Contains(string(publicPacket), "gpt-tunnel run finalize "+run.ID+" --summary <text>") || strings.Contains(string(publicPacket), "write-completion") || strings.Contains(string(publicPacket), "--completion-file") {
		t.Fatalf("active execution packet exposed the wrong completion authority: %s", publicPacket)
	}
	project := s.Config.Projects["example"]
	if err := os.WriteFile(filepath.Join(project.Root, "feature.txt"), []byte("done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "feature.txt")
	testutil.Git(t, project.Root, "commit", "-m", "implement feature")
	testutil.Git(t, project.Root, "push", "-u", "origin", task.Branch)
	completion := model.Completion{SchemaVersion: 1, RunID: run.ID, TaskSHA256: task.SHA256, Status: "succeeded", Summary: "Implemented.", GateResults: []model.CompletionGateResult{{ID: "G1", ExitCode: 0}}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := fsutil.WriteJSONAtomic(run.CompletionPath, completion, 0o600); err != nil {
		t.Fatal(err)
	}
	report, final, err := s.RunFinalize(ctx, FinalizeInput{
		RunID: run.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: dispatch.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "succeeded" || final.Status != "TASK_FINALIZED" {
		t.Fatalf("bad final: %#v %#v", report, final)
	}
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReviewState != "reviewable" {
		t.Fatalf("expected reviewable snapshot, got %s checks=%#v", snapshot.ReviewState, snapshot.Checks)
	}
	if snapshot.Report.HubCommit == "" || !snapshot.Evidence.Available || !snapshot.Repository.TaskBranchPublished {
		t.Fatalf("missing canonical review proof: %#v", snapshot)
	}
}

func TestRunReviewSnapshotActiveIsBounded(t *testing.T) {
	s, hubRev, _ := testService(t)
	ctx := context.Background()
	task, create, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Review feature",
		Objective:          "Review exact behavior.",
		Slug:               "review",
		AcceptanceCriteria: []string{"feature works"},
		Constraints:        []string{"no redesign"},
		RequiredGates:      []string{"go test ./..."},
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
		Summary:          planString("Review feature"),
		CurrentObjective: planString("Execute the prepared task."),
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
	snapshot, err := s.RunReviewSnapshot(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReviewState != "active" || snapshot.NextAction != "wait_for_terminal" {
		t.Fatalf("snapshot state=%s next=%s", snapshot.ReviewState, snapshot.NextAction)
	}
	if snapshot.Report.Available || snapshot.Evidence.Available {
		t.Fatal("active snapshot exposed terminal artifacts")
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"session_key", "result_path", "evidence_path", "dispatch_stdout", "dispatch_stderr"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("snapshot exposed forbidden field %q", forbidden)
		}
	}
}
