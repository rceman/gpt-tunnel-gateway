package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestSameAgentAuthorityCanonicalizesOptionalServerGateResults(t *testing.T) {
	left := model.Report{}
	right := model.Report{ServerGateResults: []model.CompletionGateResult{}}
	if !sameAgentAuthority(left, right) {
		t.Fatal("semantically equivalent legacy and canonical reports were treated as different")
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
			if _, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
				TaskID:                task.ID,
				RunID:                 run.ID,
				ExpectedDraftRevision: draft.DraftRevision,
			}); err == nil {
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
