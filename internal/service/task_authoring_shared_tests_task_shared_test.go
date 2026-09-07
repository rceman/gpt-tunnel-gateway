package service

import (
	"context"
	"encoding/json"
	"path/filepath"
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
		ProjectID:          "example",
		Title:              "Shared task",
		Objective:          "Commit task state locally first.",
		AcceptanceCriteria: []string{"one shared task"},
		Scope:              &model.TaskScope{Files: []string{"internal/service/task_authoring_shared.go"}, Modules: []string{"gateway"}},
		ADRRelation:        model.TaskADRNoRequired,
		CreatedBy:          "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReceipt := waitTaskCreateReceipt(t, s, created.OperationID)
	if createReceipt.Task == nil || createReceipt.Operation.Status != "planned" {
		t.Fatalf("create receipt=%#v", createReceipt)
	}
	trainTasks, err := s.TaskAuthoringList(context.Background(), TaskAuthoringListInput{
		ProjectID: "example",
		Execution: model.TaskExecutionTrain,
		Limit:     MaxTaskListLimit,
	})
	if err != nil || len(trainTasks.Tasks) != 0 {
		t.Fatalf("unassigned Shared Task matched train execution filter: %#v err=%v", trainTasks, err)
	}

	updatedTitle := "Updated shared task"
	updatedScope := &model.TaskScope{Files: []string{"internal/service/task_authoring_mutation.go"}, Modules: []string{"gateway"}}
	updated, err := s.TaskAuthoringUpdateAsync(context.Background(), TaskAuthoringUpdateInput{
		ProjectID:              "example",
		TaskID:                 createReceipt.Task.ID,
		ExpectedRevision:       createReceipt.Task.Revision,
		ExpectedRevisionSHA256: createReceipt.Task.RevisionSHA256,
		Title:                  &updatedTitle,
		Scope:                  updatedScope,
		UpdatedBy:              "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	updateReceipt := waitTaskUpdateReceipt(t, s, updated.OperationID)
	if updateReceipt.Task == nil || updateReceipt.Task.Title != updatedTitle || updateReceipt.Task.Execution != "" || updateReceipt.Task.Scope == nil || updateReceipt.Task.Scope.Files[0] != updatedScope.Files[0] {
		t.Fatalf("update receipt=%#v", updateReceipt)
	}
	filtered, err := s.TaskAuthoringList(context.Background(), TaskAuthoringListInput{
		ProjectID: "example",
		Execution: model.TaskExecutionHotfix,
		Limit:     MaxTaskListLimit,
	})
	if err != nil || len(filtered.Tasks) != 0 {
		t.Fatalf("Shared execution filter=%#v err=%v", filtered, err)
	}

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
	if stored.Status != model.TaskAuthoringReady || stored.Revision != readyReceipt.Task.Revision || stored.Execution != "" || stored.Scope == nil || stored.Scope.Files[0] != updatedScope.Files[0] {
		t.Fatalf("shared task=%#v", stored)
	}
	sharedRead, err := s.TaskAuthoringRead(context.Background(), "example", readyReceipt.Task.ID)
	if err != nil || sharedRead.Status != model.TaskAuthoringReady {
		t.Fatalf("Shared task/read failed before Hub publish: %#v %v", sharedRead, err)
	}
	var hubTask model.TaskAuthoring
	if err := s.Hub.ReadJSON(context.Background(), s.taskAuthoringPath("example", readyReceipt.Task.ID), &hubTask); !IsNotFound(err) {
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

func TestTaskAuthoringReadUsesSharedBeforeHub(t *testing.T) {
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
	created, err := trainv2.NewTask("example", "EXM-TSK900", trainv2.AuthoringDraft{Title: "Shared read", Objective: "Read locally", ADRRelation: model.TaskADRNoRequired}, "planner", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "task", sqlitestore.SharedEntity{ID: created.ID, Revision: int64(created.Revision), Payload: payload, UpdatedAt: created.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "missing-hub.git")
	read, err := s.TaskAuthoringRead(context.Background(), "example", created.ID)
	if err != nil || read.ID != created.ID {
		t.Fatalf("Shared task/read failed: %#v %v", read, err)
	}
}

func TestSharedTaskAndADRQueriesDoNotUseHub(t *testing.T) {
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
	task, err := trainv2.NewTask("example", "EXM-TSK901", trainv2.AuthoringDraft{Title: "Shared query", Objective: "Query locally", ADRRelation: model.TaskADRNoRequired}, "planner", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "task", sqlitestore.SharedEntity{ID: task.ID, Revision: int64(task.Revision), Payload: taskPayload, UpdatedAt: task.UpdatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	adr := model.ADR{SchemaVersion: model.SchemaVersion, ID: "EXM-ADR901", ProjectID: "example", Title: "Shared ADR", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: time.Now().UTC()}
	adrPayload, err := json.Marshal(adr)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutSharedProjection(context.Background(), "adr", sqlitestore.SharedEntity{ID: adr.ID, Revision: 1, Payload: adrPayload, UpdatedAt: adr.CreatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "unavailable-hub.git")
	found, err := s.TaskAuthoringFind(context.Background(), task.ID)
	if err != nil || found.ID != task.ID {
		t.Fatalf("Shared task/find failed: %#v %v", found, err)
	}
	listed, err := s.TaskAuthoringList(context.Background(), TaskAuthoringListInput{
		ProjectID: "example",
		Limit:     10,
	})
	if err != nil || len(listed.Tasks) != 1 || listed.Tasks[0].ID != task.ID {
		t.Fatalf("Shared task/list failed: %#v %v", listed, err)
	}
	foundADR, err := s.ADRRead(context.Background(), "example", adr.ID)
	if err != nil || foundADR.ID != adr.ID {
		t.Fatalf("Shared ADR/read failed: %#v %v", foundADR, err)
	}
	listedADRs, err := s.ADRList(context.Background(), "example")
	if err != nil || len(listedADRs) != 1 || listedADRs[0].ID != adr.ID {
		t.Fatalf("Shared ADR/list failed: %#v %v", listedADRs, err)
	}
}
