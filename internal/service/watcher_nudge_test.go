package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestWatcherNudgeUsesExactActiveRunSessionAndRejectsTerminalRun(t *testing.T) {
	s, _, run, _ := dispatchedRun(t, "feature/watcher-nudge")
	projectConfig := s.Config.Projects["example"]
	projectConfig.Watcher.NudgeEnabled = true
	s.Config.Projects["example"] = projectConfig
	argsPath := filepath.Join(t.TempDir(), "args")
	script := filepath.Join(t.TempDir(), "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsPath + "'\nprintf 'delivered\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	receipt, err := s.WatcherNudge(context.Background(), WatcherNudgeInput{
		ProjectID: "example",
		Text:      "continue from checkpoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Delivered || receipt.RunID != run.ID || receipt.TaskID != run.TaskID {
		t.Fatalf("unexpected watcher nudge receipt: %#v", receipt)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "prompt\n"+run.SessionKey+"\n") {
		t.Fatalf("nudge did not use exact Run session key: %q", args)
	}

	finished := run
	finished.Status = "succeeded"
	path := s.runPath(run.ProjectID, run.ID)
	revision, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Transact(context.Background(), revision, "test: terminalize watcher nudge run", func(worktree string) ([]string, error) {
		return []string{path}, hub.WriteJSON(worktree, path, finished)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatcherNudge(context.Background(), WatcherNudgeInput{
		ProjectID: "example",
		Text:      "must not send",
	}); err == nil {
		t.Fatal("terminal watcher run accepted a nudge")
	}
	if err := model.ValidateWatcherNudgeReceipt(receipt); err != nil {
		t.Fatal(err)
	}
}
