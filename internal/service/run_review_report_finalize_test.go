package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestRunReviewReportDraftFinalizeAndTaskFirstRead(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	ctx := context.Background()
	draft, err := s.TaskReviewReportStart(ctx, task.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.ID != model.NewRunReviewReportID(run.ID) || draft.DraftRevision != 1 {
		t.Fatalf("unexpected draft identity/revision: %#v", draft)
	}
	if _, err := s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		SectionID:             "gates",
		ExpectedDraftRevision: 1,
		Payload:               []byte(`[]`),
	}); err == nil {
		t.Fatal("machine section override accepted")
	}
	if _, err := s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		SectionID:             "outcome",
		ExpectedDraftRevision: 99,
		Payload:               []byte(`"accepted_reviewed_merge_ready"`),
	}); err == nil {
		t.Fatal("stale draft revision accepted")
	}
	sections := []struct {
		id      string
		payload string
	}{
		{"outcome", `"accepted_reviewed_merge_ready"`},
		{"findings", `[]`},
		{"scope_coverage", `[]`},
		{"unexpected_surfaces", `[]`},
		{"historical_compatibility", `[]`},
		{"prohibited_actions", `[]`},
		{"next_action", `"reviewed_merge_ready"`},
	}
	revision := 1
	for _, section := range sections {
		draft, err = s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{
			TaskID:                task.ID,
			RunID:                 run.ID,
			SectionID:             section.id,
			ExpectedDraftRevision: revision,
			Payload:               []byte(section.payload),
		})
		if err != nil {
			t.Fatalf("update %s: %v", section.id, err)
		}
		revision = draft.DraftRevision
	}
	validation, err := s.TaskReviewReportValidate(ctx, task.ID, run.ID)
	if err != nil || !validation.Valid {
		t.Fatalf("review validation failed: %#v %v", validation, err)
	}
	oldHub, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report, operation, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		ExpectedDraftRevision: revision,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: oldHub,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ID != model.NewRunReviewReportID(run.ID) || report.Outcome != model.ReviewOutcomeAccepted || operation.Status != "review_report_finalized" || len(operation.Hub.Paths) != 1 || !strings.HasSuffix(operation.Hub.Paths[0], "/review-report.json") {
		t.Fatalf("unexpected finalized review: %#v %#v", report, operation)
	}
	if _, err := s.TaskReviewReportStart(ctx, task.ID, run.ID); err == nil {
		t.Fatal("second report start accepted after immutable publication")
	}
	if _, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		ExpectedDraftRevision: revision,
	}); err == nil {
		t.Fatal("second report finalize unexpectedly succeeded")
	}
	read, err := s.TaskReportRead(ctx, task.ID, "")
	if err != nil || read.ID != report.ID || read.HubCommit != operation.Hub.After {
		t.Fatalf("task-first report read failed: %#v %v", read, err)
	}
	record, err := s.TaskReadRecord(ctx, task.ID)
	if err != nil || len(record.RunSummaries) != 1 || record.RunSummaries[0].DeliveryOutcome != model.ReviewOutcomeAccepted || record.RunSummaries[0].Blocker != "" {
		t.Fatalf("task review summary failed: %#v %v", record, err)
	}
}

func addNewerSucceededRun(t *testing.T, s *Service, task model.Task, base model.Run) model.Run {
	t.Helper()
	ctx := context.Background()
	agent, err := s.RunReport(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := base
	run.ID = "EXM-TSK1-RUN2"
	run.CreatedAt = base.CreatedAt.Add(time.Second)
	run.HubRevision = ""
	run.CompletionPath = filepath.Join(s.Config.StateDir, "runs", run.ID, "completion.json")
	report := agent
	report.RunID = run.ID
	report.HubCommit = ""
	latest, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(ctx, latest, "test: add newer successful run", func(worktree string) ([]string, error) {
		return []string{s.runPath(task.ProjectID, run.ID), s.reportPath(task.ProjectID, run.ID)}, func() error {
			if err := hub.WriteJSON(worktree, s.runPath(task.ProjectID, run.ID), run); err != nil {
				return err
			}
			return hub.WriteJSON(worktree, s.reportPath(task.ProjectID, run.ID), report)
		}()
	}); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestTaskReportReadAndMergeReadyNeverSubstituteOlderAcceptedRun(t *testing.T) {
	s, task, older := makeReviewableRun(t)
	ctx := context.Background()
	finalizeAcceptedDeliveryReview(t, s, task, older)
	newer := addNewerSucceededRun(t, s, task, older)
	if _, err := s.TaskReportRead(ctx, task.ID, ""); err == nil || !strings.Contains(err.Error(), newer.ID) {
		t.Fatalf("latest unreviewed run was not the blocker: %v", err)
	}
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskMarkMergeReady(ctx, TaskMarkMergeReadyInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}); err == nil || !strings.Contains(err.Error(), newer.ID) {
		t.Fatalf("merge-ready admitted older accepted report: %v", err)
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != revision {
		t.Fatalf("blocked merge-ready mutated hub: got %s want %s err=%v", got, revision, err)
	}
}
