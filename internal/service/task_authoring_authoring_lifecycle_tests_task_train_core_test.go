package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTaskAuthoringServiceWiresADRReadiness(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	attachTSK409SharedDurability(t, s)
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	syncTSK409SharedConfigurationFromHub(t, s)
	adrResult, err := s.ADRCreate(context.Background(), ADRCreateInput{
		ADR: model.ADR{ProjectID: "example", Title: "Accepted decision", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:     "example",
		Title:         "ADR-linked task",
		Objective:     "Require accepted ADR relation.",
		ADRRelation:   model.TaskADRImplementsExisting,
		ADRReferences: []string{"EXM-ADR1"},
		CreatedBy:     "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: adrResult.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.PutSharedProjection(context.Background(), "task", sqlitestore.SharedEntity{
		ID: task.ID, Revision: int64(task.Revision), Payload: taskPayload,
		UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	})
	if err != nil || ready.Status != model.TaskAuthoringReady {
		t.Fatalf("accepted ADR task was not readied: %#v %v", ready, err)
	}
	bad, badOperation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:     "example",
		Title:         "Bad ADR task",
		Objective:     "Reject missing ADR at readiness.",
		ADRRelation:   model.TaskADRImplementsExisting,
		ADRReferences: []string{"EXM-ADR99"},
		CreatedBy:     "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: "",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringReady(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 bad.ID,
		ExpectedRevision:       bad.Revision,
		ExpectedRevisionSHA256: bad.RevisionSHA256,
		ReadyBy:                "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: badOperation.Hub.After,
		},
	}); err == nil {
		t.Fatal("missing ADR task was readied")
	}
}

func TestTaskAuthoringRequiresTrainV2AndOptimisticRevision(t *testing.T) {
	s, hubRevision, _ := testServiceWithoutIdentifiers(t)
	if _, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Legacy blocked",
		Objective:   "Must require train_v2.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}); err == nil {
		t.Fatal("legacy project accepted train_v2 authoring")
	}
	hubRevision = adoptAuthoringIdentifiersForTest(t, s, hubRevision)
	hubRevision = enableTrainV2ForTest(t, s, hubRevision)
	task, operation, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID:   "example",
		Title:       "Revision guard",
		Objective:   "Exercise optimistic revision.",
		ADRRelation: model.TaskADRNoRequired,
		CreatedBy:   "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TaskAuthoringUpdate(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision + 1,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		UpdatedBy:              "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: operation.Hub.After,
		},
	}); err == nil {
		t.Fatal("stale revision was accepted")
	}
}
