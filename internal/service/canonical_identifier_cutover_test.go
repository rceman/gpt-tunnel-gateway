package service

import (
	"context"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestCanonicalTaskRunAndADRAllocation(t *testing.T) {
	s, revision, projectHead := testService(t)
	ctx := context.Background()
	identifiers, err := s.ProjectIdentifiersRead(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	if identifiers.NextTaskNumber != 1 {
		t.Fatalf("unexpected initial identifiers: %#v", identifiers)
	}
	task, created, err := s.TaskCreate(ctx, TaskCreateInput{
		ProjectID: "example", Slug: "canonical-cutover", Title: "Canonical task",
		Objective: "Exercise canonical durable allocation.", AcceptanceCriteria: []string{"allocation"},
		CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "EXM-TSK1" || task.Branch != "task/EXM-TSK1-canonical-cutover" || task.BaseRevision != projectHead {
		t.Fatalf("unexpected canonical task: %#v", task)
	}
	if created.Status != "created" {
		t.Fatalf("unexpected create result: %#v", created)
	}
	var counter model.TaskRunCounter
	if err := s.Hub.ReadJSON(ctx, s.taskRunCounterPath(task.ProjectID, task.ID), &counter); err != nil {
		t.Fatal(err)
	}
	if counter.NextRunNumber != 1 {
		t.Fatalf("unexpected initial counter: %#v", counter)
	}
	title, summary, objective := "Canonical task", "Canonical task", "Canonical task execution"
	plan, err := s.PlanUpdate(ctx, PlanUpdateInput{ProjectID: task.ProjectID, Title: &title, Summary: &summary, CurrentObjective: &objective, ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, dispatched, err := s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "EXM-TSK1-RUN1" || run.TaskID != task.ID || dispatched.Status != "awaiting_result" {
		t.Fatalf("unexpected canonical run: %#v %#v", run, dispatched)
	}
	if err := s.Hub.ReadJSON(ctx, s.taskRunCounterPath(task.ProjectID, task.ID), &counter); err != nil {
		t.Fatal(err)
	}
	if counter.NextRunNumber != 2 {
		t.Fatalf("counter was not advanced: %#v", counter)
	}
	adrResult, err := s.ADRCreate(ctx, ADRCreateInput{ADR: model.ADR{ProjectID: task.ProjectID, Title: "Canonical ADR", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences"}, WriteOptions: WriteOptions{ExpectedHubRevision: dispatched.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if adrResult.Status != "created" || len(adrResult.Hub.Paths) != 2 {
		t.Fatalf("unexpected ADR result: %#v", adrResult)
	}
	adrs, err := s.ADRList(ctx, task.ProjectID)
	if err != nil || len(adrs) != 1 || adrs[0].ID != "EXM-ADR1" {
		t.Fatalf("unexpected ADR list: %#v %v", adrs, err)
	}
	if _, _, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: task.ProjectID, Title: "Missing slug", Objective: "This must fail", AcceptanceCriteria: []string{"reject"}, CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: adrResult.Hub.After}}); err == nil || !strings.Contains(err.Error(), "slug is required") {
		t.Fatalf("missing slug was accepted: %v", err)
	}
}
