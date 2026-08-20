package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

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
