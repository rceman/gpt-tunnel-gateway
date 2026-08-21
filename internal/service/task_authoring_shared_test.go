package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
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
