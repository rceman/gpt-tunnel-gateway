package service

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestClassifyProgressStateMatrix(t *testing.T) {
	now := time.Now().UTC()
	run := model.Run{ID: "run", TaskID: "task", ProjectID: "project", Status: "awaiting_result", CreatedAt: now.Add(-time.Minute)}
	tests := []struct {
		name   string
		e      progressEvidence
		active int
		want   string
	}{
		{name: "idle", e: progressEvidence{
			Status: airelay.SessionStatus{State: "idle", ControllerReachable: true},
		}, want: model.AgentStateIdle},
		{name: "no active running", e: progressEvidence{
			Status: airelay.SessionStatus{State: "running", ControllerReachable: true},
		}, want: model.AgentStateRunning},
		{name: "waiting for input", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "waiting", ControllerReachable: true},
			Tail:      "Which option?",
		}, active: 1, want: model.AgentStateWaitingForInput},
		{name: "completion pending", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "waiting", ControllerReachable: true},
		}, active: 1, want: model.AgentStateCompletionPending},
		{name: "compacted idle takes precedence over waiting", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "waiting", ControllerReachable: true},
			Tail:      "Context compacted\nAcknowledged\nModel: test\nContext window: 90% remaining\nWorkspace: /tmp/project\nStatus: waiting",
			Compaction: compactionObservation{
				Detected: true,
			},
		}, active: 1, want: model.AgentStateCompactedIdle},
		{name: "capacity", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "running", ControllerReachable: true, CapacityWarnings: []string{"model capacity blocked"}},
		}, active: 1, want: model.AgentStateCapacityBlocked},
		{name: "rate limited", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "running", ControllerReachable: true, CapacityWarnings: []string{"rate limited"}},
		}, active: 1, want: model.AgentStateRateLimited},
		{name: "compacting marker", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "idle", ControllerReachable: true},
			Compaction: compactionObservation{
				Started: true,
			},
		}, active: 1, want: model.AgentStateCompacting},
		{name: "compacted idle", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "idle", ControllerReachable: true},
			Compaction: compactionObservation{
				Detected: true,
			},
		}, active: 1, want: model.AgentStateCompactedIdle},
		{name: "compacted resuming", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "idle", ControllerReachable: true},
			Compaction: compactionObservation{
				Detected: true,
			},
			Events: []model.RunOperationalEvent{{EventType: model.EventResumeSent, OccurredAt: now}},
		}, active: 1, want: model.AgentStateCompactedResuming},
		{name: "stalled after compaction", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "idle", ControllerReachable: true},
			Compaction: compactionObservation{
				Detected: true,
			},
			Events: []model.RunOperationalEvent{{EventType: model.EventResumeSent, OccurredAt: now.Add(-resumeObservationWindow - time.Second)}},
		}, active: 1, want: model.AgentStateStalled},
		{name: "finalization pending", e: progressEvidence{
			ActiveRun:  &run,
			Completion: true,
			Status:     airelay.SessionStatus{State: "idle", ControllerReachable: true},
		}, active: 1, want: model.AgentStateFinalizationPending},
		{name: "error", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "error", ControllerReachable: false},
		}, active: 1, want: model.AgentStateError},
		{name: "unknown", e: progressEvidence{
			ActiveRun: &run,
			Status:    airelay.SessionStatus{State: "", ControllerReachable: true},
		}, active: 1, want: model.AgentStateUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _, _ := classifyProgress(test.e, test.active, now)
			if got != test.want {
				t.Fatalf("state=%q want %q", got, test.want)
			}
		})
	}
}

func TestWarningKindDistinguishesRemainingQuota(t *testing.T) {
	if got := warningKind([]string{"less than 25% of your weekly limit left"}); got != "" {
		t.Fatalf("remaining quota was treated as a blocker: %q", got)
	}
	if got := warningKind([]string{"weekly limit exhausted"}); got != model.AgentStateCapacityBlocked {
		t.Fatalf("explicit quota exhaustion was not treated as a blocker: %q", got)
	}
}

func TestWorktreeHasConflictRecognizesPorcelainV2AndV1(t *testing.T) {
	for _, status := range []string{"u UU 1 2 3 4 5 6 7 8 file.txt", "UU file.txt", "AA file.txt", "DD file.txt"} {
		if !worktreeHasConflict(status) {
			t.Fatalf("conflict status was not recognized: %q", status)
		}
	}
	for _, status := range []string{"# branch.head feature/x", "1 .M N... 100644 100644 100644 a b file.txt", ""} {
		if worktreeHasConflict(status) {
			t.Fatalf("clean status was treated as conflict: %q", status)
		}
	}
}
