package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

func TestTaskAuthoringAsyncMutationsCommitSharedBeforeHubSync(t *testing.T) {
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
	markSharedBootstrapCompleteForTest(t, db)

	created, err := s.TaskAuthoringCreateAsync(context.Background(), TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Shared task", Objective: "Commit task state locally first.",
		AcceptanceCriteria: []string{"one shared task"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReceipt := waitTaskCreateReceipt(t, s, created.OperationID)
	if createReceipt.Task == nil || createReceipt.Operation.Status != "planned" {
		t.Fatalf("create receipt=%#v", createReceipt)
	}

	updatedTitle := "Updated shared task"
	updated, err := s.TaskAuthoringUpdateAsync(context.Background(), TaskAuthoringUpdateInput{
		ProjectID: "example", TaskID: createReceipt.Task.ID, ExpectedRevision: createReceipt.Task.Revision,
		ExpectedRevisionSHA256: createReceipt.Task.RevisionSHA256, Title: &updatedTitle, UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReceipt := waitTaskUpdateReceipt(t, s, updated.OperationID)
	if updateReceipt.Task == nil || updateReceipt.Task.Title != updatedTitle {
		t.Fatalf("update receipt=%#v", updateReceipt)
	}

	ready, err := s.TaskAuthoringReadyAsync(context.Background(), TaskAuthoringReadyInput{
		ProjectID: "example", TaskID: updateReceipt.Task.ID, ExpectedRevision: updateReceipt.Task.Revision,
		ExpectedRevisionSHA256: updateReceipt.Task.RevisionSHA256, ReadyBy: "planner",
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
		t.Fatalf("pending outbox entries=%d, want 3: %#v", len(pending), pending)
	}
	shared, err := db.ReadSharedTask(context.Background(), readyReceipt.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored model.TaskAuthoring
	if err := json.Unmarshal(shared.Payload, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.TaskAuthoringReady || stored.Revision != readyReceipt.Task.Revision {
		t.Fatalf("shared task=%#v", stored)
	}
	if _, err := s.TaskAuthoringRead(context.Background(), "example", readyReceipt.Task.ID); !IsNotFound(err) {
		t.Fatalf("task mutation wrote Hub synchronously: %v", err)
	}
	for _, entry := range pending {
		if err := s.publishSharedOutboxEntry(context.Background(), entry); err != nil {
			t.Fatalf("publish outbox %s: %v", entry.ID, err)
		}
		if err := db.MarkOutboxPublished(context.Background(), entry.ID, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	published, err := s.TaskAuthoringRead(context.Background(), "example", readyReceipt.Task.ID)
	if err != nil || published.Status != model.TaskAuthoringReady {
		t.Fatalf("published Hub task=%#v err=%v", published, err)
	}
	if revision == "" {
		t.Fatal("test fixture did not establish Hub baseline")
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
	markSharedBootstrapCompleteForTest(t, db)
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")

	created, err := s.TaskAuthoringCreateAsync(context.Background(), TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Offline shared task", Objective: "Commit without Hub availability.",
		AcceptanceCriteria: []string{"create", "update", "ready"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReceipt := waitTaskCreateReceipt(t, s, created.OperationID)
	updatedTitle := "Offline updated task"
	updated, err := s.TaskAuthoringUpdateAsync(context.Background(), TaskAuthoringUpdateInput{
		ProjectID: "example", TaskID: createReceipt.Task.ID, ExpectedRevision: createReceipt.Task.Revision,
		ExpectedRevisionSHA256: createReceipt.Task.RevisionSHA256, Title: &updatedTitle, UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReceipt := waitTaskUpdateReceipt(t, s, updated.OperationID)
	ready, err := s.TaskAuthoringReadyAsync(context.Background(), TaskAuthoringReadyInput{
		ProjectID: "example", TaskID: updateReceipt.Task.ID, ExpectedRevision: updateReceipt.Task.Revision,
		ExpectedRevisionSHA256: updateReceipt.Task.RevisionSHA256, ReadyBy: "planner",
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
	markSharedBootstrapCompleteForTest(t, db)
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")

	dependencyID := "GTW-TSK324"
	task, err := trainv2.NewTask("example", "EXM-TSK330", trainv2.AuthoringDraft{
		Title: "Dependent task", Objective: "Require a locally proven integration.",
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
	readyInput := TaskAuthoringReadyInput{ProjectID: "example", TaskID: task.ID, ExpectedRevision: task.Revision, ExpectedRevisionSHA256: task.RevisionSHA256, ReadyBy: "planner"}
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

func TestTaskAuthoringUpdateSharedBootstrapsLegacyTaskBeforeHubUnavailable(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	revision = adoptAuthoringIdentifiersForTest(t, s, revision)
	revision = enableTrainV2ForTest(t, s, revision)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	legacy, _, err := s.TaskAuthoringCreate(context.Background(), TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Legacy shared task", Objective: "Bootstrap before local-only mutation.",
		AcceptanceCriteria: []string{"update survives Hub outage"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
		WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	bootstrapCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	if err := s.BootstrapSharedFromHub(bootstrapCtx); err != nil {
		t.Fatalf("shared bootstrap: %v", err)
	}
	title := "Updated while Hub is unavailable"
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	started, err := s.TaskAuthoringUpdateAsync(context.Background(), TaskAuthoringUpdateInput{
		ProjectID: "example", TaskID: legacy.ID, ExpectedRevision: legacy.Revision,
		ExpectedRevisionSHA256: legacy.RevisionSHA256, Title: &title, UpdatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := waitTaskUpdateReceipt(t, s, started.OperationID)
	if updated.Task == nil || updated.Task.Title != title {
		t.Fatalf("Hub-unavailable update receipt=%#v", updated)
	}
}

func TestSharedBootstrapMarkerBlocksAuthoringAndSurvivesRestart(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s.Durability = db
	in := TaskAuthoringCreateInput{ProjectID: "example", Title: "Marker task", Objective: "Require bootstrap first.", AcceptanceCriteria: []string{"marker"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner"}
	if _, _, err := s.taskAuthoringCreateShared(context.Background(), "op-before-bootstrap", in); err == nil || !strings.Contains(err.Error(), "bootstrap is incomplete") {
		t.Fatalf("authoring before bootstrap error=%v", err)
	}
	markSharedBootstrapCompleteForTest(t, db)
	if _, _, err := s.taskAuthoringCreateShared(context.Background(), "op-after-bootstrap", in); err != nil {
		t.Fatalf("authoring after bootstrap: %v", err)
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
	complete, err := db.SharedBootstrapComplete(context.Background(), "example")
	if err != nil || !complete {
		t.Fatalf("bootstrap marker after restart: complete=%v err=%v", complete, err)
	}
	if err := s.requireLocalTaskAuthoring(context.Background(), "example"); err != nil {
		t.Fatalf("restart lost bootstrap authority: %v", err)
	}
}

func markSharedBootstrapCompleteForTest(t *testing.T, db *sqlitestore.Databases) {
	t.Helper()
	if err := db.MarkSharedBootstrapComplete(context.Background(), sqlitestore.SharedBootstrapMarker{ProjectID: "example", HubRevision: "fixture", CompletedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
}

func waitTaskCreateReceipt(t *testing.T, s *Service, operationID string) TaskCreateReceipt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		receipt, err := s.TaskCreateOperationStatus(context.Background(), operationID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status == "completed" || receipt.Status == "failed" || receipt.Status == "outcome_unknown" {
			if receipt.Status != "completed" {
				t.Fatalf("task/create receipt=%#v", receipt)
			}
			return receipt
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task/create receipt did not complete: %s", operationID)
	return TaskCreateReceipt{}
}

func waitTaskUpdateReceipt(t *testing.T, s *Service, operationID string) TaskAuthoringUpdateReceipt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		receipt, err := s.TaskAuthoringUpdateOperationStatus(context.Background(), operationID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status == "completed" {
			return receipt
		}
		if receipt.Status == "failed" || receipt.Status == "outcome_unknown" {
			t.Fatalf("task/update receipt=%#v", receipt)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task/update receipt did not complete: %s", operationID)
	return TaskAuthoringUpdateReceipt{}
}

func waitTaskReadyReceipt(t *testing.T, s *Service, operationID string) TaskAuthoringReadyReceipt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		receipt, err := s.TaskAuthoringReadyOperationStatus(context.Background(), operationID)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Status == "completed" {
			return receipt
		}
		if receipt.Status == "failed" || receipt.Status == "outcome_unknown" {
			t.Fatalf("task/ready receipt=%#v", receipt)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task mutation receipt did not complete: %s", operationID)
	return TaskAuthoringReadyReceipt{}
}
