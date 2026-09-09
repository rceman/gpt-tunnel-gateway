package service

import (
	"context"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestFreshSharedBaselineAllowsAuthoringWithoutBootstrapMarker(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	in := TaskAuthoringCreateInput{
		ProjectID:          "example",
		Title:              "Marker task",
		Summary:            "Verify baseline authoring.",
		Objective:          "Require bootstrap first.",
		AcceptanceCriteria: []string{"marker"},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
	}
	defer db.Close()
	s.Durability = db
	if _, _, err := s.taskAuthoringCreateShared(context.Background(), "op-fresh-baseline", in); err != nil {
		t.Fatalf("fresh baseline authoring: %v", err)
	}
	rows, err := db.Shared.Query(context.Background(), `SELECT COUNT(*) FROM shared_bootstrap_markers`)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(0) {
		t.Fatalf("fresh baseline synthesized bootstrap marker: %#v %v", rows.Rows, err)
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
