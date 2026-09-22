package service

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK660TrackRemovalIgnoresGlobalExecutionWithoutTrackHistory(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	first := tsk585Task(t, s, "tsk660-global-execution", "Global execution")
	second := tsk585Task(t, s, "tsk660-global-execution-peer", "Global execution peer")
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Global execution grouping",
		Tasks:     []string{first.ID, second.ID},
		CreatedBy: "planner",
	}, "tsk660-global-execution-milestone")
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Track scoped removal",
		Tasks:     []string{first.ID, second.ID},
		CreatedBy: "planner",
	}, "tsk660-global-execution-track")
	if err != nil {
		t.Fatal(err)
	}
	head, err := s.Git.RefreshDefaultBranch(ctx, s.Config.Projects["example"])
	if err != nil {
		t.Fatal(err)
	}
	seedTrackExecution(t, s, second, head, model.TaskExecutionIntegrated)
	if err := s.recordTrackTaskDispatch(ctx, "example", second.ID); err != nil {
		t.Fatal(err)
	}
	seedTrackExecution(t, s, first, head, model.TaskExecutionIntegrated)
	stored, err := s.TrackLifecycleRead(ctx, "example", track.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.DispatchedTasks) != 1 || stored.DispatchedTasks[0] != second.ID {
		t.Fatalf("global execution leaked into Track history: %#v", stored.DispatchedTasks)
	}
	updated, _, err := s.TrackLifecycleRemoveTasks(ctx, TrackMembershipInput{
		ProjectID: "example",
		Key:       track.ID,
		Tasks:     []string{first.ID},
		Actor:     "lead",
		Reason:    "remove task never dispatched in this Track",
	})
	if err != nil {
		t.Fatalf("global Task execution incorrectly blocked Track removal: %v", err)
	}
	if containsString(updated.Tasks, first.ID) {
		t.Fatalf("removed Task remained in Track: %#v", updated.Tasks)
	}
}

func TestTSK660TrackDispatchHistoryBlocksSameTrackRemoval(t *testing.T) {
	s, db := tsk585Setup(t)
	defer db.Close()
	ctx := context.Background()
	first := tsk585Task(t, s, "tsk660-track-dispatch", "Track dispatch")
	second := tsk585Task(t, s, "tsk660-track-dispatch-peer", "Track dispatch peer")
	milestone, _, err := s.MilestoneLifecycleCreate(ctx, MilestoneCreateInput{
		ProjectID: "example",
		Title:     "Track dispatch grouping",
		Tasks:     []string{first.ID, second.ID},
		CreatedBy: "planner",
	}, "tsk660-track-dispatch-milestone")
	if err != nil {
		t.Fatal(err)
	}
	track, _, err := s.TrackLifecycleCreate(ctx, TrackCreateInput{
		ProjectID: "example",
		Milestone: milestone.ID,
		Title:     "Track dispatch history",
		Tasks:     []string{first.ID, second.ID},
		CreatedBy: "planner",
	}, "tsk660-track-dispatch-track")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
		ProjectID: "example",
		Key:       first.ID,
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.TrackLifecycleRead(ctx, "example", track.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.DispatchedTasks) != 1 || stored.DispatchedTasks[0] != first.ID {
		t.Fatalf("Track dispatch history=%#v", stored.DispatchedTasks)
	}
	if _, _, err := s.TrackLifecycleRemoveTasks(ctx, TrackMembershipInput{
		ProjectID: "example",
		Key:       track.ID,
		Tasks:     []string{first.ID},
		Actor:     "lead",
		Reason:    "must reject dispatched member",
	}); err == nil || !strings.Contains(err.Error(), "already dispatched") {
		t.Fatalf("same-Track dispatched removal error=%v", err)
	}
	updated, _, err := s.TrackLifecycleRemoveTasks(ctx, TrackMembershipInput{
		ProjectID: "example",
		Key:       track.ID,
		Tasks:     []string{second.ID},
		Actor:     "lead",
		Reason:    "remove undispatched member",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tasks) != 1 || updated.Tasks[0] != first.ID || len(updated.DispatchedTasks) != 1 || updated.DispatchedTasks[0] != first.ID {
		t.Fatalf("Track removal changed dispatch history incorrectly: %#v", updated)
	}
}
