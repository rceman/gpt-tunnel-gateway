package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/airelay"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func writeLivenessScript(t *testing.T, s *Service, tail string, status string, log string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n"
	body += "session-status) printf '%s\\n' '" + strings.ReplaceAll(status, "'", "'\\''") + "' ;;\n"
	body += "tail) printf '%s\\n' '" + strings.ReplaceAll(tail, "'", "'\\''") + "' ;;\n"
	body += "prompt)"
	if log != "" {
		body += " printf '%s\\n' \"$@\" >> '" + filepath.Join(dir, "calls") + "'"
	}
	body += " printf 'sent\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
}

func TestProjectStatusAggregatesProgressWithoutSessionIdentity(t *testing.T) {
	s, _, _ := testService(t)
	writeLivenessScript(t, s, "Idle prompt ready", "Controller: reachable\nState: error", "")
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Progress.AgentState != "idle" || status.Progress.Tail != "Idle prompt ready\n" || status.Progress.RecommendedNextAction != "await_authorized_task" {
		t.Fatalf("unexpected progress: %#v", status.Progress)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session_key") || strings.Contains(string(data), "airelay_session_key") {
		t.Fatalf("project status exposed session identity: %s", data)
	}
}

func TestProjectStatusReturnsSafePartialComponentErrors(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: unreachable\\nState: error\\n'; exit 1;;\ntail) printf 'tail\\n'; exit 1;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Progress.AgentState != model.AgentStateError || len(status.Progress.ComponentErrors) != 2 || status.Progress.ComponentErrors[0] != "agent_status_unavailable" || status.Progress.ComponentErrors[1] != "agent_tail_unavailable" {
		t.Fatalf("unexpected partial progress: %#v", status.Progress)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "unreachable") || strings.Contains(string(encoded), dir) {
		t.Fatalf("raw component detail leaked: %s", encoded)
	}
}

func TestRunResumeIsCanonicalAndOneShot(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/compaction-resume")
	writeLivenessScript(t, s, "Context compaction completed\nAcknowledged; resuming", "Controller: reachable\nState: idle", "record")
	result, err := s.RunResume(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Sent || result.State != "compacted_resuming" || result.CompactionEventID == "" || result.MessageDigest == "" {
		t.Fatalf("unexpected resume result: %#v", result)
	}
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.EventType] = true
	}
	for _, eventType := range []string{"compaction_completed", "resume_sent", "resume_completed"} {
		if !seen[eventType] {
			t.Fatalf("missing operational event %q: %#v", eventType, events)
		}
	}
	eventBytes, err := os.ReadFile(s.operationalEventPath(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(eventBytes), "Context recovery") || strings.Contains(string(eventBytes), "session_key") {
		t.Fatalf("operational event log contains unbounded/raw data: %s", eventBytes)
	}
	if _, err := s.RunResume(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), "already sent") {
		t.Fatalf("duplicate resume was accepted: %v", err)
	}
}

func TestRunResumeRejectsFalseCompactionAndQuestions(t *testing.T) {
	for _, fixture := range []struct {
		name string
		tail string
		want string
	}{
		{name: "low context", tail: "Context 12% left\nIdle prompt ready", want: "confirmed resumable compaction"},
		{name: "question", tail: "Context compaction completed\nWhich branch should I use?", want: "confirmed resumable compaction"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s, _, run, _ := dispatchedRun(t, "feature/false-compaction")
			writeLivenessScript(t, s, fixture.tail, "Controller: reachable\nState: idle", "")
			if _, err := s.RunResume(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), fixture.want) {
				t.Fatalf("unexpected resume result: %v", err)
			}
		})
	}
}

func TestTaskPacketContainsDurableCompactionRecovery(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "feature/packet-recovery")
	packet, err := s.TaskRead(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Context-compaction recovery",
		"re-read this immutable task packet",
		"declared branch, base",
		"do not rely on conversation memory",
		"gpt-tunnel run finalize " + run.ID,
	} {
		if !strings.Contains(packet.Text, want) {
			t.Fatalf("packet is missing %q: %s", want, packet.Text)
		}
	}
}

func TestRunSweepUsesOneCanonicalResume(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "feature/sweep-resume")
	writeLivenessScript(t, s, "Context compaction completed\nAcknowledged; resuming", "Controller: reachable\nState: idle", "record")
	old := run
	old.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	dispatched := old.CreatedAt
	old.DispatchedAt = &dispatched
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(context.Background(), revision, "test: age run", func(worktree string) ([]string, error) {
		path := s.runPath(task.ProjectID, run.ID)
		return []string{path}, hub.WriteJSON(worktree, path, old)
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.RunSweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Action != "resume" {
		t.Fatalf("sweep did not use canonical resume: %#v", result)
	}
}

func TestClassifyProgressStateMatrix(t *testing.T) {
	now := time.Now().UTC()
	run := model.Run{ID: "run", TaskID: "task", ProjectID: "project", Status: "awaiting_result", CreatedAt: now.Add(-time.Minute)}
	tests := []struct {
		name   string
		e      progressEvidence
		active int
		want   string
	}{
		{name: "idle", e: progressEvidence{Status: airelay.SessionStatus{State: "idle", ControllerReachable: true}}, want: model.AgentStateIdle},
		{name: "no active running", e: progressEvidence{Status: airelay.SessionStatus{State: "running", ControllerReachable: true}}, want: model.AgentStateRunning},
		{name: "waiting for input", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "waiting", ControllerReachable: true}, Tail: "Which option?"}, active: 1, want: model.AgentStateWaitingForInput},
		{name: "completion pending", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "waiting", ControllerReachable: true}}, active: 1, want: model.AgentStateCompletionPending},
		{name: "capacity", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "running", ControllerReachable: true, CapacityWarnings: []string{"model capacity blocked"}}}, active: 1, want: model.AgentStateCapacityBlocked},
		{name: "rate limited", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "running", ControllerReachable: true, CapacityWarnings: []string{"rate limited"}}}, active: 1, want: model.AgentStateRateLimited},
		{name: "compacting marker", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "idle", ControllerReachable: true}, Compaction: compactionObservation{Started: true}}, active: 1, want: model.AgentStateCompacting},
		{name: "compacted idle", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "idle", ControllerReachable: true}, Compaction: compactionObservation{Detected: true}}, active: 1, want: model.AgentStateCompactedIdle},
		{name: "compacted resuming", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "idle", ControllerReachable: true}, Compaction: compactionObservation{Detected: true}, Events: []model.RunOperationalEvent{{EventType: model.EventResumeSent, OccurredAt: now}}}, active: 1, want: model.AgentStateCompactedResuming},
		{name: "stalled after compaction", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "idle", ControllerReachable: true}, Compaction: compactionObservation{Detected: true}, Events: []model.RunOperationalEvent{{EventType: model.EventResumeSent, OccurredAt: now.Add(-resumeObservationWindow - time.Second)}}}, active: 1, want: model.AgentStateStalled},
		{name: "finalization pending", e: progressEvidence{ActiveRun: &run, Completion: true, Status: airelay.SessionStatus{State: "idle", ControllerReachable: true}}, active: 1, want: model.AgentStateFinalizationPending},
		{name: "error", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "error", ControllerReachable: false}}, active: 1, want: model.AgentStateError},
		{name: "unknown", e: progressEvidence{ActiveRun: &run, Status: airelay.SessionStatus{State: "", ControllerReachable: true}}, active: 1, want: model.AgentStateUnknown},
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
