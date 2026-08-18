package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func TestTrainV2LegacyMigrationMarksHistoricalWithExactDigestAndIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := reviewBackfillFixture(t)
	train.ID = "EXM-TRN1"
	train.Status = model.TrainV2RecoveryQuarantined
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed historical Train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	path := s.trainV2Path("example", train.ID)
	original, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestBytes(original)
	ctx := trainV2RetirementTestContext()
	input := TrainV2LegacyStateMigrationInput{
		ProjectID: "example",
		Reason:    "preserve pre-cutover history",
		Actions:   []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionMarkHistorical, TrainID: train.ID, TrainSHA256: digest}},
	}
	dry, err := s.TrainV2MigrateLegacyState(ctx, input)
	if err != nil || !dry.DryRun || len(dry.Records) != 1 {
		t.Fatalf("unexpected dry-run: %#v err=%v", dry, err)
	}
	unchanged, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("dry-run changed historical bytes: err=%v", err)
	}
	applyInput := input
	applyInput.Apply = true
	applyInput.ExpectedHubRevision = dry.HubBefore
	applied, err := s.TrainV2MigrateLegacyState(ctx, applyInput)
	if err != nil || !applied.Applied {
		t.Fatalf("migration apply failed: %#v err=%v", applied, err)
	}
	var migrated model.TrainV2
	if err := s.Hub.ReadJSON(context.Background(), path, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.Historical == nil || migrated.Historical.SourceSHA256 != digest || migrated.Status != model.TrainV2RecoveryQuarantined {
		t.Fatalf("historical disposition missing: %#v", migrated)
	}
	retry, err := s.TrainV2MigrateLegacyState(ctx, input)
	if err != nil || !retry.AlreadyDone {
		t.Fatalf("migration was not idempotent: %#v err=%v", retry, err)
	}
	bad := input
	bad.Actions = []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionMarkHistorical, TrainID: train.ID, TrainSHA256: strings.Repeat("f", 64)}}
	if _, err := s.TrainV2MigrateLegacyState(ctx, bad); err == nil {
		t.Fatal("stale digest was accepted")
	}
}

