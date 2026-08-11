package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func TestWatcherEndToEndLifecycleUsesOneGuideAndNoCron(t *testing.T) {
	s, firstTask, firstRun, _ := dispatchedRun(t, "feature/watcher-e2e")
	ctx := context.Background()
	projectConfig := s.Config.Projects["example"]
	projectConfig.Watcher.Mode = "observe"
	projectConfig.Watcher.NudgeEnabled = true
	projectConfig.Watcher.CadenceSeconds = 1
	s.Config.Projects["example"] = projectConfig

	outputPath := filepath.Join(t.TempDir(), "tail")
	callsPath := filepath.Join(t.TempDir(), "prompt-sessions")
	if err := os.WriteFile(outputPath, []byte("checkpoint-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "airelay")
	scriptBody := "#!/bin/sh\ncase \"$1\" in\n tail) cat '" + outputPath + "';;\n session-status) printf 'Controller: reachable\\nState: idle\\n';;\n prompt) printf '%s\\n' \"$2\" >> '" + callsPath + "';;\nesac\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script

	base := time.Date(2026, time.August, 11, 16, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	now := base
	s.clock = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func() {
		nowMu.Lock()
		now = now.Add(time.Second)
		nowMu.Unlock()
	}

	guideResult, err := s.WatcherGuideUpdate(ctx, WatcherGuideUpdateInput{
		ProjectID: "example",
		Guide:     CanonicalWatcherGuide("example", "test", base),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(guideResult.Hub.Paths) != 1 || guideResult.Hub.Paths[0] != s.watcherGuidePath("example") {
		t.Fatalf("guide update did not use exactly one canonical path: %#v", guideResult.Hub.Paths)
	}
	guide, err := s.WatcherGuideRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if guide.Content != CanonicalWatcherGuideContent || guide.Revision != 1 {
		t.Fatalf("unexpected canonical watcher guide: %#v", guide)
	}

	started, err := s.WatcherStart(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if started.WatcherAgentID != "watcher-example" || started.WatcherSession != "watcher_master" {
		t.Fatalf("watcher binding was not resolved at start: %#v", started)
	}
	if err := s.WatcherSupervisorTick(ctx, "example"); err != nil {
		t.Fatal(err)
	}
	state, err := watcher.LoadSupervisor(s.Config.StateDir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if state.LastUsefulAt.IsZero() || state.TargetSession != firstRun.SessionKey {
		t.Fatalf("initial watcher observation failed: %#v", state)
	}
	if got := watcherPromptCount(t, callsPath, "watcher_master"); got != 1 {
		t.Fatalf("initial idle watcher prompt count=%d want=1", got)
	}

	if err := s.WatcherSupervisorTick(ctx, "example"); err != nil {
		t.Fatal(err)
	}
	if got := watcherPromptCount(t, callsPath, "watcher_master"); got != 1 {
		t.Fatalf("replayed watcher tail caused another prompt: %d", got)
	}

	if err := os.WriteFile(outputPath, []byte("checkpoint-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	advance()
	if err := s.WatcherSupervisorTick(ctx, "example"); err != nil {
		t.Fatal(err)
	}
	if got := watcherPromptCount(t, callsPath, "watcher_master"); got != 2 {
		t.Fatalf("new watcher evidence prompt count=%d want=2", got)
	}

	s.Config.AgentBindings["watcher-example"] = config.AgentBinding{SessionKey: "watcher_rebound"}
	if err := os.WriteFile(outputPath, []byte("checkpoint-three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	advance()
	if err := s.WatcherSupervisorTick(ctx, "example"); err != nil {
		t.Fatal(err)
	}
	state, err = watcher.LoadSupervisor(s.Config.StateDir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if state.WatcherAgentID != "watcher-example" || state.WatcherSession != "watcher_rebound" {
		t.Fatalf("watcher binding rebind was not adopted: %#v", state)
	}
	if got := watcherPromptCount(t, callsPath, "watcher_rebound"); got != 1 {
		t.Fatalf("rebound watcher prompt count=%d want=1", got)
	}

	advance()
	restartCtx, cancel := context.WithCancel(ctx)
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		s.runWatcherSupervisors(restartCtx, ticks, "watcher-restart")
		close(done)
	}()
	var stopOnce sync.Once
	stopRestart := func() {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stopRestart)
	beforeReplayPrompts := watcherPromptCount(t, callsPath, "watcher_rebound")
	ticks <- now
	state = waitForWatcherSupervisorState(t, s, func(value model.WatcherSupervisorState) bool {
		return value.LeaseID == "watcher-restart" && value.RestartCount == 1
	})
	if state.LastUsefulAt.IsZero() || watcherPromptCount(t, callsPath, "watcher_rebound") != beforeReplayPrompts {
		t.Fatalf("restart replayed the unchanged watcher window: %#v", state)
	}
	stopRestart()

	secondTask, secondRun := createSecondWatcherRun(t, s)
	if err := os.WriteFile(outputPath, []byte("checkpoint-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	advance()
	if err := s.WatcherSupervisorTick(ctx, "example"); err != nil {
		t.Fatal(err)
	}
	observation, err := watcher.LoadObservation(s.Config.StateDir, "example")
	if err != nil {
		t.Fatal(err)
	}
	if observation.TaskID != secondTask.ID || observation.RunID != secondRun.ID || observation.LastTail != "checkpoint-one" {
		t.Fatalf("Task/Run change did not reset unseen state: %#v", observation)
	}

	stopped, err := s.WatcherStop(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Desired != "stopped" || stopped.Runtime != "stopped" {
		t.Fatalf("watcher stop did not become idempotent desired state: %#v", stopped)
	}
	if _, ok := s.Config.AgentBindings["watcher-example"]; !ok {
		t.Fatal("watcher stop removed the Agent binding")
	}
	var preservedRun model.Run
	if err := s.Hub.ReadJSON(ctx, s.runPath("example", secondRun.ID), &preservedRun); err != nil {
		t.Fatal(err)
	}
	if !watcherActiveStatus(preservedRun.Status) {
		t.Fatalf("watcher stop changed target Run state: %#v", preservedRun)
	}

	assertNoCronOrPythonScheduler(t)
	if firstTask.ID == secondTask.ID || firstRun.ID == secondRun.ID {
		t.Fatal("E2E fixture did not create a new Task/Run identity")
	}
}

func createSecondWatcherRun(t *testing.T, s *Service) (model.Task, model.Run) {
	t.Helper()
	ctx := context.Background()
	projectConfig := s.Config.Projects["example"]
	projectConfig.AirelaySessionKey = "example_second_master"
	s.Config.Projects["example"] = projectConfig
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID:          "example",
		Title:              "Watcher second task",
		Objective:          "Exercise watcher Task identity replacement.",
		Slug:               "watcher-second-task",
		AcceptanceCriteria: []string{"identity resets"},
		OperationClass:     "implementation",
		CreatedBy:          "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
		ProjectID:        "example",
		Title:            planString("Watcher second task"),
		Summary:          planString("Watcher second task"),
		CurrentObjective: planString("Exercise watcher Task identity replacement."),
		ActiveTaskID:     planString(task.ID),
		ActiveRunID:      planString(""),
		UpdatedBy:        "test",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(ctx, DispatchInput{
		TaskID: task.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: plan.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task, run
}

func watcherPromptCount(t *testing.T, path, session string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), session+"\n")
}

func assertNoCronOrPythonScheduler(t *testing.T) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	data, err := os.ReadFile(filepath.Join(root, "cmd/gpt-tunnel-gatewayd/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(data))
	for _, forbidden := range []string{"cron", "python", ".py", "airelay-watch"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("gateway daemon contains forbidden scheduler dependency %q", forbidden)
		}
	}
}
