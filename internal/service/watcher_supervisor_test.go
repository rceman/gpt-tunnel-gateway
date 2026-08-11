package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func TestWatcherSupervisorStartStopTickIsIdempotentAndRunIndependent(t *testing.T) {
	s, _, _ := testService(t)
	ctx := context.Background()
	if _, err := s.WatcherStart(ctx, "example"); err == nil {
		t.Fatal("disabled watcher accepted start")
	}
	projectConfig := s.Config.Projects["example"]
	projectConfig.Watcher.Mode = "observe"
	projectConfig.Watcher.CadenceSeconds = 1
	projectConfig.Watcher.TailLines = 100
	s.Config.Projects["example"] = projectConfig
	started, err := s.WatcherStart(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if started.Desired != "running" || started.Runtime != "starting" || started.InstanceID == "" || started.LeaseID != started.InstanceID {
		t.Fatalf("unexpected watcher start state: %#v", started)
	}
	again, err := s.WatcherStart(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if again.InstanceID != started.InstanceID {
		t.Fatal("idempotent watcher start allocated a second instance")
	}
	if err := s.WatcherSupervisorTick(ctx, "example"); err != nil {
		t.Fatal(err)
	}
	running, err := watcher.LoadSupervisor(s.Config.StateDir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if running.Runtime != "running" || running.Desired != "running" {
		t.Fatalf("watcher tick did not reconcile runtime: %#v", running)
	}
	status, err := s.WatcherStatus(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.Desired != "running" || status.Runtime != "running" || status.CadenceSeconds != 1 {
		t.Fatalf("watcher status omitted scheduler state: %#v", status)
	}
	stopped, err := s.WatcherStop(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Desired != "stopped" || stopped.Runtime != "stopped" {
		t.Fatalf("unexpected watcher stop state: %#v", stopped)
	}
	if stopped.ActiveRunID != "" {
		t.Fatalf("watcher stop changed a run pointer: %#v", stopped)
	}
	if err := model.ValidateWatcherSupervisorState(stopped); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherSupervisorPromptsOnlyIdleWatcherAndStillObservesWhileBusy(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "feature/watcher-idle-prompt")
	modePath := filepath.Join(t.TempDir(), "mode")
	outputPath := filepath.Join(t.TempDir(), "output")
	markerPath := filepath.Join(t.TempDir(), "prompt")
	if err := os.WriteFile(modePath, []byte("busy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "airelay")
	body := "#!/bin/sh\ncase \"$1\" in\n tail) printf 'line-'; cat '" + outputPath + "';;\n session-status) printf 'State: '; cat '" + modePath + "';;\n prompt) printf 'prompt\\n' > '" + markerPath + "';;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	projectConfig := s.Config.Projects["example"]
	projectConfig.Watcher.Mode = "observe"
	projectConfig.Watcher.NudgeEnabled = true
	projectConfig.Watcher.CadenceSeconds = 1
	s.Config.Projects["example"] = projectConfig
	if _, err := s.WatcherStart(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	if err := s.WatcherSupervisorTick(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	state, err := watcher.LoadSupervisor(s.Config.StateDir, "example")
	if err != nil {
		t.Fatal(err)
	}
	// A busy watcher must not block observation, but it must suppress its own
	// prompt. LastUsefulAt is set by observation; LastNudgeAt is not.
	if state.LastUsefulAt.IsZero() {
		t.Fatalf("busy watcher did not record useful observation: %#v", state)
	}
	if !state.LastNudgeAt.IsZero() {
		t.Fatalf("busy watcher was prompted: %#v", state)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("busy watcher was prompted: %v", err)
	}
	if err := os.WriteFile(modePath, []byte("idle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.WatcherSupervisorTick(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	state, err = watcher.LoadSupervisor(s.Config.StateDir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if state.LastNudgeAt.IsZero() {
		t.Fatalf("idle watcher was not prompted: %#v", state)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("idle watcher prompt receipt missing: %v", err)
	}
}

func TestWatcherSupervisorSchedulerReconcilesLeaseAndCadence(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/watcher-scheduler")
	s.Airelay.Command = watcherScript(t, "scheduler output\n")
	projectConfig := s.Config.Projects["example"]
	projectConfig.Watcher.Mode = "observe"
	projectConfig.Watcher.CadenceSeconds = 1
	s.Config.Projects["example"] = projectConfig
	base := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	now := base
	s.clock = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	if _, err := s.WatcherStart(context.Background(), "example"); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.runWatcherSupervisors(ctx, ticks, "scheduler-test")
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	ticks <- base
	state := waitForWatcherSupervisorState(t, s, func(value model.WatcherSupervisorState) bool {
		return value.Runtime == "running" && value.LeaseID == "scheduler-test"
	})
	if state.ActiveRunID != run.ID || state.RestartCount != 1 || !state.LastTickAt.Equal(base) {
		t.Fatalf("scheduler did not reconcile the durable lease and active run: %#v", state)
	}
	firstTick := state.LastTickAt
	ticks <- base
	state = waitForWatcherSupervisorState(t, s, func(value model.WatcherSupervisorState) bool {
		return value.LastTickAt.Equal(firstTick)
	})
	if !state.LastTickAt.Equal(firstTick) {
		t.Fatalf("scheduler ignored cadence bound: %#v", state)
	}
	nowMu.Lock()
	now = base.Add(time.Second)
	nowMu.Unlock()
	ticks <- now
	state = waitForWatcherSupervisorState(t, s, func(value model.WatcherSupervisorState) bool {
		return value.LastTickAt.Equal(now)
	})
	if !state.LastTickAt.Equal(now) {
		t.Fatalf("scheduler did not tick after cadence elapsed: %#v", state)
	}
}

func waitForWatcherSupervisorState(t *testing.T, s *Service, predicate func(model.WatcherSupervisorState) bool) model.WatcherSupervisorState {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := watcher.LoadSupervisor(s.Config.StateDir, "example")
		if err == nil && predicate(state) {
			return state
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for watcher supervisor state: %#v (err=%v)", state, err)
		}
	}
}
