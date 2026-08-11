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
	"github.com/rceman/gpt-tunnel-gateway/internal/watcher"
)

func watcherScript(t *testing.T, output string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airelay")
	body := "#!/bin/sh\nprintf '%b' '" + strings.ReplaceAll(strings.ReplaceAll(output, "'", "'\\''"), "\n", "\\n") + "'\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWatcherObserveSuppressesRepeatedAndVolatileTailLines(t *testing.T) {
	s, task, run, _ := dispatchedRun(t, "feature/watcher-observation")
	s.Airelay.Command = watcherScript(t, "Working (run-123)\nWaiting for background terminal (job-7)\ncheckpoint one\n")
	ctx := context.Background()

	first, err := s.WatcherObserve(ctx, WatcherObserveInput{
		ProjectID: "example",
		Lines:     100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Useful || first.TaskID != task.ID || first.RunID != run.ID || first.TargetSession != run.SessionKey {
		t.Fatalf("unexpected first watcher observation: %#v", first)
	}
	if strings.Contains(first.Tail, "run-123") || !strings.Contains(first.Tail, "Working (...)\nWaiting for background terminal (...)\ncheckpoint one") {
		t.Fatalf("volatile watcher output was not normalized: %q", first.Tail)
	}

	repeat, err := s.WatcherObserve(ctx, WatcherObserveInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Useful || repeat.Tail != "" || len(repeat.NewDigests) != 0 {
		t.Fatalf("repeated watcher tail was not suppressed: %#v", repeat)
	}

	if err := os.WriteFile(s.Airelay.Command, []byte("#!/bin/sh\nprintf '%b' 'Working (new-run)\\ncheckpoint one\\ncheckpoint two\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	next, err := s.WatcherObserve(ctx, WatcherObserveInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if !next.Useful || next.Tail != "checkpoint two" {
		t.Fatalf("new watcher line was not isolated: %#v", next)
	}
	if _, err := os.Stat(watcher.ObservationPath(s.Config.StateDir, "example")); err != nil {
		t.Fatalf("watcher state was not persisted: %v", err)
	}
}

func TestWatcherObserveResetsOnTaskRunIdentityChangeAndTerminalIsAuthoritative(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/watcher-reset")
	s.Airelay.Command = watcherScript(t, "active\n")
	ctx := context.Background()
	if _, err := s.WatcherObserve(ctx, WatcherObserveInput{ProjectID: "example"}); err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	run.Status = "succeeded"
	run.FinishedAt = &finished
	plan, err := s.PlanRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if err := updateWatcherRunOnly(t, s, run); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.WatcherObserve(ctx, WatcherObserveInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if !terminal.Terminal || terminal.RunID != run.ID || terminal.RunStatus != "succeeded" {
		t.Fatalf("terminal watcher run was not authoritative: %#v", terminal)
	}
	plan.ActiveRunID = ""
	plan.UpdatedAt = finished
	plan.UpdatedBy = "test"
	if err := updateWatcherPlanAndRun(t, s, plan, run); err != nil {
		t.Fatal(err)
	}
	empty, err := s.WatcherObserve(ctx, WatcherObserveInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if !empty.IdentityChanged || empty.Terminal || empty.RunID != "" {
		t.Fatalf("unexpected no-active-run identity observation: %#v", empty)
	}
}

func updateWatcherRunOnly(t *testing.T, s *Service, run model.Run) error {
	t.Helper()
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		return err
	}
	path := s.runPath(run.ProjectID, run.ID)
	_, err = s.Hub.Transact(context.Background(), revision, "test: finish watcher run", func(worktree string) ([]string, error) {
		return []string{path}, hub.WriteJSON(worktree, path, run)
	})
	return err
}

func updateWatcherPlanAndRun(t *testing.T, s *Service, plan model.Plan, run model.Run) error {
	t.Helper()
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		return err
	}
	planPath, runPath := s.planPath(plan.ProjectID), s.runPath(run.ProjectID, run.ID)
	_, err = s.Hub.Transact(context.Background(), revision, "test: finish watcher run and clear pointer", func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, planPath, plan); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, runPath, run); err != nil {
			return nil, err
		}
		return []string{planPath, runPath}, nil
	})
	return err
}