func TestTrainV2LegacyMigrationRecoversFailedIntegrationByDigest(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train, _ := reviewBackfillFixture(t)
	train.ID = "EXM-TRN2"
	train.Status = model.TrainV2ReadyForIntegration
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed integration migration", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	var err error
	revision, err = s.hubRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	op := trainv2.IntegrationOperation{SchemaVersion: 1, OperationID: "GTW-INTEGRATE-000000000000000000000001", ProjectID: "example", TrainID: train.ID, RequestSHA256: strings.Repeat("a", 64), SourceHead: strings.Repeat("b", 40), TargetBranch: "main", TargetBefore: strings.Repeat("c", 40), Phase: trainv2.IntegrationPhasePrePending, UpdatedAt: now}
	seedTrainIntegrationOperation(t, s, revision, op)
	mutationInput := []byte(`{"project_id":"example","train_id":"EXM-TRN2"}`)
	if err := s.writeDurableMutation(durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   "mutation-legacy-integration",
		Kind:          "train-v2-integrate",
		RequestSHA256: durableMutationDigest("train-v2-integrate", "", mutationInput),
		ProjectID:     "example",
		Input:         mutationInput,
		Status:        "failed",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, "mutation-legacy-integration")) })
	trainRaw, err := s.Hub.ReadFile(context.Background(), s.trainV2Path("example", train.ID))
	if err != nil {
		t.Fatal(err)
	}
	opRaw, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationPath("example", train.ID))
	if err != nil {
		t.Fatal(err)
	}
	mutationRaw, err := os.ReadFile(durableMutationPath(s.Config.StateDir, "mutation-legacy-integration"))
	if err != nil {
		t.Fatal(err)
	}
	input := TrainV2LegacyStateMigrationInput{
		ProjectID:           "example",
		Apply:               true,
		ExpectedHubRevision: mustHubRevision(t, s),
		Reason:              "reconcile failed integration prefix",
		Actions:             []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionRecoverIntegrate, TrainID: train.ID, TrainSHA256: digestBytes(trainRaw), IntegrationSHA256: digestBytes(opRaw), IntegrationMutationID: "mutation-legacy-integration", IntegrationMutationSHA256: digestBytes(mutationRaw)}},
	}
	result, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), input)
	if err != nil || !result.Applied {
		t.Fatalf("integration migration failed: %#v err=%v", result, err)
	}
	var recovered trainv2.IntegrationOperation
	if err := s.Hub.ReadJSON(context.Background(), trainV2IntegrationOperationPath("example", train.ID), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != trainv2.IntegrationPhaseRecoveryRequired {
		t.Fatalf("stale operation was not moved to recovery: %#v", recovered)
	}
	history, err := s.Hub.ReadFile(context.Background(), trainV2IntegrationOperationHistoryPath("example", train.ID, op.OperationID))
	if err != nil || string(history) != string(opRaw) {
		t.Fatalf("original integration bytes were not preserved: err=%v", err)
	}
	retryInput := input
	retryInput.ExpectedHubRevision = mustHubRevision(t, s)
	retry, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), retryInput)
	if err != nil || !retry.AlreadyDone {
		t.Fatalf("integration migration was not idempotent: %#v err=%v", retry, err)
	}
	wrongMutationID := retryInput
	wrongMutationID.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	wrongMutationID.Actions[0].IntegrationMutationID = "mutation-other-integration"
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), wrongMutationID); err == nil {
		t.Fatal("receipt accepted a different integration mutation ID")
	}
	wrongMutationDigest := retryInput
	wrongMutationDigest.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	wrongMutationDigest.Actions[0].IntegrationMutationSHA256 = strings.Repeat("f", 64)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), wrongMutationDigest); err == nil {
		t.Fatal("receipt accepted a different integration mutation digest")
	}
	pathTraversal := retryInput
	pathTraversal.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	pathTraversal.Actions[0].IntegrationMutationID = "../outside-state"
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), pathTraversal); err == nil {
		t.Fatal("path-traversal integration mutation ID was accepted")
	}
	receiptRaw, err := s.Hub.ReadFile(context.Background(), result.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	evidencePath := trainV2LegacyMigrationEvidencePath("example", train.ID, digestBytes(trainRaw))
	evidenceRaw, err := s.Hub.ReadFile(context.Background(), evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var receipt model.TrainV2LegacyStateMigrationReceipt
	if err := decodeStrict(receiptRaw, &receipt); err != nil {
		t.Fatal(err)
	}
	var evidence model.TrainV2LegacyStateMigrationRecord
	if err := decodeStrict(evidenceRaw, &evidence); err != nil {
		t.Fatal(err)
	}
	writeHubJSON := func(path string, value any) {
		t.Helper()
		revision := mustHubRevision(t, s)
		if _, err := s.Hub.Transact(context.Background(), revision, "test: tamper migration evidence", func(worktree string) ([]string, error) {
			if err := hub.WriteJSON(worktree, path, value); err != nil {
				return nil, err
			}
			return []string{path}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	tamperedReceipt := receipt
	tamperedReceipt.Reason = "tampered receipt"
	writeHubJSON(result.ReceiptPath, tamperedReceipt)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), retryInput); err == nil {
		t.Fatal("tampered migration receipt was accepted")
	}
	writeHubJSON(result.ReceiptPath, receipt)
	tamperedEvidence := evidence
	tamperedEvidence.TrainPath = s.trainV2Path("example", "EXM-TRN3")
	writeHubJSON(evidencePath, tamperedEvidence)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), retryInput); err == nil {
		t.Fatal("tampered migration evidence was accepted")
	}
	writeHubJSON(evidencePath, evidence)
	var malformedMutation durableMutationOperation
	if err := decodeStrict(mutationRaw, &malformedMutation); err != nil {
		t.Fatal(err)
	}
	malformedMutation.Input = json.RawMessage(`{}`)
	malformedMutationRaw, err := json.Marshal(malformedMutation)
	if err != nil {
		t.Fatal(err)
	}
	malformedRecord := evidence
	malformedRecord.MutationSHA256 = digestBytes(malformedMutationRaw)
	malformedRecord.OriginalMutationJSONB64 = base64.StdEncoding.EncodeToString(malformedMutationRaw)
	malformedReceipt := receipt
	malformedReceipt.Records = append([]model.TrainV2LegacyStateMigrationRecord{}, receipt.Records...)
	malformedReceipt.Records[0] = malformedRecord
	writeHubJSON(evidencePath, malformedRecord)
	writeHubJSON(result.ReceiptPath, malformedReceipt)
	if err := os.WriteFile(durableMutationPath(s.Config.StateDir, "mutation-legacy-integration"), malformedMutationRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	malformedInput := retryInput
	malformedInput.ExpectedHubRevision = mustHubRevision(t, s)
	malformedInput.Actions = append([]TrainV2LegacyStateMigrationAction{}, retryInput.Actions...)
	malformedInput.Actions[0].IntegrationMutationSHA256 = digestBytes(malformedMutationRaw)
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), malformedInput); err == nil {
		t.Fatal("malformed original mutation evidence was accepted")
	}
}

