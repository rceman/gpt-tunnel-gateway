package service

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
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
		OperationClass: "implementation", CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: revision},
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
	readADR, err := s.ADRRead(ctx, task.ProjectID, adrs[0].ID)
	if err != nil || readADR.ID != adrs[0].ID {
		t.Fatalf("compact ADR read did not match list: %#v %v", readADR, err)
	}
	for _, invalid := range []string{"GRP-ADR1", "EXM-A1", "EXM-ADR01", "EXM-ADR1-extra"} {
		if _, err := s.ADRRead(ctx, task.ProjectID, invalid); err == nil {
			t.Fatalf("accepted invalid compact ADR read ID %q", invalid)
		}
	}
	if _, _, err := s.TaskCreate(ctx, TaskCreateInput{ProjectID: task.ProjectID, Title: "Missing slug", Objective: "This must fail", AcceptanceCriteria: []string{"reject"}, CreatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: adrResult.Hub.After}}); err == nil || !strings.Contains(err.Error(), "slug is required") {
		t.Fatalf("missing slug was accepted: %v", err)
	}
}

func snapshotCanonicalHubPaths(t *testing.T, s *Service, paths []string) map[string][]byte {
	t.Helper()
	snapshot := make(map[string][]byte, len(paths))
	for _, path := range paths {
		data, err := s.Hub.ReadFile(context.Background(), path)
		if err == nil {
			snapshot[path] = append([]byte(nil), data...)
			continue
		}
		if !IsNotFound(err) {
			t.Fatalf("snapshot %s: %v", path, err)
		}
		snapshot[path] = nil
	}
	return snapshot
}

