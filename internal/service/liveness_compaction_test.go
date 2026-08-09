package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
