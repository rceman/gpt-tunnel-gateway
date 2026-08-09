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

func TestTaskRevisionListReadAndStatusExposeStableRevisionOne(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, result, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Title:              "Revision listing",
		Objective:          "Read stable task revisions.",
		Slug:               "revision-listing",
		AcceptanceCriteria: []string{"read"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := s.TaskRevisionList(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 || revisions[0].ID != task.ID+".REV1" || revisions[0].RevisionSHA256 != task.SHA256 {
		t.Fatalf("unexpected revisions: %#v", revisions)
	}
	read, err := s.TaskRevisionRead(context.Background(), task.ID+".REV1")
	if err != nil {
		t.Fatal(err)
	}
	if read.ID != revisions[0].ID || read.RevisionSHA256 != revisions[0].RevisionSHA256 {
		t.Fatalf("read revision mismatch: %#v", read)
	}
	status, err := s.TaskRevisionStatus(context.Background(), task.ID+".REV1")
	if err != nil {
		t.Fatal(err)
	}
	if status.ID != read.ID || status.Status != read.Status || status.TaskRevision != 1 {
		t.Fatalf("status mismatch: %#v", status)
	}
	if result.Hub.After == "" {
		t.Fatal("task creation did not return a Hub revision")
	}
}

func TestTaskCorrectionCreateAppendsRevisionWithoutRewritingRootOrDelivery(t *testing.T) {
	s, task, run := makeReviewableRun(t)
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
		{"findings", `[{"id":"F1","severity":"high","title":"Correction required","detail":"A bounded correction is required."}]`},
		{"scope_coverage", `[]`},
		{"unexpected_surfaces", `[]`},
		{"historical_compatibility", `[]`},
		{"prohibited_actions", `[]`},
		{"next_action", `"publish one bounded correction"`},
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
	delivery, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		ExpectedDraftRevision: draft.DraftRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeTask, err := s.Hub.ReadFile(ctx, s.taskPath(task.ProjectID, task.ID))
	if err != nil {
		t.Fatal(err)
	}
	beforeRun, err := s.Hub.ReadFile(ctx, s.runPath(task.ProjectID, run.ID))
	if err != nil {
		t.Fatal(err)
	}
	beforeDelivery, err := s.Hub.ReadFile(ctx, s.reviewReportPath(task.ProjectID, run.ID))
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revision, operation, err := s.TaskCorrectionCreate(ctx, TaskCorrectionCreateInput{
		TaskID:           task.ID,
		SourceRevisionID: task.ID + ".REV1",
		SourceRunID:      run.ID,
		SourceReportID:   delivery.ID,
		Objective:        "Corrected bounded objective.",
		CreatedBy:        "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: current,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.ID != task.ID+".REV2" || revision.ParentTaskRevision != 1 || revision.SourceRunID != run.ID || operation.Status != "revision_created" {
		t.Fatalf("unexpected correction result: %#v %#v", revision, operation)
	}
	if revision.BaseRevision != delivery.ReviewedHead {
		t.Fatalf("correction revision did not pin the reviewed source head: got=%s want=%s", revision.BaseRevision, delivery.ReviewedHead)
	}
	if got, err := s.Hub.ReadFile(ctx, s.taskPath(task.ProjectID, task.ID)); err != nil || string(got) != string(beforeTask) {
		t.Fatalf("root task changed: err=%v", err)
	}
	if got, err := s.Hub.ReadFile(ctx, s.runPath(task.ProjectID, run.ID)); err != nil || string(got) != string(beforeRun) {
		t.Fatalf("source run changed: err=%v", err)
	}
	if got, err := s.Hub.ReadFile(ctx, s.reviewReportPath(task.ProjectID, run.ID)); err != nil || string(got) != string(beforeDelivery) {
		t.Fatalf("Delivery report changed: err=%v", err)
	}
	state, err := s.taskState(ctx, task)
	if err != nil || state.Status != "ready" {
		t.Fatalf("correction did not reopen mutable task state: %#v %v", state, err)
	}
	items, err := s.TaskRevisionList(ctx, task.ID)
	if err != nil || len(items) != 2 || items[1].Objective != "Corrected bounded objective." {
		t.Fatalf("unexpected revision chain: %#v %v", items, err)
	}
	if !strings.Contains(operation.Hub.Paths[0], "/revisions/") {
		t.Fatalf("correction did not publish the revision child: %#v", operation.Hub.Paths)
	}
}

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

func TestTaskCorrectionCreateAcceptsRejectedNeedsCorrectionReport(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	ctx := context.Background()
	draft, err := s.TaskReviewReportStart(ctx, task.ID, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	sections := []struct {
		id      string
		payload string
	}{
		{"outcome", `"rejected_needs_correction"`},
		{"findings", `[{"id":"F1","severity":"high","title":"Correction required","detail":"A bounded correction is required."}]`},
		{"scope_coverage", `[]`},
		{"unexpected_surfaces", `[]`},
		{"historical_compatibility", `[]`},
		{"prohibited_actions", `[]`},
		{"next_action", `"create the bounded correction revision"`},
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
	delivery, _, err := s.TaskReviewReportFinalize(ctx, TaskReviewReportFinalizeInput{
		TaskID:                task.ID,
		RunID:                 run.ID,
		ExpectedDraftRevision: draft.DraftRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	current := installTaskLifecycleState(t, s, task, model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "ready", UpdatedAt: time.Now().UTC()}, delivery.HubCommit)
	revision, operation, err := s.TaskCorrectionCreate(ctx, TaskCorrectionCreateInput{
		TaskID:           task.ID,
		SourceRevisionID: task.ID + ".REV1",
		SourceRunID:      run.ID,
		SourceReportID:   delivery.ID,
		Objective:        "Correct the rejected review finding.",
		CreatedBy:        "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: current,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if revision.ID != task.ID+".REV2" || revision.ParentTaskRevision != 1 || revision.ParentTaskSHA256 != task.SHA256 || revision.SourceRunID != run.ID || revision.SourceReportID != delivery.ID || operation.Status != "revision_created" {
		t.Fatalf("unexpected rejected correction result: %#v %#v", revision, operation)
	}

	current, err = s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskCorrectionCreate(ctx, TaskCorrectionCreateInput{
		TaskID:           task.ID,
		SourceRevisionID: task.ID + ".REV1",
		SourceRunID:      run.ID,
		SourceReportID:   task.ID + "-wrong-report",
		Objective:        "Must reject wrong report binding.",
		CreatedBy:        "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: current,
		},
	}); err == nil {
		t.Fatal("wrong source report unexpectedly created a revision")
	}
}

func TestTaskCorrectionCreateReadyRejectsCleanAcceptedReport(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	ctx := context.Background()
	delivery := finalizeAcceptedDeliveryReview(t, s, task, run)
	current := installTaskLifecycleState(t, s, task, model.TaskState{SchemaVersion: model.SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "ready", UpdatedAt: time.Now().UTC()}, delivery.HubCommit)
	if _, _, err := s.TaskCorrectionCreate(ctx, TaskCorrectionCreateInput{
		TaskID:           task.ID,
		SourceRevisionID: task.ID + ".REV1",
		SourceRunID:      run.ID,
		SourceReportID:   delivery.ID,
		Objective:        "Must reject a clean accepted report from ready state.",
		CreatedBy:        "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: current,
		},
	}); err == nil {
		t.Fatal("clean accepted report unexpectedly created a correction from ready state")
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != current {
		t.Fatalf("rejected ready correction mutated hub: got %s want %s err=%v", got, current, err)
	}
}

func TestTaskCorrectionCreateReadyRejectsWrongSourceBindings(t *testing.T) {
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
		{"next_action", `"create the bounded correction revision"`},
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
	cases := []TaskCorrectionCreateInput{
		{TaskID: task.ID, SourceRevisionID: task.ID + ".REV2", SourceRunID: run.ID, SourceReportID: delivery.ID},
		{TaskID: task.ID, SourceRevisionID: task.ID + ".REV1", SourceRunID: run.ID + "-WRONG", SourceReportID: delivery.ID},
		{TaskID: task.ID, SourceRevisionID: task.ID + ".REV1", SourceRunID: run.ID, SourceReportID: delivery.ID + "-WRONG"},
	}
	for i, in := range cases {
		in.CreatedBy = "delivery"
		in.Objective = "Must reject mismatched source binding."
		in.WriteOptions.ExpectedHubRevision = current
		if _, _, err := s.TaskCorrectionCreate(ctx, in); err == nil {
			t.Fatalf("case %d unexpectedly created a correction", i)
		}
		if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != current {
			t.Fatalf("case %d mutated hub: got %s want %s err=%v", i, got, current, err)
		}
	}
}

func TestCorrectionEligibleReportRejectsCleanAcceptedAndInvalidOutcomes(t *testing.T) {
	cleanAccepted := model.RunReviewReport{Outcome: model.ReviewOutcomeAccepted}
	if correctionEligibleReport(cleanAccepted) {
		t.Fatal("clean accepted report unexpectedly became correction-eligible")
	}
	for _, outcome := range []string{model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive, "unknown"} {
		if correctionEligibleReport(model.RunReviewReport{Outcome: outcome}) {
			t.Fatalf("outcome %q unexpectedly became correction-eligible", outcome)
		}
	}
}
