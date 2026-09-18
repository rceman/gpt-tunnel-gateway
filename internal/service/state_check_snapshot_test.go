package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestStateCheckWithoutDurabilityUsesLocalConfigurationWithoutHub(t *testing.T) {
	s, _, _ := testServiceSerial(t)
	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "git.log")
	gitWrapper := filepath.Join(binDir, "git")
	const wrapper = `#!/bin/sh
if [ "$1" = "fetch" ]; then
  printf '%s\n' "$*" >> "__STATE_CHECK_GIT_LOG__"
fi
exec /usr/bin/git "$@"
`
	if err := os.WriteFile(gitWrapper, []byte(strings.ReplaceAll(wrapper, "__STATE_CHECK_GIT_LOG__", logPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Setenv("PATH", oldPath)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := s.StateCheck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("StateCheck invalid: %#v", result.Issues)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("StateCheck touched Hub git fetch path: stat=%v", err)
	}
}

func TestStateCheckUsesLocalSQLiteWhenHubUnavailableAndLocked(t *testing.T) {
	s, _, _ := testService(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	configuration := model.DefaultProjectConfiguration("example", time.Now().UTC())
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload, UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedSharedRulesFromConfiguration(context.Background(), configuration, "EXM"); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	hubLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "hub-repository")
	if err != nil {
		t.Fatal(err)
	}
	defer hubLock.Release()

	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || len(result.Issues) != 0 {
		t.Fatalf("local StateCheck failed with Hub unavailable/locked: %#v", result)
	}
}
