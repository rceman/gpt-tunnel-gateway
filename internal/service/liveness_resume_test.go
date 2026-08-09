package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
