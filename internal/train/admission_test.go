package train

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAdmissionBuildsOnlyExactReadySnapshots(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	task := model.TaskAuthoring{SchemaVersion: model.TaskAuthoringSchemaVersion, ID: "GTW-TSK179", ProjectID: "gateway", Revision: 1, Title: "Ready task", Objective: "A ready task for admission.", ADRRelation: model.TaskADRNoRequired, Status: model.TaskAuthoringReady, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, ReadySeal: &model.TaskReadySeal{Revision: 1, ReadyBy: "planner", ReadyAt: now}}
	digest, err := model.HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	task.RevisionSHA256 = digest
	task.ReadySeal.RevisionSHA256 = digest
	items, err := ReadyItems([]model.TaskAuthoring{task}, now, 0)
	if err != nil || len(items) != 1 || items[0].TaskRevisionSHA256 != digest {
		t.Fatalf("unexpected admission: %#v %v", items, err)
	}
	task.Status = model.TaskAuthoringPlanned
	if _, err := ReadyItems([]model.TaskAuthoring{task}, now, 0); err == nil {
		t.Fatal("planned task was admitted")
	}
}
