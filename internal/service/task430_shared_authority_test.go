package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestTSK430AgentAndTrainLifecycleUseSharedWhenHubUnavailable(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	ready, revision := readyTrainTaskForTest(t, s, revision, "Shared lifecycle task")
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "missing-hub.git")
	s.Config.Hub.RepositoryURL = s.Hub.Config.Hub.RepositoryURL

	if _, err := s.AgentRead(context.Background(), "example", "coder-example"); err != nil {
		t.Fatalf("AgentRead used unavailable Hub: %v", err)
	}
	if _, err := s.AgentList(context.Background(), "example"); err != nil {
		t.Fatalf("AgentList used unavailable Hub: %v", err)
	}
	train, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{ready.ID}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatalf("TrainV2Create used unavailable Hub: %v", err)
	}
	if _, err := s.TrainV2Read(context.Background(), "example", train.ID); err != nil {
		t.Fatalf("TrainV2Read used unavailable Hub: %v", err)
	}
	if listed, err := s.TrainV2List(context.Background(), TrainV2ListInput{ProjectID: "example"}); err != nil || len(listed.Trains) != 1 {
		t.Fatalf("TrainV2List Shared authority failed: %#v err=%v", listed, err)
	}
}

func TestTSK430IntegrationReceiptCommitsSharedBeforeHubReplica(t *testing.T) {
	s, revision, _ := testService(t)
	revision = enableTrainV2ForTest(t, s, revision)
	ready, revision := readyTrainTaskForTest(t, s, revision, "Integration receipt task")
	train, _, err := s.TrainV2Create(context.Background(), TrainV2CreateInput{ProjectID: "example", TaskIDs: []string{ready.ID}, CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatalf("create Train: %v", err)
	}
	head := strings.Repeat("a", 40)
	receipt := trainv2.IntegrationReceipt{
		SchemaVersion: 1, ProjectID: "example", TrainID: train.ID,
		BaseRevision: head, LaneHead: head, TargetBefore: head,
		IntegrationHead: head, RuntimeHead: head, ProofCandidate: head,
		PreActivation: "ok", PreSmoke: "ok", PostActivation: "ok", PostSmoke: "ok",
		Status: "completed", NextAction: "complete", UpdatedAt: time.Now().UTC(),
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "missing-hub.git")
	s.Config.Hub.RepositoryURL = s.Hub.Config.Hub.RepositoryURL
	if err := s.writeTrainV2IntegrationReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("Shared integration receipt commit used unavailable Hub: %v", err)
	}
	entity, err := s.Durability.ReadSharedEntity(context.Background(), "integration_receipt", sqlitestore.SharedIntegrationReceiptID("example", train.ID))
	if err != nil {
		t.Fatalf("read Shared integration receipt: %v", err)
	}
	if entity.Revision != 1 || len(entity.Payload) == 0 {
		t.Fatalf("unexpected Shared integration receipt projection: %#v", entity)
	}
}
