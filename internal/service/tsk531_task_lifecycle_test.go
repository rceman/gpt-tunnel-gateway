package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK531SharedTaskLifecycleContract(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	project := s.Config.Projects["example"]
	project.ProjectCode = "EXM"
	s.Config.Projects["example"] = project
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Durability = db
	ctx := context.Background()
	created, _, err := s.taskAuthoringCreateShared(ctx, "tsk531-create", TaskAuthoringCreateInput{
		ProjectID: "example", Title: "Lifecycle Task", Summary: "Searchable compact summary.", Objective: "A bounded objective sentence.",
		AcceptanceCriteria: []string{"history"}, ADRRelation: model.TaskADRNoRequired, CreatedBy: "planner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Summary != "Searchable compact summary." {
		t.Fatalf("create=%#v", created)
	}
	if _, err := s.TaskLifecycleRead(ctx, "example", created.ID, 1); err != nil {
		t.Fatalf("historical revision 1: %v", err)
	}
	if _, _, err := s.taskAuthoringUpdateShared(ctx, "tsk531-noop", TaskAuthoringUpdateInput{ProjectID: "example", TaskID: created.ID, ExpectedRevision: 1, Reason: "   "}); err == nil {
		t.Fatal("whitespace-only reason accepted before no-op rejection")
	}
	newTitle, newSummary := "Updated Lifecycle Task", "Updated searchable summary."
	updated, _, err := s.taskAuthoringUpdateShared(ctx, "tsk531-update", TaskAuthoringUpdateInput{
		ProjectID: "example", TaskID: created.ID, ExpectedRevision: created.Revision, ExpectedRevisionSHA256: created.RevisionSHA256,
		Title: &newTitle, Summary: &newSummary, UpdatedBy: "planner", Reason: "clarify content",
	})
	if err != nil || updated.Revision != 2 || updated.Summary != newSummary {
		t.Fatalf("update=%#v err=%v", updated, err)
	}
	if _, _, err := s.taskAuthoringUpdateShared(ctx, "tsk531-conflict", TaskAuthoringUpdateInput{ProjectID: "example", TaskID: created.ID, ExpectedRevision: 1, Title: &newTitle, UpdatedBy: "planner", Reason: "stale"}); err == nil {
		t.Fatal("stale server-owned CAS update succeeded")
	}
	page, err := s.TaskLifecycleListQuery(ctx, "example", "Updated searchable", "", model.TaskTypeTask, "", false)
	if err != nil || len(page.Tasks) != 1 || page.Tasks[0].Summary != newSummary || page.Tasks[0].Revision != 2 {
		t.Fatalf("summary query page=%#v err=%v", page, err)
	}
	if archived, err := s.TaskLifecycleArchive(ctx, "example", created.ID, "planner", "retire"); err != nil || archived.Revision != 3 || archived.Status != model.TaskAuthoringArchived {
		t.Fatalf("archive=%#v err=%v", archived, err)
	}
	history, err := s.TaskLifecycleHistory(ctx, "example", created.ID, "")
	if err != nil || len(history.Records) != 3 || history.Records[2].MutationKind != "archive" {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	if got := taskArchiveChangedFields(true); len(got) != 2 || got[1] != "ready_seal" {
		t.Fatalf("ready seal change fields=%v", got)
	}
	repeated, err := s.TaskLifecycleArchive(ctx, "example", created.ID, "planner", "different reason")
	if err != nil || repeated.Revision != archivedRevision(history) {
		t.Fatalf("archived repeat=%#v err=%v", repeated, err)
	}
	active, err := s.TaskLifecycleListQuery(ctx, "example", "", "", model.TaskTypeTask, "", false)
	if err != nil || len(active.Tasks) != 0 {
		t.Fatalf("archived Task in default list: %#v err=%v", active, err)
	}
	all, err := s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringArchived, model.TaskTypeTask, "", true)
	if err != nil || len(all.Tasks) != 1 {
		t.Fatalf("archived Task missing from explicit list: %#v err=%v", all, err)
	}
}

func archivedRevision(page sqlitestore.SharedHistoryPage) int {
	return int(page.Records[len(page.Records)-1].Revision)
}
