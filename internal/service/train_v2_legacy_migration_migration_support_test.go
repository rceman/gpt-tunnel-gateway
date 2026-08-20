package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func mustHubRevision(t *testing.T, s *Service) string {
	t.Helper()
	revision, err := s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
func installFetchCounter(t *testing.T) (func(string), func() int) {
	t.Helper()
	dir := t.TempDir()
	countPath := filepath.Join(dir, "fetch-count")
	limitPath := filepath.Join(dir, "fetch-limit")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(dir, "git")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "fetch" ]; then
  count=0
  if [ -f %q ]; then count=$(cat %q); fi
  if [ -f %q ]; then
    limit=$(cat %q)
    if [ "$count" -ge "$limit" ]; then exit 97; fi
  fi
  count=$((count + 1))
  printf '%%s\n' "$count" > %q
fi
exec %q "$@"
`, countPath, countPath, limitPath, limitPath, countPath, gitPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	reset := func(limit string) {
		t.Helper()
		if err := os.WriteFile(countPath, []byte("0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if limit == "" {
			if err := os.Remove(limitPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			return
		}
		if err := os.WriteFile(limitPath, []byte(limit+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() int {
		t.Helper()
		data, err := os.ReadFile(countPath)
		if err != nil {
			t.Fatal(err)
		}
		count, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	reset("")
	return reset, read
}
func TestTrainV2LegacyMigrationSevenActionsReuseOneSnapshot(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := nowUTC()
	trains := make([]model.TrainV2, 0, 7)
	historical, _ := reviewBackfillFixture(t)
	historical.ID = "EXM-TRN301"
	historical.Status = model.TrainV2RecoveryQuarantined
	trains = append(trains, historical)
	stale := staleTrainV2ForRetirementTest(now)
	stale.ID = "EXM-TRN302"
	trains = append(trains, stale)

	operations := make([]trainv2.IntegrationOperation, 0, 5)
	mutations := make([]durableMutationOperation, 0, 5)
	for i := 0; i < 5; i++ {
		train, _ := reviewBackfillFixture(t)
		train.ID = fmt.Sprintf("EXM-TRN30%d", i+3)
		train.Status = model.TrainV2ReadyForIntegration
		trains = append(trains, train)
		operation := trainv2.IntegrationOperation{
			SchemaVersion: 1,
			OperationID:   fmt.Sprintf("GTW-INTEGRATE-%024d", i+1),
			ProjectID:     "example",
			TrainID:       train.ID,
			RequestSHA256: strings.Repeat("a", 64),
			SourceHead:    strings.Repeat("b", 40),
			TargetBranch:  "main",
			TargetBefore:  strings.Repeat("c", 40),
			Phase:         trainv2.IntegrationPhasePrePending,
			UpdatedAt:     now,
		}
		operations = append(operations, operation)
		mutationID := fmt.Sprintf("mutation-legacy-integration-%d", i+1)
		mutationInput := []byte(fmt.Sprintf(`{"project_id":"example","train_id":"%s"}`, train.ID))
		mutations = append(mutations, durableMutationOperation{
			SchemaVersion: durableMutationSchemaVersion,
			OperationID:   mutationID,
			Kind:          "train-v2-integrate",
			RequestSHA256: durableMutationDigest("train-v2-integrate", "", mutationInput),
			ProjectID:     "example",
			Input:         mutationInput,
			Status:        "failed",
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	if tx, err := s.Hub.Transact(context.Background(), revision, "test: seed seven migration actions", func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(trains)+len(operations))
		for _, train := range trains {
			path := s.trainV2Path("example", train.ID)
			if err := hub.WriteJSON(worktree, path, train); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		for _, operation := range operations {
			path := trainV2IntegrationOperationPath("example", operation.TrainID)
			if err := hub.WriteJSON(worktree, path, operation); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		return paths, nil
	}); err != nil {
		t.Fatal(err)
	} else {
		revision = tx.After
	}
	for _, mutation := range mutations {
		if err := s.writeDurableMutation(mutation); err != nil {
			t.Fatal(err)
		}
		mutationID := mutation.OperationID
		t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, mutationID)) })
	}

	actions := []TrainV2LegacyStateMigrationAction{}
	for _, train := range trains {
		trainRaw, err := s.Hub.ReadFile(context.Background(), s.trainV2Path("example", train.ID))
		if err != nil {
			t.Fatal(err)
		}
		action := TrainV2LegacyStateMigrationAction{
			TrainID:     train.ID,
			TrainSHA256: digestBytes(trainRaw),
		}
		switch train.ID {
		case historical.ID:
			action.Action = TrainV2LegacyActionMarkHistorical
		case stale.ID:
			action.Action = TrainV2LegacyActionRetireStale
		default:
			action.Action = TrainV2LegacyActionRecoverIntegrate
			opRaw, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationPath("example", train.ID))
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range operations {
				if operation.TrainID == train.ID {
					action.IntegrationSHA256 = digestBytes(opRaw)
					for _, mutation := range mutations {
						if mutation.OperationID == fmt.Sprintf("mutation-legacy-integration-%d", len(actions)-1) {
							mutationRaw, readErr := os.ReadFile(durableMutationPath(s.Config.StateDir, mutation.OperationID))
							if readErr != nil {
								t.Fatal(readErr)
							}
							action.IntegrationMutationID = mutation.OperationID
							action.IntegrationMutationSHA256 = digestBytes(mutationRaw)
						}
					}
				}
			}
		}
		actions = append(actions, action)
	}

	resetFetches, fetches := installFetchCounter(t)
	resetFetches("1")
	dryCtx, cancel := context.WithTimeout(trainV2RetirementTestContext(), 15*time.Second)
	dry, err := s.TrainV2MigrateLegacyState(dryCtx, TrainV2LegacyStateMigrationInput{
		ProjectID: "example",
		Reason:    "seven-action snapshot regression",
		Actions:   actions,
	})
	cancel()
	if err != nil || !dry.DryRun || len(dry.Records) != 7 {
		t.Fatalf("seven-action dry-run failed: %#v err=%v", dry, err)
	}
	if got := fetches(); got != 1 {
		t.Fatalf("dry-run used %d fetches, want exactly one", got)
	}
	resetFetches("")
	if _, err := s.Hub.ReadFile(context.Background(), s.trainV2Path("example", historical.ID)); err != nil {
		t.Fatal(err)
	}
	if got := fetches(); got != 1 {
		t.Fatalf("snapshot leaked outside request: fetches=%d want one external refresh", got)
	}

	resetFetches("")
	applyCtx, applyCancel := context.WithTimeout(trainV2RetirementTestContext(), 15*time.Second)
	result, err := s.TrainV2MigrateLegacyState(applyCtx, TrainV2LegacyStateMigrationInput{
		ProjectID:           "example",
		Apply:               true,
		ExpectedHubRevision: dry.HubBefore,
		Reason:              "seven-action snapshot regression",
		Actions:             actions,
	})
	applyCancel()
	if err != nil || !result.Applied {
		t.Fatalf("seven-action apply failed: %#v err=%v", result, err)
	}
	if got := fetches(); got != 3 {
		t.Fatalf("apply used %d fetches, want snapshot plus two guarded transaction refreshes", got)
	}
}
