package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestADRCreateAsyncIsBoundedAndIdempotent(t *testing.T) {
	s, revision, _ := testServiceWithoutIdentifiers(t)
	attachTSK409SharedDurability(t, s)
	revision = adoptAuthoringIdentifiersForTest(t, s, revision)
	in := ADRCreateInput{
		ADR: model.ADR{
			ProjectID:    "example",
			Title:        "Async decision",
			Status:       "accepted",
			Context:      "context",
			Decision:     "decision",
			Consequences: "consequences",
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}

	started := time.Now()
	first, err := s.ADRCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("ADR create initiation exceeded one second: %s", elapsed)
	} else {
		t.Logf("ADR create initiation latency: %s", elapsed)
	}
	second, err := s.ADRCreateAsync(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == "" || first.OperationID != second.OperationID {
		t.Fatalf("ADR create initiation was not idempotent: first=%#v second=%#v", first, second)
	}

	deadline := time.Now().Add(10 * time.Second)
	var completed ADRCreateReceipt
	for time.Now().Before(deadline) {
		completed, err = s.ADRCreateOperationStatus(context.Background(), first.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if completed.Status == "completed" || completed.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.Status != "completed" || completed.Operation == nil {
		t.Fatalf("ADR create worker did not complete: %#v", completed)
	}
}

func attachTSK409SharedDurability(t *testing.T, s *Service) {
	t.Helper()
	configuration, err := s.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s.Durability = db
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	if _, err := db.Shared.Exec(context.Background(), `INSERT OR REPLACE INTO shared_project_identifiers(project_id,project_code,next_task_number,next_adr_number,next_rule_number,next_train_number,next_journal_number) VALUES(?,?,?,?,?,?,?)`, "example", "EXM", 1, 1, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Shared.Exec(context.Background(), `INSERT OR REPLACE INTO shared_entity_sequences(entity_type,project_id,project_code,next_number) VALUES(?,?,?,?)`, "adr", "example", "EXM", 1); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload,
		UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}

func syncTSK409SharedConfigurationFromHub(t *testing.T, s *Service) {
	t.Helper()
	var configuration model.ProjectConfiguration
	if err := s.Hub.ReadJSON(context.Background(), s.projectConfigurationPath("example"), &configuration); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.PutSharedProjection(context.Background(), "project_configuration", sqlitestore.SharedEntity{
		ID: configuration.ProjectID, Revision: int64(configuration.Revision), Payload: payload,
		UpdatedAt: configuration.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
}
