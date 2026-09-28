package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK673ResetTaskProjectsPlannedInTrackAndMilestone(t *testing.T) {
	ctx := context.Background()
	planner := authority.WithPlanner(ctx)
	s, db := tsk585Setup(t)
	defer db.Close()
	task := tsk585Task(t, s, "tsk673-track-reset-projection", "Reset projection")
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: task.ProjectID,
		Title:     "Reset projection milestone",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk673-milestone-create")
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: task.ProjectID,
		Milestone: milestone.ID,
		Title:     "Reset projection Track",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk673-track-create")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
	})
	if err != nil || initial.Status != model.TaskExecutionDispatched {
		t.Fatalf("initial execution=%#v err=%v", initial, err)
	}

	blocked, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "awaiting Planner reset",
	})
	if err != nil || blocked.Status != model.TaskExecutionBlocked {
		t.Fatalf("blocked execution=%#v err=%v", blocked, err)
	}
	reset, err := s.TaskExecutionReset(planner, TaskExecutionResetInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
		Reason:    "retire stale execution",
	})
	if err != nil || reset.Status != model.TaskExecutionPlanned {
		t.Fatalf("reset=%#v err=%v", reset, err)
	}
	updatedSummary := "Updated after reset"
	if _, _, err := s.TaskLifecycleUpdate(planner, TaskAuthoringUpdateInput{
		ProjectID:              task.ProjectID,
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		Summary:                &updatedSummary,
		UpdatedBy:              "planner",
		Reason:                 "revise after reset",
	}); err != nil {
		t.Fatal(err)
	}
	taskRead, err := s.TaskLifecycleRead(ctx, task.ProjectID, task.ID, 0)
	if err != nil || taskRead.Status != model.TaskAuthoringPlanned {
		t.Fatalf("Task read after reset/update=%#v err=%v", taskRead, err)
	}

	taskStatus, err := s.TaskExecutionStatus(ctx, task.ProjectID, task.ID)
	if err != nil || taskStatus.Status != model.TaskExecutionPlanned {
		t.Fatalf("Task execution status after reset/update=%#v err=%v", taskStatus, err)
	}
	trackView, err := s.TrackLifecycleView(ctx, task.ProjectID, track.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trackView.Tasks) != 1 || trackView.Tasks[0].Status != model.TaskExecutionPlanned {
		t.Fatalf("Track did not project the reset Task as planned: %#v", trackView.Tasks)
	}
	if trackView.Tasks[0].Execution.Status != model.TaskExecutionAbandoned {
		t.Fatalf("Track lost the prior abandoned execution history: %#v", trackView.Tasks[0].Execution)
	}
	if trackView.Track.Status == model.TrackReady {
		t.Fatalf("abandoned execution incorrectly made Track ready: %#v", trackView.Track)
	}
	milestoneView, err := s.MilestoneLifecycleView(ctx, task.ProjectID, milestone.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(milestoneView.Tasks) != 1 || milestoneView.Tasks[0].Status != model.TaskExecutionPlanned {
		t.Fatalf("Milestone did not project the reset Task as planned: %#v", milestoneView.Tasks)
	}
	retired, found, err := db.ReadTaskExecutionState(ctx, task.ProjectID, task.ID)
	if err != nil || !found || retired.Status != model.TaskExecutionAbandoned || retired.ExecutionRevision != reset.ExecutionRevision {
		t.Fatalf("prior execution was not preserved as abandoned: %#v found=%v err=%v", retired, found, err)
	}

	redispatched, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: task.ProjectID,
		Key:       task.ID,
	})
	if err != nil || redispatched.Status != model.TaskExecutionDispatched || redispatched.ExecutionRevision != reset.ExecutionRevision+1 {
		t.Fatalf("redispatched execution=%#v err=%v", redispatched, err)
	}
	trackView, err = s.TrackLifecycleView(ctx, task.ProjectID, track.ID, 0)
	if err != nil || len(trackView.Tasks) != 1 || trackView.Tasks[0].Status != model.TaskExecutionDispatched || trackView.Tasks[0].Execution.Status != model.TaskExecutionDispatched {
		t.Fatalf("Track did not project the new execution: %#v err=%v", trackView.Tasks, err)
	}
}

func TestTSK673UnprovenAbandonedExecutionDoesNotProjectPlanned(t *testing.T) {
	s, _, projectHead := testServiceSerial(t)
	testServiceWithDurability(t, s)
	task := seedTrackTask(t, s, "EXM-TSK673", "Unproven abandoned execution", model.TaskPriorityP1)
	ctx := context.Background()
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: task.ProjectID,
		Title:     "Unproven reset milestone",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk673-unproven-milestone-create")
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: task.ProjectID,
		Milestone: milestone.ID,
		Title:     "Unproven reset Track",
		Tasks:     []string{task.ID},
		CreatedBy: "planner",
	}, "tsk673-unproven-track-create")
	if err != nil {
		t.Fatal(err)
	}
	seedTrackExecution(t, s, task, projectHead, model.TaskExecutionAbandoned)
	if _, err := s.TrackLifecycleView(ctx, task.ProjectID, track.ID, 0); err == nil {
		t.Fatal("Track projected an abandoned execution as a valid planned reset without reset evidence")
	}
	if _, err := s.MilestoneLifecycleView(ctx, task.ProjectID, milestone.ID, 0); err == nil {
		t.Fatal("Milestone projected an abandoned execution as a valid planned reset without reset evidence")
	}
}
