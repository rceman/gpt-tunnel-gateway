package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestTrainV2StartBindsExactItemLocalAttempt(t *testing.T) {
	s, hubRevision, _ := testService(t)
	s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Attempt start")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ensureTaskWorkMirrorForTest(t, s)
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Attempt.Number != 1 || started.Attempt.AgentID != "coder-example" || started.Attempt.AirelaySessionKey != "example_master" || started.Record.CurrentItemPosition != 0 || started.Record.CurrentAttemptNumber != 1 {
		t.Fatalf("unexpected Attempt start: %#v", started)
	}
	if _, err := os.Stat(filepath.Join(s.Config.StateDir, "runs")); !os.IsNotExist(err) {
		t.Fatalf("Train-v2 start created legacy runs storage: %v", err)
	}
}

func TestTrainV2StartRejectsAttemptSessionMismatch(t *testing.T) {
	s, hubRevision, _ := testService(t)
	s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Attempt session")
	train, operation, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   train.ID,
		StartedBy: "planner",
		AgentID:   "unknown",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err == nil {
		t.Fatal("unknown Attempt owner was accepted")
	}
}

func TestTrainV2StartHistoricalDuplicateIsNotLiveOwner(t *testing.T) {
	s, hubRevision, _ := testService(t)
	s.TrainEvidence = persistence.NewLocalEvidenceStore(s.Config.StateDir)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, hubRevision := readyTrainTaskForTest(t, s, hubRevision, "Historical duplicate start")
	now := nowUTC()
	historical, err := trainv2.New("example", "EXM-TRN1", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	historical.Status = model.TrainV2RecoveryQuarantined
	historical.Historical = &model.TrainV2HistoricalDisposition{
		Kind:         model.TrainV2HistoricalDispositionKind,
		SourcePath:   s.trainV2Path("example", historical.ID),
		SourceSHA256: strings.Repeat("a", 64),
		Reason:       "historical duplicate",
		MarkedAt:     now,
	}
	canonical, err := trainv2.New("example", "EXM-TRN2", "planner", []model.TaskAuthoring{task}, now)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := s.Hub.Transact(context.Background(), hubRevision, "test: seed historical and canonical Trains", func(worktree string) ([]string, error) {
		historicalPath := s.trainV2Path("example", historical.ID)
		canonicalPath := s.trainV2Path("example", canonical.ID)
		if err := hub.WriteJSON(worktree, historicalPath, historical); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, canonicalPath, canonical); err != nil {
			return nil, err
		}
		return []string{historicalPath, canonicalPath}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []model.TrainV2{historical, canonical} {
		payload, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if putErr := s.Durability.PutSharedProjection(context.Background(), "train", sqlitestore.SharedEntity{ID: record.ID, Revision: int64(record.Revision), Payload: payload, UpdatedAt: record.UpdatedAt.UTC().Format(time.RFC3339Nano)}); putErr != nil {
			t.Fatal(putErr)
		}
	}
	if _, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{
		ProjectID: "example",
		TaskIDs:   []string{task.ID},
		CreatedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: seed.After,
		},
	}); err == nil {
		t.Fatal("historical Task was re-admitted")
	}
	ensureTaskWorkMirrorForTest(t, s)
	started, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   canonical.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: seed.After,
		},
	})
	if err != nil {
		t.Fatalf("canonical Train start was blocked by historical duplicate: %v", err)
	}
	if started.Attempt.Number != 1 || started.Record.CurrentTaskID != task.ID {
		t.Fatalf("unexpected canonical start result: %#v", started)
	}
}

func TestTrainV2AttemptValidationRemainsStrict(t *testing.T) {
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: "agent-one", AirelaySessionKey: "example_master", GatewayID: "gateway", StartHead: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", StartedAt: nowUTC()}
	if err := model.ValidateTrainV2Attempt(attempt); err != nil {
		t.Fatal(err)
	}
}
