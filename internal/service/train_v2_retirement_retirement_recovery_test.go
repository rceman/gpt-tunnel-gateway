package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func seedTrainV2RecordsForRetirementTest(t *testing.T, s *Service, revision string, trains ...model.TrainV2) string {
	t.Helper()
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed Train conflict records", func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(trains))
		for _, train := range trains {
			path := s.trainV2Path("example", train.ID)
			if err := hub.WriteJSON(worktree, path, train); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		return paths, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx.After
}
func TestTrainV2StartDoesNotRejectDisjointActiveTrain(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	active := staleTrainV2ForRetirementTest(now)
	active.ID = "EXM-TRN2"
	active.Status = model.TrainV2Running
	active.Items[0].Status = model.TrainV2ItemRunning
	active.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	active.Items[0].Attempts[0].FinishedAt = nil
	target := staleTrainV2ForRetirementTest(now)
	target.ID = "EXM-TRN3"
	target.Status = model.TrainV2Planned
	target.Items[0].Status = model.TrainV2ItemQueued
	target.Items[0].Attempts = nil
	revision = seedTrainV2RecordsForRetirementTest(t, s, revision, active, target)
	_, err := s.TrainV2Start(context.Background(), TrainV2StartInput{
		ProjectID: "example",
		TrainID:   target.ID,
		StartedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil && strings.Contains(err.Error(), "TRAIN_ACTIVE_CONFLICT") {
		t.Fatalf("Train start retained project-wide active Train rejection: %v", err)
	}
}
func TestTrainV2AdvanceDoesNotRejectDisjointActiveTrain(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	active := staleTrainV2ForRetirementTest(now)
	active.ID = "EXM-TRN2"
	active.Status = model.TrainV2Running
	active.Items[0].Status = model.TrainV2ItemRunning
	active.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	active.Items[0].Attempts[0].FinishedAt = nil
	target := staleTrainV2ForRetirementTest(now)
	target.ID = "EXM-TRN3"
	target.Status = model.TrainV2Running
	revision = seedTrainV2RecordsForRetirementTest(t, s, revision, active, target)
	_, err := s.TrainV2Advance(context.Background(), TrainV2AdvanceInput{
		ProjectID: "example",
		TrainID:   target.ID,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	})
	if err != nil && strings.Contains(err.Error(), "TRAIN_ACTIVE_CONFLICT") {
		t.Fatalf("Train advance retained project-wide active Train rejection: %v", err)
	}
}
