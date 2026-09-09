package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestSharedQueriesScopeBeforeGlobalPageLimit(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < 1001; i++ {
		id := fmt.Sprintf("AAA-TSK%04d", i)
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, id, 1, []byte(`{"project_id":"other"}`), now); err != nil {
			t.Fatal(err)
		}
		adrID := fmt.Sprintf("AAA-ADR%04d", i)
		if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, adrID, 1, []byte(`{"project_id":"other"}`), now); err != nil {
			t.Fatal(err)
		}
	}
	task, err := trainv2.NewTask("example", "EXM-TSK903", trainv2.AuthoringDraft{Title: "After page", Summary: "Remain visible after pagination.", Objective: "Remain visible", ADRRelation: model.TaskADRNoRequired}, "planner", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, task.Revision, taskPayload, now); err != nil {
		t.Fatal(err)
	}
	adr := model.ADR{SchemaVersion: model.SchemaVersion, ID: "EXM-ADR903", ProjectID: "example", Title: "After page", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: time.Now().UTC()}
	adrPayload, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(ctx, `INSERT INTO shared_adrs(id,revision,payload,updated_at) VALUES(?,?,?,?)`, adr.ID, 1, adrPayload, now); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	listed, err := s.TaskAuthoringList(ctx, TaskAuthoringListInput{
		ProjectID: "example",
		Limit:     1,
	})
	if err != nil || len(listed.Tasks) != 1 || listed.Tasks[0].ID != task.ID {
		t.Fatalf("task after global page was lost: %#v %v", listed, err)
	}
	listedADRs, err := s.ADRList(ctx, "example")
	if err != nil || len(listedADRs) != 1 || listedADRs[0].ID != adr.ID {
		t.Fatalf("ADR after global page was lost: %#v %v", listedADRs, err)
	}
}

func TestSharedProjectEntitiesDeduplicateKeysetRepeats(t *testing.T) {
	page := []sqlitestore.SharedEntity{
		{ID: "EXM-TSK904", Payload: []byte(`{"project_id":"example"}`)},
		{ID: "EXM-TSK904", Payload: []byte(`{"project_id":"example"}`)},
	}
	entities, err := sharedProjectEntitiesFromPage(page, "task", "example", map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entities) != 1 || entities[0].ID != "EXM-TSK904" {
		t.Fatalf("repeated keyset entity was not deduplicated: %#v", entities)
	}
}

func TestSharedADRPublishConvergesAfterRestart(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	adr := model.ADR{SchemaVersion: model.SchemaVersion, ID: "EXM-ADR1", ProjectID: "example", Title: "Local ADR", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: time.Now().UTC()}
	payload, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := db.CommitSharedADRCreate(context.Background(), sqlitestore.SharedADRCreate{OperationID: "OPR-ADR-RESTART", ProjectID: "example", ProjectCode: "EXM", Kind: "adr-create", BuildPayload: func(string) ([]byte, error) { return payload, nil }}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	pending, err := db.PendingOutbox(context.Background(), 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("restart pending=%#v err=%v", pending, err)
	}
	if err := s.publishSharedOutboxEntry(context.Background(), pending[0]); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkOutboxPublished(context.Background(), pending[0].ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var published model.ADR
	if err := s.Hub.ReadJSON(context.Background(), s.adrPath("example", adr.ID), &published); err != nil {
		t.Fatal(err)
	}
	if published.ID != adr.ID || published.Title != adr.Title {
		t.Fatalf("published ADR=%#v", published)
	}
}

func TestTaskAuthoringAsyncMutationsCommitSharedWhenHubUnavailable(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	revision = adoptAuthoringIdentifiersForTest(t, s, revision)
	revision = enableTrainV2ForTest(t, s, revision)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")

	created, err := s.TaskAuthoringCreateAsync(context.Background(), TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Offline shared task",
		Summary:            "Commit shared state without Hub.",
		Objective:          "Commit without Hub availability.",
		AcceptanceCriteria: []string{"create", "update", "ready"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReceipt := waitTaskCreateReceipt(t, s, created.OperationID)
	updatedTitle := "Offline updated task"
	updated, err := s.TaskAuthoringUpdateAsync(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 createReceipt.Task.ID,
		ExpectedRevision:       createReceipt.Task.Revision,
		ExpectedRevisionSHA256: createReceipt.Task.RevisionSHA256,
		Title:                  &updatedTitle,
		UpdatedBy:              "planner",
		Reason:                 "update offline shared task",
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReceipt := waitTaskUpdateReceipt(t, s, updated.OperationID)
	ready, err := s.TaskAuthoringReadyAsync(context.Background(), TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 updateReceipt.Task.ID,
		ExpectedRevision:       updateReceipt.Task.Revision,
		ExpectedRevisionSHA256: updateReceipt.Task.RevisionSHA256,
		ReadyBy:                "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	readyReceipt := waitTaskReadyReceipt(t, s, ready.OperationID)
	if readyReceipt.Task == nil || readyReceipt.Task.Status != model.TaskAuthoringReady {
		t.Fatalf("ready receipt=%#v", readyReceipt)
	}
	pending, err := db.PendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Fatalf("pending outbox entries=%d, want 3", len(pending))
	}
	shared, err := db.ReadSharedTask(context.Background(), readyReceipt.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored model.TaskAuthoring
	if err := json.Unmarshal(shared.Payload, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.TaskAuthoringReady {
		t.Fatalf("shared task=%#v", stored)
	}
}

func TestTaskAuthoringReadySharedRequiresLocalIntegrationReceipt(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")

	dependencyID := "GTW-TSK324"
	task, err := trainv2.NewTask("example", "EXM-TSK330", trainv2.AuthoringDraft{
		Title: "Dependent task", Summary: "Require a locally proven integration.", Objective: "Require a locally proven integration.",
		AcceptanceCriteria: []string{"local receipt is required"}, Dependencies: []string{dependencyID},
		ADRRelation: model.TaskADRNoRequired,
	}, "planner", time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SeedSharedTask(context.Background(), sqlitestore.SharedTask{ID: task.ID, Revision: int64(task.Revision), Payload: payload, UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	train, integration := dependencyIntegrationFixture(model.TrainV2Completed)
	trainPayload, err := json.Marshal(train)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), `INSERT INTO shared_trains(id,revision,payload,updated_at) VALUES(?,?,?,?)`, train.ID, train.Revision, trainPayload, train.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	readyInput := TaskAuthoringReadyInput{
		ProjectID:              "example",
		TaskID:                 task.ID,
		ExpectedRevision:       task.Revision,
		ExpectedRevisionSHA256: task.RevisionSHA256,
		ReadyBy:                "planner",
	}
	if _, _, err := s.taskAuthoringReadyShared(context.Background(), "op-missing-receipt", readyInput); err == nil || !strings.Contains(err.Error(), "dependency-not-integrated") {
		t.Fatalf("missing local integration receipt error=%v", err)
	}
	integrationPayload, err := json.Marshal(integration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedIntegrationReceipt(context.Background(), sqlitestore.SharedIntegrationReceipt{ID: sqlitestore.SharedIntegrationReceiptID("example", train.ID), Revision: 1, Payload: integrationPayload, UpdatedAt: integration.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	ready, _, err := s.taskAuthoringReadyShared(context.Background(), "op-with-receipt", readyInput)
	if err != nil {
		t.Fatalf("ready with local integration receipt: %v", err)
	}
	if ready.Status != model.TaskAuthoringReady {
		t.Fatalf("ready task status=%q", ready.Status)
	}
}