func requireCanonicalHubPathsUnchanged(t *testing.T, s *Service, snapshot map[string][]byte) {
	t.Helper()
	for path, want := range snapshot {
		got, err := s.Hub.ReadFile(context.Background(), path)
		if want == nil {
			if err == nil {
				t.Fatalf("unexpected new hub path %s", path)
			}
			if !IsNotFound(err) {
				t.Fatalf("read absent hub path %s: %v", path, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("read unchanged hub path %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("hub path %s changed", path)
		}
	}
}

func TestTaskSupersedeRejectsEveryAllocatedTargetCollision(t *testing.T) {
	cases := []struct {
		name string
		path func(*Service, string) string
	}{
		{name: "task", path: func(s *Service, id string) string { return s.taskPath("example", id) }},
		{name: "state", path: func(s *Service, id string) string { return s.taskStatePath("example", id) }},
		{name: "run-counter", path: func(s *Service, id string) string { return s.taskRunCounterPath("example", id) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, revision, _ := testService(t)
			ctx := context.Background()
			old, created, err := s.TaskCreate(ctx, TaskCreateInput{
				ProjectID: "example", Slug: "supersede-source", Title: "Source task", Objective: "Source task for collision proof.",
				AcceptanceCriteria: []string{"rollback"}, OperationClass: "implementation", CreatedBy: "test",
				WriteOptions: WriteOptions{ExpectedHubRevision: revision},
			})
			if err != nil {
				t.Fatal(err)
			}
			targetID := "EXM-TSK2"
			targetPath := tc.path(s, targetID)
			sentinel := []byte("pre-existing-" + tc.name + "\n")
			collision, err := s.Hub.Transact(ctx, created.Hub.After, "test: install supersede collision", func(worktree string) ([]string, error) {
				return []string{targetPath}, hub.WriteText(worktree, targetPath, string(sentinel))
			})
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{
				s.taskPath("example", old.ID),
				s.taskStatePath("example", old.ID),
				s.projectIdentifiersPath("example"),
				s.taskPath("example", targetID),
				s.taskStatePath("example", targetID),
				s.taskRunCounterPath("example", targetID),
			}
			before := snapshotCanonicalHubPaths(t, s, paths)
			listBefore, err := s.Hub.List(ctx, s.projectPrefix("example")+"/tasks", ".json")
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.TaskSupersede(ctx, old.ID, TaskCreateInput{
				ProjectID: "example", Slug: "replacement", Title: "Replacement", Objective: "Must not overwrite collision target.",
				AcceptanceCriteria: []string{"rollback"}, OperationClass: "implementation", CreatedBy: "test",
				WriteOptions: WriteOptions{ExpectedHubRevision: collision.After},
			})
			if err == nil || !strings.Contains(err.Error(), targetPath) {
				t.Fatalf("collision was not rejected with target path: %v", err)
			}
			if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != collision.After {
				t.Fatalf("rejected supersede changed hub revision: got=%s err=%v want=%s", got, err, collision.After)
			}
			requireCanonicalHubPathsUnchanged(t, s, before)
			listAfter, err := s.Hub.List(ctx, s.projectPrefix("example")+"/tasks", ".json")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(listAfter, listBefore) {
				t.Fatalf("supersede created or removed sibling task paths: before=%v after=%v", listBefore, listAfter)
			}
			identifiers, err := s.ProjectIdentifiersRead(ctx, "example")
			if err != nil {
				t.Fatal(err)
			}
			if identifiers.NextTaskNumber != 2 {
				t.Fatalf("collision consumed task number: %#v", identifiers)
			}
		})
	}
}

func TestTaskDispatchRejectsTaskRunCounterIdentityMismatch(t *testing.T) {
	cases := []struct {
		name      string
		projectID string
		taskID    string
	}{
		{name: "project", projectID: "other", taskID: "EXM-TSK1"},
		{name: "task", projectID: "example", taskID: "EXM-TSK999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, revision, _ := testService(t)
			ctx := context.Background()
			task, created, err := s.TaskCreate(ctx, TaskCreateInput{
				ProjectID: "example", Slug: "dispatch-counter", Title: "Dispatch task", Objective: "Reject mismatched run counter.",
				AcceptanceCriteria: []string{"rollback"}, OperationClass: "implementation", CreatedBy: "test",
				WriteOptions: WriteOptions{ExpectedHubRevision: revision},
			})
			if err != nil {
				t.Fatal(err)
			}
			title, summary, objective := "Dispatch", "Dispatch", "Dispatch"
			plan, err := s.PlanUpdate(ctx, PlanUpdateInput{
				ProjectID: task.ProjectID, Title: &title, Summary: &summary, CurrentObjective: &objective,
				ActiveTaskID: &task.ID, UpdatedBy: "test", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After},
			})
			if err != nil {
				t.Fatal(err)
			}
			counterPath := s.taskRunCounterPath(task.ProjectID, task.ID)
			counter := model.TaskRunCounter{SchemaVersion: model.SchemaVersion, ProjectID: tc.projectID, TaskID: tc.taskID, NextRunNumber: 1}
			counterTx, err := s.Hub.Transact(ctx, plan.Hub.After, "test: install mismatched task run counter", func(worktree string) ([]string, error) {
				return []string{counterPath}, hub.WriteJSON(worktree, counterPath, counter)
			})
			if err != nil {
				t.Fatal(err)
			}
			runID := "EXM-TSK1-RUN1"
			paths := []string{counterPath, s.taskStatePath(task.ProjectID, task.ID), s.planPath(task.ProjectID), s.projectIdentifiersPath(task.ProjectID), s.runPath(task.ProjectID, runID)}
			before := snapshotCanonicalHubPaths(t, s, paths)
			_, _, err = s.TaskDispatch(ctx, DispatchInput{TaskID: task.ID, WriteOptions: WriteOptions{ExpectedHubRevision: counterTx.After}})
			if err == nil || !strings.Contains(err.Error(), "task run counter identity mismatch") {
				t.Fatalf("counter identity mismatch was not rejected: %v", err)
			}
			if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != counterTx.After {
				t.Fatalf("rejected dispatch changed hub revision: got=%s err=%v want=%s", got, err, counterTx.After)
			}
			requireCanonicalHubPathsUnchanged(t, s, before)
			if _, err := os.Stat(s.localRunDir(runID)); err == nil || !os.IsNotExist(err) {
				t.Fatalf("rejected dispatch created local run directory: %v", err)
			}
		})
	}
}
