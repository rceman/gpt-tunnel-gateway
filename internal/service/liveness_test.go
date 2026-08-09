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
	if strings.Contains(string(data), "session_key") || strings.Contains(string(data), "airelay_session_key") || strings.Contains(string(data), s.Config.Projects["example"].Root) || strings.Contains(string(data), s.Config.Projects["example"].Mirror) {
		t.Fatalf("project status exposed session identity: %s", data)
	}
}

func TestProjectStatusRedactsInternalPathsFromTail(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/status-redaction")
	root := s.Config.Projects["example"].Root
	writeLivenessScript(t, s, root+"\n"+run.CompletionPath+"\n"+s.Config.StateDir, "Controller: reachable\nState: idle", "")
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{root, run.CompletionPath, s.Config.StateDir} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("routine project status leaked internal path %q: %s", forbidden, data)
		}
	}
}

func TestProjectStatusDelayedComponentsCompleteConcurrently(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\nsession-status) sleep 1; printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) sleep 1; printf 'Idle prompt ready\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	started := time.Now()
	if _, err := s.ProjectStatus(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("bounded concurrent project status took too long: %s", elapsed)
	}
}

func TestProjectStatusUsesStatusOnlyTaskProjection(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, _, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Slug:               "status-only-task",
		Title:              "Status-only task",
		Objective:          "Exercise the bounded project status task projection.",
		AcceptanceCriteria: []string{"bounded"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.taskStatusList(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	var found *TaskRecord
	for i := range items {
		if items[i].Task.ID == task.ID {
			found = &items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("status-only task projection omitted %s", task.ID)
	}
	if found.CurrentRevision != nil || len(found.RunSummaries) != 0 {
		t.Fatalf("status-only projection performed enrichment: %#v", *found)
	}
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range status.Progress.ComponentErrors {
		if component == "tasks_unavailable" {
			t.Fatalf("healthy status-only task projection became unavailable: %#v", status.Progress)
		}
	}
}

func TestProjectStatusStatusOnlyTaskFailureRemainsUnavailable(t *testing.T) {
	s, hubRevision, _ := testService(t)
	task, created, err := s.TaskCreate(context.Background(), TaskCreateInput{
		ProjectID:          "example",
		Slug:               "status-only-failure",
		Title:              "Status-only failure",
		Objective:          "Exercise task component failure signaling.",
		AcceptanceCriteria: []string{"failure"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Hub.Transact(context.Background(), created.Hub.After, "test: corrupt task state", func(worktree string) ([]string, error) {
		path := s.taskStatePath(task.ProjectID, task.ID)
		if err := hub.WriteJSON(worktree, path, map[string]any{
			"schema_version": 1,
			"task_id":        task.ID,
			"task_sha256":    task.SHA256,
			"status":         "not-a-task-state",
			"updated_at":     time.Now().UTC(),
		}); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range status.Progress.ComponentErrors {
		if component == "tasks_unavailable" {
			found = true
			break
		}
	}
	if !found || status.Progress.BlockerClassification != "PROGRESS_COMPONENT_ERROR" {
		t.Fatalf("task failure was not preserved as a component error: %#v", status.Progress)
	}
}

func TestPublicRoutineProjectionsRedactInternalPaths(t *testing.T) {
	s, _, _ := testService(t)
	run := model.Run{SchemaVersion: 1, ID: "run", TaskID: "task", TaskSHA256: strings.Repeat("a", 64), ProjectID: "example", GatewayID: s.Config.GatewayID, SessionKey: "example_master", CompletionPath: filepath.Join(s.Config.StateDir, "runs", "run", "completion.json"), CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(PublicRunView(run))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session_key") || strings.Contains(string(data), "completion_path") || strings.Contains(string(data), s.Config.StateDir) {
		t.Fatalf("routine run projection leaked internal data: %s", data)
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

func TestProjectStatusReturnsPartialHubAndWorktreeErrors(t *testing.T) {
	s, _, _ := testService(t)
	writeLivenessScript(t, s, "Idle prompt ready", "Controller: reachable\nState: idle", "")
	project := s.Config.Projects["example"]
	project.Root = filepath.Join(t.TempDir(), "missing-project")
	s.Config.Projects["example"] = project
	missingState := filepath.Join(t.TempDir(), "missing-hub-state")
	s.Hub.Config.StateDir = missingState
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project_unavailable", "plan_unavailable", "tasks_unavailable", "runs_unavailable", "hub_revision_unavailable", "worktree_unavailable"} {
		found := false
		for _, code := range status.Progress.ComponentErrors {
			if code == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("partial component error %q missing: %#v", want, status.Progress.ComponentErrors)
		}
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "read-only hub unavailable") || strings.Contains(string(encoded), missingState) {
		t.Fatalf("partial status leaked hub failure detail: %s", encoded)
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
	info, err := os.Stat(s.operationalEventPath(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Size() > maxOperationalEventFile {
		t.Fatalf("operational event bounds or permissions invalid: mode=%o size=%d", info.Mode().Perm(), info.Size())
	}
}

func TestRunResumeAllowsWaitingWithoutQuestion(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/waiting-compaction")
	writeLivenessScript(t, s, "Context compacted\nAcknowledged; resuming\nModel: test\nContext window: 90% remaining\nWorkspace: /tmp/project\nState: waiting", "Controller: reachable\nState: waiting", "")
	result, err := s.RunResume(context.Background(), run.ID)
	if err != nil || !result.Sent {
		t.Fatalf("waiting state without explicit question blocked recovery: result=%#v err=%v", result, err)
	}
}

func TestProjectStatusCompactionSurvivesReachableStatusError(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/reachable-status-error")
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: error\\n'; exit 1 ;;\ntail) printf 'Context compacted\\nAcknowledged; resuming\\nModel: test\\nContext window: 90%% remaining\\nWorkspace: /tmp/project\\nStatus: idle\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	status, err := s.ProjectStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Progress.AgentState != model.AgentStateCompactedIdle || status.Progress.LatestRun == nil || status.Progress.LatestRun.ID != run.ID {
		t.Fatalf("reachable status error hid compaction recovery: %#v", status.Progress)
	}
}

func TestRunResumeRejectsStaticFooterAsProgress(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/footer-progress")
	writeLivenessScript(t, s, "Context compacted\nAcknowledged; resuming\nModel: test\nContext window: 90% remaining\nWorkspace: /tmp/project\nStatus: idle", "Controller: reachable\nState: idle", "")
	if _, err := s.RunResume(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	events, err := s.readOperationalEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := latestEvent(events, model.EventMeaningfulOutput, ""); got != nil {
		t.Fatalf("static footer was recorded as meaningful progress: %#v", got)
	}
	if meaningfulTailAfterResume("Acknowledged; resuming\nModel: test\nContext window: 90% remaining\nWorkspace: /tmp/project\nStatus: idle") {
		t.Fatal("static footer was classified as meaningful progress")
	}
	if !meaningfulTailAfterResume("Acknowledged; resuming\nImplemented the requested correction") {
		t.Fatal("real work was not classified as meaningful progress")
	}
}

func TestRunResumeRejectsDuplicateAfterServiceRestart(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/restart-resume")
	writeLivenessScript(t, s, "Context compaction completed\nAcknowledged; resuming", "Controller: reachable\nState: idle", "")
	if _, err := s.RunResume(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.Config)
	restarted.Airelay.Command = s.Airelay.Command
	if _, err := restarted.RunResume(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), "already sent") {
		t.Fatalf("restart did not preserve resume reservation: %v", err)
	}
}

func TestDirectSendAndResumeShareSessionLock(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/session-lock")
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	calls := filepath.Join(dir, "calls")
	body := "#!/bin/sh\ncase \"$1\" in\nsession-status) printf 'Controller: reachable\\nState: idle\\n' ;;\ntail) printf 'Context compaction completed\\nAcknowledged; resuming\\n' ;;\nprompt) printf 'prompt-start\\n' >> '" + calls + "'; sleep 1; printf 'sent\\n' ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	done := make(chan error, 1)
	go func() {
		_, err := s.AgentSend(context.Background(), "example", "bounded emergency check")
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(calls)
		if strings.Contains(string(data), "prompt-start") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(calls)
	if !strings.Contains(string(data), "prompt-start") {
		t.Fatal("direct send did not enter the shared session write")
	}
	if _, err := s.RunResume(context.Background(), run.ID); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("resume raced direct send: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(s.Config.StateDir, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "example_master") {
			t.Fatalf("raw session identity appeared in lock path: %s", entry.Name())
		}
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

func TestRunSweepRecoversCompactionBeforeTimeout(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/pre-timeout-resume")
	writeLivenessScript(t, s, "Context compacted\nAcknowledged; resuming\nModel: test\nState: idle", "Controller: reachable\nState: waiting", "record")
	result, err := s.RunSweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].RunID != run.ID || result.Items[0].Action != "resume" {
		t.Fatalf("pre-timeout sweep did not recover compaction: %#v", result)
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