func TestStateCheckIgnoresExplicitHistoricalDuplicateButKeepsCanonicalOwner(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	first, _ := reviewBackfillFixture(t)
	second, _ := reviewBackfillFixture(t)
	first.ID, second.ID = "EXM-TRN1", "EXM-TRN2"
	first.Status = model.TrainV2RecoveryQuarantined
	second.Status = model.TrainV2Completed
	second.Items[0].Status = model.TrainV2ItemFinalized
	second.Historical = nil
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed historical duplicate", func(worktree string) ([]string, error) {
		firstPath, secondPath := s.trainV2Path("example", first.ID), s.trainV2Path("example", second.ID)
		if err := hub.WriteJSON(worktree, firstPath, first); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, secondPath, second); err != nil {
			return nil, err
		}
		return []string{firstPath, secondPath}, nil
	}); err != nil {
		t.Fatal(err)
	}
	firstPath := s.trainV2Path("example", first.ID)
	firstRaw, err := s.Hub.ReadFile(context.Background(), firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), TrainV2LegacyStateMigrationInput{
		ProjectID:           "example",
		Apply:               true,
		ExpectedHubRevision: mustHubRevision(t, s),
		Reason:              "pre-cutover duplicate retained as history",
		Actions:             []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionMarkHistorical, TrainID: first.ID, TrainSHA256: digestBytes(firstRaw)}},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := s.StateCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Fatalf("historical duplicate still blocked StateCheck: %#v", result.Issues)
	}
}

func TestHistoricalDuplicateReservesAdmissionButDoesNotOwnCanonicalStart(t *testing.T) {
	s, _, _ := testService(t)
	now := nowUTC()
	historical, _ := reviewBackfillFixture(t)
	historical.ID = "EXM-TRN1"
	historical.Status = model.TrainV2RecoveryQuarantined
	historical.Historical = &model.TrainV2HistoricalDisposition{
		Kind:         model.TrainV2HistoricalDispositionKind,
		SourcePath:   s.trainV2Path("example", historical.ID),
		SourceSHA256: strings.Repeat("a", 64),
		Reason:       "historical duplicate",
		MarkedAt:     now,
	}
	canonical := historical
	canonical.ID = "EXM-TRN2"
	canonical.Historical = nil
	canonical.Status = model.TrainV2Running
	worktree := t.TempDir()
	root := filepath.Join(worktree, filepath.FromSlash(s.trainV2Root("example")))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, train := range []model.TrainV2{historical, canonical} {
		if err := hub.WriteJSON(worktree, s.trainV2Path("example", train.ID), train); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.validateTrainV2TaskMembershipInWorktree(worktree, "example", canonical.ID); err != nil {
		t.Fatalf("historical duplicate blocked canonical start: %v", err)
	}
	if err := s.validateTrainV2TaskMembershipInWorktree(worktree, "example", historical.ID); err == nil {
		t.Fatal("historical Train remained startable")
	}
	if err := trainv2.ValidateUnadmitted([]model.TrainV2{historical}, []string{historical.Items[0].TaskID}); err == nil {
		t.Fatal("historical Task was released for re-admission")
	}
}

func TestTrainV2LegacyMigrationRetiresProvenStaleTrain(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	train := staleTrainV2ForRetirementTest(nowUTC())
	train.ID = "EXM-TRN13"
	if _, err := s.Hub.Transact(context.Background(), revision, "test: seed stale Train migration", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	}); err != nil {
		t.Fatal(err)
	}
	path := s.trainV2Path("example", train.ID)
	raw, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.TrainV2MigrateLegacyState(trainV2RetirementTestContext(), TrainV2LegacyStateMigrationInput{
		ProjectID:           "example",
		Apply:               true,
		ExpectedHubRevision: mustHubRevision(t, s),
		Reason:              "retire stale non-live Train",
		Actions:             []TrainV2LegacyStateMigrationAction{{Action: TrainV2LegacyActionRetireStale, TrainID: train.ID, TrainSHA256: digestBytes(raw)}},
	})
	if err != nil || !result.Applied {
		t.Fatalf("stale migration failed: %#v err=%v", result, err)
	}
	var retired model.TrainV2
	if err := s.Hub.ReadJSON(context.Background(), path, &retired); err != nil {
		t.Fatal(err)
	}
	if retired.Status != model.TrainV2Retired || retired.Retirement == nil || retired.Retirement.PreviousStatus != model.TrainV2Blocked {
		t.Fatalf("stale Train was not retired with evidence: %#v", retired)
	}
}

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
