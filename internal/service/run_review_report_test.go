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
		draft, err = s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{TaskID: task.ID, RunID: run.ID, SectionID: section.id, ExpectedDraftRevision: draft.DraftRevision, Payload: []byte(section.payload)})
		if err != nil {
			t.Fatalf("update %s: %v", section.id, err)
		}
	}
	report, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{TaskID: task.ID, RunID: run.ID, ExpectedDraftRevision: draft.DraftRevision})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

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
	if _, err := s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{TaskID: task.ID, RunID: run.ID, SectionID: "gates", ExpectedDraftRevision: 1, Payload: []byte(`[]`)}); err == nil {
		t.Fatal("machine section override accepted")
	}
	if _, err := s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{TaskID: task.ID, RunID: run.ID, SectionID: "outcome", ExpectedDraftRevision: 99, Payload: []byte(`"accepted_reviewed_merge_ready"`)}); err == nil {
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
		draft, err = s.TaskReviewReportSectionUpdate(ctx, TaskReviewReportSectionUpdateInput{TaskID: task.ID, RunID: run.ID, SectionID: section.id, ExpectedDraftRevision: revision, Payload: []byte(section.payload)})
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
	report, operation, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{TaskID: task.ID, RunID: run.ID, ExpectedDraftRevision: revision, WriteOptions: WriteOptions{ExpectedHubRevision: oldHub}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ID != model.NewRunReviewReportID(run.ID) || report.Outcome != model.ReviewOutcomeAccepted || operation.Status != "review_report_finalized" || len(operation.Hub.Paths) != 1 || !strings.HasSuffix(operation.Hub.Paths[0], "/review-report.json") {
		t.Fatalf("unexpected finalized review: %#v %#v", report, operation)
	}
	if _, err := s.TaskReviewReportStart(ctx, task.ID, run.ID); err == nil {
		t.Fatal("second report start accepted after immutable publication")
	}
	if _, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{TaskID: task.ID, RunID: run.ID, ExpectedDraftRevision: revision}); err == nil {
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
	if _, err := s.TaskMarkMergeReady(ctx, TaskMarkMergeReadyInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: revision}}); err == nil || !strings.Contains(err.Error(), newer.ID) {
		t.Fatalf("merge-ready admitted older accepted report: %v", err)
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != revision {
		t.Fatalf("blocked merge-ready mutated hub: got %s want %s err=%v", got, revision, err)
	}
}

func TestRunReviewReportFinalizationDetectsChangedMachineAuthority(t *testing.T) {
	for _, name := range []string{"gates", "changed_files", "repository_state"} {
		t.Run(name, func(t *testing.T) {
			s, task, run := makeReviewableRun(t)
			ctx := context.Background()
			draft, err := s.TaskReviewReportStart(ctx, task.ID, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Hub.Transact(ctx, before, "test: mutate agent machine authority", func(worktree string) ([]string, error) {
				path := s.reportPath(task.ProjectID, run.ID)
				var report model.Report
				if err := readWorktreeJSON(worktree, path, &report); err != nil {
					return nil, err
				}
				switch name {
				case "gates":
					report.GateResults = []model.CompletionGateResult{{ID: "G1", ExitCode: 0}}
				case "changed_files":
					report.Repository.ChangedFiles = []string{"synthetic.txt"}
				case "repository_state":
					report.Repository.WorktreeClean = !report.Repository.WorktreeClean
				}
				return []string{path}, hub.WriteJSON(worktree, path, report)
			}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{TaskID: task.ID, RunID: run.ID, ExpectedDraftRevision: draft.DraftRevision}); err == nil {
				t.Fatal("changed Agent machine authority was published")
			}
			if _, err := s.Hub.ReadFile(ctx, s.reviewReportPath(task.ProjectID, run.ID)); err == nil {
				t.Fatal("failed finalization created immutable Delivery report")
			}
			if _, err := s.readReviewDraft(run.ID); err != nil {
				t.Fatalf("failed finalization did not preserve draft: %v", err)
			}
		})
	}
}
