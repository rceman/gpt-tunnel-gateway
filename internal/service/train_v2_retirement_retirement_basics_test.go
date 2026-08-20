package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func trainV2RetirementTestContext() context.Context {
	return WithAgentSessionID(authority.WithPlanner(context.Background()), "SP-ABCDEFGH")
}
func staleTrainV2ForRetirementTest(now time.Time) model.TrainV2 {
	finished := now.Add(-time.Minute)
	return model.TrainV2{
		SchemaVersion: model.TrainV2SchemaVersion, ID: "EXM-TRN1", ProjectID: "example", Revision: 2,
		Status: model.TrainV2Blocked, CreatedBy: "planner", CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Items: []model.TrainV2Item{{Position: 0, TaskID: "EXM-TSK1", TaskRevision: 1, TaskRevisionSHA256: strings.Repeat("a", 64), Status: model.TrainV2ItemBlocked, AddedAt: now.Add(-time.Hour), Attempts: []model.TrainV2Attempt{{Number: 1, Status: model.TrainV2AttemptFailed, AgentID: "agent-1", AirelaySessionKey: "session-1", GatewayID: "gateway-1", StartHead: strings.Repeat("b", 40), StartedAt: now.Add(-2 * time.Hour), FinishedAt: &finished}}}},
	}
}
func seedLiveTrainMutationForRetirementTest(t *testing.T, s *Service, kind string) string {
	t.Helper()
	operationID := "mutation-test-" + strings.ReplaceAll(kind, "_", "-")
	now := time.Now().UTC()
	if err := s.writeDurableMutation(durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   operationID,
		Kind:          kind,
		RequestSHA256: durableMutationDigest(kind, "", []byte(`{"train_id":"EXM-TRN1"}`)),
		ProjectID:     "example",
		Input:         []byte(`{"train_id":"EXM-TRN1"}`),
		Status:        "running",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(durableMutationPath(s.Config.StateDir, operationID)) })
	return operationID
}
func TestTrainV2RetireRecordsServerOwnedEvidenceAndIsIdempotent(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	now := time.Now().UTC()
	train := staleTrainV2ForRetirementTest(now)
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed stale train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := trainV2RetirementTestContext()
	input := TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "terminal failed Attempt has no live owner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	}
	retired, err := s.TrainV2Retire(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if retired.Status != model.TrainV2Retired || retired.Train.Retirement == nil || retired.Train.Retirement.ActorSessionID != "SP-ABCDEFGH" || retired.Train.Retirement.PreviousStatus != model.TrainV2Blocked {
		t.Fatalf("retirement evidence missing: %#v", retired)
	}
	second, err := s.TrainV2Retire(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.Train.Revision != retired.Train.Revision || second.Status != model.TrainV2Retired {
		t.Fatalf("retirement was not idempotent: %#v", second)
	}
}
func TestTrainV2RetireRejectsLiveAttempt(t *testing.T) {
	now := time.Now().UTC()
	train := staleTrainV2ForRetirementTest(now)
	train.Status = model.TrainV2Running
	train.Items[0].Status = model.TrainV2ItemRunning
	train.Items[0].Attempts[0].Status = model.TrainV2AttemptRunning
	train.Items[0].Attempts[0].FinishedAt = nil
	if err := model.ValidateTrainV2(train); err != nil {
		t.Fatal(err)
	}
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	tx, err := s.Hub.Transact(context.Background(), revision, "test: seed live train", func(worktree string) ([]string, error) {
		path := s.trainV2Path("example", train.ID)
		if err := hub.WriteJSON(worktree, path, train); err != nil {
			return nil, err
		}
		return []string{path}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TrainV2Retire(trainV2RetirementTestContext(), TrainV2RetireInput{
		ProjectID: "example",
		TrainID:   train.ID,
		Reason:    "must fail",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: tx.After,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "TRAIN_ATTEMPT_LIVE") {
		t.Fatalf("live Train was not rejected: %v", err)
	}
}
func TestTrainV2LiveOperationRecognizesAttemptMutationKinds(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	for _, kind := range []string{"train-attempt-finalize", "train-attempt-review", "train-attempt-proof-recovery"} {
		t.Run(kind, func(t *testing.T) {
			seedLiveTrainMutationForRetirementTest(t, s, kind)
			live, err := s.trainV2HasLiveOperation("example", "EXM-TRN1")
			if err != nil || !live {
				t.Fatalf("kind %q live=%v err=%v", kind, live, err)
			}
		})
	}
}
func TestTrainV2LiveOperationUnknownKindFailsClosed(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	seedLiveTrainMutationForRetirementTest(t, s, "train-v2-future-mutation")
	if _, err := s.trainV2HasLiveOperation("example", "EXM-TRN1"); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("unknown Train operation did not fail closed: %v", err)
	}
}
