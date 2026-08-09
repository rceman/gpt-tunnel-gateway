package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTaskReviewReportDraftUsesOptimisticRevisionAndTaskRunBinding(t *testing.T) {
	s, _, _ := testService(t)
	ctx := context.Background()
	if _, err := s.TaskReviewReportStart(ctx, "task-1", "run-1"); err == nil {
		t.Fatal("missing task/run unexpectedly accepted")
	}
	if _, err := s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{
		TaskID:                "task-1",
		RunID:                 "run-1",
		SectionID:             "outcome",
		ExpectedDraftRevision: 0,
		Payload:               []byte(`"accepted_reviewed_merge_ready"`),
	}); err == nil {
		t.Fatal("unbound draft update unexpectedly accepted")
	}
}

func TestTaskReportReadRequiresExactTaskOwnership(t *testing.T) {
	s, _, _ := testService(t)
	_, err := s.TaskReportRead(context.Background(), "task-1", "run-1")
	if err == nil {
		t.Fatal("report read for an unowned run unexpectedly succeeded")
	}
}

func makeReviewableRun(t *testing.T) (*Service, model.Task, model.Run) {
	t.Helper()
	s, _, _, _ := dispatchedRun(t, "feature/review-report")
	ctx := context.Background()
	local, err := s.projectConfig("example")
	if err != nil {
		t.Fatal(err)
	}
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := s.RunList(ctx, "example")
	if err != nil || len(runs) != 1 {
		t.Fatalf("unexpected dispatched runs: %#v %v", runs, err)
	}
	run := runs[0]
	task, err := s.findTask(ctx, run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	proof, risks, err := s.durableRepositoryProof(ctx, run, local, head, branch, clean, false)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	run.FinishedAt = func() *time.Time { v := time.Now().UTC(); return &v }()
	report := model.Report{SchemaVersion: model.SchemaVersion, TaskID: task.ID, RunID: run.ID, ProjectID: task.ProjectID, Status: "succeeded", Summary: "synthetic reviewable run", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{"AC1"}, Deviations: []string{}, RemainingRisks: risks, Repository: proof, FinishedAt: *run.FinishedAt}
	state := model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", UpdatedAt: time.Now().UTC()}
	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Revision++
	plan.ActiveTaskID = ""
	plan.ActiveRunID = ""
	plan.UpdatedBy = "test"
	plan.UpdatedAt = time.Now().UTC()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(ctx, revision, "test: complete synthetic reviewable run", func(w string) ([]string, error) {
		paths := []string{s.runPath(task.ProjectID, run.ID), s.reportPath(task.ProjectID, run.ID), s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID)}
		if err := hub.WriteJSON(w, paths[0], run); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, paths[1], report); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, paths[2], state); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(w, paths[3], plan); err != nil {
			return nil, err
		}
		return paths, nil
	}); err != nil {
		t.Fatal(err)
	}
	return s, task, run
}

func finalizeAcceptedDeliveryReview(t *testing.T, s *Service, task model.Task, run model.Run) model.RunReviewReport {
	t.Helper()
	ctx := context.Background()
	draft, err := s.TaskReviewReportStart(ctx, task.ID, run.ID)
	if err != nil {
		t.Fatal(err)
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
	for _, section := range sections {
		draft, err = s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{
			TaskID:                task.ID,
			RunID:                 run.ID,
			SectionID:             section.id,
			ExpectedDraftRevision: draft.DraftRevision,
			Payload:               []byte(section.payload),
		})
		if err != nil {
			t.Fatalf("update %s: %v", section.id, err)
		}
	}
	report, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		ExpectedDraftRevision: draft.DraftRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}
