package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

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
