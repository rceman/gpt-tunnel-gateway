package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTaskCorrectionDispatchPinsReviewedHeadAfterCanonicalAdvance(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	ctx := context.Background()
	draft, err := s.TaskReviewReportStart(ctx, task.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []struct {
		id      string
		payload string
	}{
		{"outcome", `"rejected_needs_correction"`},
		{"findings", `[{"id":"F1","severity":"high","title":"Correction required","detail":"A bounded correction is required."}]`},
		{"scope_coverage", `[]`},
		{"unexpected_surfaces", `[]`},
		{"historical_compatibility", `[]`},
		{"prohibited_actions", `[]`},
		{"next_action", `"dispatch the bounded correction"`},
	} {
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
	delivery, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		ExpectedDraftRevision: draft.DraftRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	current := installTaskLifecycleState(t, s, task, model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "ready", UpdatedAt: time.Now().UTC()}, delivery.HubCommit)
	revision, correctionOp, err := s.TaskCorrectionCreate(ctx, TaskCorrectionCreateInput{
		TaskID:           task.ID,
		SourceRevisionID: task.ID + ".REV1",
		SourceRunID:      run.ID,
		SourceReportID:   delivery.ID,
		Objective:        "Dispatch the reviewed correction from its reviewed source head.",
		CreatedBy:        "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: current,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.BaseRevision != delivery.ReviewedHead {
		t.Fatalf("correction revision base mismatch: got=%s want=%s", revision.BaseRevision, delivery.ReviewedHead)
	}

	project := s.Config.Projects[task.ProjectID]
	advanceRoot := filepath.Join(t.TempDir(), "advance")
	testutil.Git(t, project.Root, "worktree", "add", "--detach", advanceRoot, "main")
	defer testutil.Git(t, project.Root, "worktree", "remove", "--force", advanceRoot)
	if err := os.WriteFile(filepath.Join(advanceRoot, "correction-dispatch.txt"), []byte("advance canonical main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, advanceRoot, "add", "correction-dispatch.txt")
	testutil.Git(t, advanceRoot, "commit", "-m", "advance canonical main after review")
	testutil.Git(t, advanceRoot, "push", "origin", "HEAD:main")
	advancedHead := strings.TrimSpace(testutil.Git(t, advanceRoot, "rev-parse", "HEAD"))
	if advancedHead == delivery.ReviewedHead {
		t.Fatal("canonical main did not advance")
	}

	plan, err := s.PlanRead(ctx, task.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	activated, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        task.ProjectID,
		Title:            planString(plan.Title),
		Summary:          planString(plan.Summary),
		CurrentObjective: planString(plan.CurrentObjective),
		ActiveTaskID:     planString(task.ID),
		UpdatedBy:        "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: correctionOp.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatched, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: activated.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dispatched.BaseRevision != delivery.ReviewedHead {
		t.Fatalf("correction dispatch followed canonical main instead of reviewed head: got=%s want=%s", dispatched.BaseRevision, delivery.ReviewedHead)
	}
	if dispatched.BaseRevision == advancedHead {
		t.Fatal("correction dispatch used the advanced canonical main")
	}
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	if status.Head != delivery.ReviewedHead || status.Branch != revision.Branch || !status.Clean {
		t.Fatalf("correction worktree was not prepared from reviewed head: %#v", status)
	}
}
