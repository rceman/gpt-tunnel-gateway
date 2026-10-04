package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

// TSK697: task/complete mode=integrated gains a bounded fallback to durable
// Planner-accepted Track evidence when the immutable verification is
// unavailable only because the Task predates the current gate contract (or
// its gate profile changed since the receipt). The fallback proves the exact
// Task revision, the ordinary integrated execution phase, canonical
// integration commit containment inside the accepted Track source, and
// accepted-Track membership. Stale freshness projection on an accepted Track
// does not revoke durable acceptance.

// tsk697LegacyTask seeds a ready Task with the given acceptance criteria.
func tsk697LegacyTask(t *testing.T, s *Service, key string, criteria []string) model.TaskAuthoring {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: key, ProjectID: "example", Revision: 1, Title: key + " title", Summary: key + " summary", Objective: key + " objective", Type: model.TaskTypeTask, Execution: model.TaskExecutionCanonical, Status: model.TaskAuthoringPlanned, AcceptanceCriteria: criteria, Priority: model.TaskPriorityP0, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now}
	var err error
	task.RevisionSHA256, err = model.HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, task.Revision, payload, task.UpdatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return task
}

// tsk697IntegratedFixture seeds the delivered-legacy shape: an integrated
// execution bound to the current Task revision plus exactly one ordinary
// integration phase carrying head — and no verification receipt.
func tsk697IntegratedFixture(t *testing.T, s *Service, task model.TaskAuthoring, head string) {
	t.Helper()
	ctx := context.Background()
	state := model.TaskExecutionState{TaskID: task.ID, ProjectID: task.ProjectID, Status: model.TaskExecutionIntegrated, Stage: "code", Worktree: "WT-" + strings.TrimPrefix(task.ID, "EXM-") + "-" + strings.ToLower(head[:8]), BaseHead: head, Head: head, Branch: "task/" + task.ID + "-lane", TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256, Agent: "agent", ExecutionRevision: 2, UpdatedAt: time.Now().UTC()}
	if err := s.Durability.CreateTaskExecutionState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.AppendTaskExecutionPhase(ctx, sqlitestore.TaskExecutionPhase{
		TaskID: task.ID, ProjectID: task.ProjectID, ExecutionRevision: state.ExecutionRevision,
		Stage: "integration", Status: model.TaskExecutionIntegrated, Head: head, Branch: state.Branch,
		TaskRevisionSHA256: task.RevisionSHA256, EventKind: "integration", Decision: "accept",
		Comment: "integrated", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// tsk697AcceptedTrack creates a Milestone plus a Track containing the
// given member Tasks, submits it and Planner-accepts it.
func tsk697AcceptedTrack(t *testing.T, s *Service, key string, taskIDs []string) model.Track {
	t.Helper()
	ctx := context.Background()
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     key + " Milestone",
		Tasks:     taskIDs,
		CreatedBy: "planner",
	}, "mil-"+key)
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     key + " Track",
		Tasks:     taskIDs,
		CreatedBy: "planner",
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := s.TrackLifecycleSubmit(ctx, "example", track.ID, "lead")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.TrackLifecycleAccept(ctx, "example", submitted.Track.ID, "planner")
	if err != nil {
		t.Fatal(err)
	}
	return accepted.Track
}

func tsk697ReviewJournal(t *testing.T, s *Service, taskID string) model.OperatorJournalEvent {
	t.Helper()
	sessionID := tsk585PlannerSession(t, s)
	return tsk566SeedOperatorEvent(t, s, operatorEvidenceSeed{
		projectID:  "example",
		sessionID:  &sessionID,
		kind:       model.OperatorTaskReview,
		summary:    "final Task review",
		content:    model.OperatorJournalContent{Facts: []string{"all acceptance criteria are satisfied"}},
		references: model.OperatorJournalReferences{Tasks: []string{taskID}},
		actor:      "owner",
	})
}

func tsk697Complete(s *Service, task model.TaskAuthoring, reviewID string) (TaskCompleteOutput, error) {
	return s.TaskComplete(context.Background(), tsk585CompletionInput(task, "integrated", "all criteria accepted", reviewID), "planner")
}

func tsk697StoredTrackStatus(t *testing.T, s *Service, track model.Track, status string) {
	t.Helper()
	stored, err := s.trackReadStored(context.Background(), "example", track.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stored.Status = status
	if status == model.TrackCancelled {
		stored.CancelledAt = &now
		stored.CancelledBy = "planner"
		stored.CancelledReason = "withdrawn"
	}
	stored.UpdatedAt = now
	payload, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Durability.Shared.Exec(context.Background(), `UPDATE shared_tracks SET payload=? WHERE id=?`, payload, track.ID); err != nil {
		t.Fatal(err)
	}
}

// TestTSK697IntegratedCompleteAcceptedTrackFallback proves a delivered legacy
// Task with no verification receipt completes through accepted-Track proof.
func TestTSK697IntegratedCompleteAcceptedTrackFallback(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	db := testServiceWithDurability(t, s)
	ctx := context.Background()
	task := tsk697LegacyTask(t, s, "EXM-TSK700", []string{"criterion one"})
	tsk697IntegratedFixture(t, s, task, projectHead)
	track := tsk697AcceptedTrack(t, s, "EXM-TRK700", []string{task.ID})
	review := tsk697ReviewJournal(t, s, task.ID)
	out, err := tsk697Complete(s, task, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != model.TaskAuthoringDone {
		t.Fatalf("status=%q", out.Status)
	}
	event, found, err := db.ReadTaskCompletionEvent(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("completion event found=%v err=%v", found, err)
	}
	var contract taskCompletionContract
	if err := decodeStrict(event.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.AcceptedTrack != track.ID || contract.VerificationOperationID != "" || contract.VerificationAttemptRevision != 0 || contract.IntegrationHead != projectHead {
		t.Fatalf("contract=%#v", contract)
	}
	// Idempotent replay through the recorded contract resolves cleanly.
	out, err = tsk697Complete(s, task, review.ID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if out.Status != model.TaskAuthoringDone {
		t.Fatalf("replay status=%q", out.Status)
	}
}

// TestTSK697IntegratedCompleteAcceptedThenStale proves durable acceptance
// stays valid when later canonical source movement projects the Track stale:
// a second member drifts after acceptance while the Task's own pin and the
// integration commit containment remain exact.
func TestTSK697IntegratedCompleteAcceptedThenStale(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	task := tsk697LegacyTask(t, s, "EXM-TSK701", []string{"criterion one"})
	tsk697IntegratedFixture(t, s, task, projectHead)
	other := tsk697LegacyTask(t, s, "EXM-TSK702", []string{"criterion"})
	tsk697IntegratedFixture(t, s, other, projectHead)
	track := tsk697AcceptedTrack(t, s, "EXM-TRK701", []string{task.ID, other.ID})
	// Drift the second member after acceptance — the Track's accepted review
	// snapshot is no longer fresh, so the derived projection is stale while
	// the stored status stays accepted.
	tsk696BumpTaskRevision(t, s, other.ID)
	stored, err := s.trackReadStored(ctx, "example", track.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.TrackAccepted {
		t.Fatalf("stored status=%q", stored.Status)
	}
	projected, err := s.deriveTrackStatus(ctx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if projected != model.TrackStale {
		t.Fatalf("projected status=%q", projected)
	}
	review := tsk697ReviewJournal(t, s, task.ID)
	out, err := tsk697Complete(s, task, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != model.TaskAuthoringDone {
		t.Fatalf("status=%q", out.Status)
	}
}

// TestTSK697IntegratedCompleteFailClosed covers the rejection battery:
// review_pending, cancelled-without-acceptance, unrelated Track, mismatched
// Task revision, noncanonical integration commit, and a non-eligible
// verification rejection (existing but failed receipt) all stay closed.
func TestTSK697IntegratedCompleteFailClosed(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, key string) (*Service, model.TaskAuthoring, model.Track, string) {
		s, _, projectHead := testServiceSerial(t)
		testServiceWithDurability(t, s)
		task := tsk697LegacyTask(t, s, key, []string{"criterion one"})
		tsk697IntegratedFixture(t, s, task, projectHead)
		track := tsk697AcceptedTrack(t, s, strings.Replace(key, "TSK", "TRK", 1), []string{task.ID})
		review := tsk697ReviewJournal(t, s, task.ID)
		return s, task, track, review.ID
	}
	expectClosed := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("completion was accepted")
		}
	}

	t.Run("ReviewPendingTrackRejected", func(t *testing.T) {
		s, task, track, review := setup(t, "EXM-TSK710")
		tsk697StoredTrackStatus(t, s, track, model.TrackReviewPending)
		expectClosed(t, func() error { _, err := tsk697Complete(s, task, review); return err }())
	})

	t.Run("CancelledWithoutAcceptanceRejected", func(t *testing.T) {
		s, task, track, review := setup(t, "EXM-TSK711")
		tsk697StoredTrackStatus(t, s, track, model.TrackCancelled)
		expectClosed(t, func() error { _, err := tsk697Complete(s, task, review); return err }())
	})

	t.Run("UnrelatedTrackRejected", func(t *testing.T) {
		s, task, track, review := setup(t, "EXM-TSK712")
		// The Track review pins the Task but Track membership itself does not
		// contain it — membership in the accepted Track is required.
		stored, err := s.trackReadStored(ctx, "example", track.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		stored.Tasks = []string{"EXM-TSK999"}
		stored.UpdatedAt = time.Now().UTC()
		payload, _ := json.Marshal(stored)
		if _, err := s.Durability.Shared.Exec(ctx, `UPDATE shared_tracks SET payload=? WHERE id=?`, payload, track.ID); err != nil {
			t.Fatal(err)
		}
		expectClosed(t, func() error { _, err := tsk697Complete(s, task, review); return err }())
	})

	t.Run("MismatchedTaskRevisionRejected", func(t *testing.T) {
		s, task, _, review := setup(t, "EXM-TSK713")
		// Bump the Task to revision 2 and rebind the execution + phase to the
		// new revision; the accepted Track review still pins revision 1.
		tsk696BumpTaskRevision(t, s, task.ID)
		row, err := s.Durability.ReadSharedTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		var bumped model.TaskAuthoring
		if err := json.Unmarshal(row.Payload, &bumped); err != nil {
			t.Fatal(err)
		}
		state, found, err := s.Durability.ReadTaskExecutionState(ctx, "example", task.ID)
		if err != nil || !found {
			t.Fatalf("state found=%v err=%v", found, err)
		}
		prev := state.ExecutionRevision
		state.TaskRevision = bumped.Revision
		state.TaskRevisionSHA256 = bumped.RevisionSHA256
		state.UpdatedAt = time.Now().UTC()
		if err := s.Durability.UpdateTaskExecutionState(ctx, state, prev); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Durability.Local.Exec(ctx, `UPDATE local_task_execution_phases SET task_revision_sha256=? WHERE task_id=?`, bumped.RevisionSHA256, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := tsk697Complete(s, bumped, review); err == nil || !strings.Contains(err.Error(), "pin the current Task revision") {
			t.Fatalf("mismatched revision accepted or wrong error: %v", err)
		}
	})

	t.Run("NoncanonicalIntegrationRejected", func(t *testing.T) {
		s, task, _, review := setup(t, "EXM-TSK714")
		// Point the integration phase at a commit that is not contained in
		// the accepted Track source snapshot — a real commit on a side branch.
		root := s.Config.Projects["example"].Root
		testutil.Git(t, root, "checkout", "-b", "tsk697-side")
		testutil.Git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "side")
		side := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))
		testutil.Git(t, root, "checkout", "main")
		if _, err := s.Durability.Local.Exec(ctx, `UPDATE local_task_execution_phases SET head_sha=? WHERE task_id=?`, side, task.ID); err != nil {
			t.Fatal(err)
		}
		expectClosed(t, func() error { _, err := tsk697Complete(s, task, review); return err }())
	})

	t.Run("FailedReceiptStaysClosed", func(t *testing.T) {
		s, task, _, review := setup(t, "EXM-TSK715")
		// A verification receipt exists but failed — not a
		// historical-freshness case, so the fallback must not rescue it even
		// with an accepted Track present.
		state, stateFound, stateErr := s.Durability.ReadTaskExecutionState(ctx, "example", task.ID)
		if stateErr != nil || !stateFound {
			t.Fatalf("state found=%v err=%v", stateFound, stateErr)
		}
		receipt := model.TaskExecutionVerification{ProjectID: "example", TaskID: task.ID, OperationID: "op-failed", AttemptRevision: 1, TaskRevision: task.Revision, TaskRevisionSHA256: task.RevisionSHA256, BaseHead: state.BaseHead, CandidateHead: state.Head, CandidateTree: state.Head, Branch: state.Branch, GateProfileSHA256: task.RevisionSHA256, CodeReviewID: 1, TestsReviewID: 0, Outcome: model.TaskExecutionVerificationFailed, Error: "gate failed", StartedAt: state.UpdatedAt.Add(-time.Second), CompletedAt: time.Now().UTC()}
		receiptJSON, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Durability.Local.Exec(ctx, `INSERT INTO local_task_execution_verifications(project_id,task_id,operation_id,attempt_revision,outcome,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`, "example", task.ID, receipt.OperationID, receipt.AttemptRevision, receipt.Outcome, string(receiptJSON), receipt.CompletedAt.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
		if _, err := tsk697Complete(s, task, review); err == nil || !strings.Contains(err.Error(), "receipt outcome is not succeeded") {
			t.Fatalf("failed receipt accepted or wrong error: %v", err)
		}
	})
}

// TestTSK697ZeroCriteriaViaFallback proves a legacy Task without acceptance
// criteria completes only through the accepted-Track fallback plus a Planner
// completion Journal referencing the Task.
func TestTSK697ZeroCriteriaViaFallback(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	task := tsk697LegacyTask(t, s, "EXM-TSK720", nil)
	tsk697IntegratedFixture(t, s, task, projectHead)
	tsk697AcceptedTrack(t, s, "EXM-TRK720", []string{task.ID})
	review := tsk697ReviewJournal(t, s, task.ID)
	out, err := tsk697Complete(s, task, review.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != model.TaskAuthoringDone {
		t.Fatalf("status=%q", out.Status)
	}

	// A zero-criteria Task whose completion Journal does not reference the
	// Task stays closed.
	task2 := tsk697LegacyTask(t, s, "EXM-TSK721", nil)
	tsk697IntegratedFixture(t, s, task2, projectHead)
	tsk697AcceptedTrack(t, s, "EXM-TRK721", []string{task2.ID})
	sessionID := tsk585PlannerSession(t, s)
	unrelated := tsk566SeedOperatorEvent(t, s, operatorEvidenceSeed{
		projectID:  "example",
		sessionID:  &sessionID,
		kind:       model.OperatorTaskReview,
		summary:    "final Task review",
		content:    model.OperatorJournalContent{Facts: []string{"all acceptance criteria are satisfied"}},
		references: model.OperatorJournalReferences{Tasks: []string{"EXM-TSK999"}},
		actor:      "owner",
	})
	if _, err := tsk697Complete(s, task2, unrelated.ID); err == nil {
		t.Fatal("zero-criteria Task completed without a referencing Planner Journal")
	}

	// A zero-criteria Task with no accepted Track proof stays closed.
	task3 := tsk697LegacyTask(t, s, "EXM-TSK722", nil)
	tsk697IntegratedFixture(t, s, task3, projectHead)
	review3 := tsk697ReviewJournal(t, s, task3.ID)
	if _, err := tsk697Complete(s, task3, review3.ID); err == nil {
		t.Fatal("zero-criteria Task completed without accepted-Track proof")
	}
}

// TestTSK697MilestoneCompletesAfterFallback proves milestone/complete works
// once the fallback finalizes the delivered Tasks (the TSK696+TSK697 chain).
func TestTSK697MilestoneCompletesAfterFallback(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	ctx := context.Background()
	task := tsk697LegacyTask(t, s, "EXM-TSK730", []string{"criterion one"})
	tsk697IntegratedFixture(t, s, task, projectHead)
	tsk697AcceptedTrack(t, s, "EXM-TRK730", []string{task.ID})
	// The Track's Milestone is the one created by the helper.
	page, err := s.Durability.QuerySharedLifecycle(ctx, sqlitestore.SharedLifecycleQuery{EntityType: "milestone", ProjectID: "example", Limit: sqlitestore.SharedLifecycleQueryMaxRows})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entities) != 1 {
		t.Fatalf("milestones=%d", len(page.Entities))
	}
	milestoneID := page.Entities[0].ID
	if _, err := s.MilestoneLifecycleActivate(ctx, "example", milestoneID, "planner"); err != nil {
		t.Fatal(err)
	}
	review := tsk697ReviewJournal(t, s, task.ID)
	if _, err := tsk697Complete(s, task, review.ID); err != nil {
		t.Fatal(err)
	}
	completed, err := s.MilestoneLifecycleComplete(ctx, MilestoneCompletionInput{
		ProjectID: "example",
		Key:       milestoneID,
		Evidence:  "all delivered Tasks finalized",
		Actor:     "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.MilestoneCompleted {
		t.Fatalf("milestone status=%q", completed.Status)
	}
}
