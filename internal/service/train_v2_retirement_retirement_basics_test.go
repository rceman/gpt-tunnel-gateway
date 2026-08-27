package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
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
