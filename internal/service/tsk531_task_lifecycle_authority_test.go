package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/pagination"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func tsk531DrainOutbox(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	entries, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := s.publishSharedOutboxEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
		if err := s.Durability.MarkOutboxPublished(ctx, entry.ID, s.durableNow()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTSK531TaskArchiveSurvivesDegradedHub(t *testing.T) {
	s, db := tsk585Setup(t)
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk531-degraded-hub", "Degraded hub Task")
	tsk531DrainOutbox(t, s)
	healthy := s.Hub.Config.Hub.RepositoryURL
	s.Hub.Config.Hub.RepositoryURL = filepath.Join(t.TempDir(), "missing.git")
	archived, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "retire under degraded hub")
	if err != nil {
		t.Fatalf("degraded Hub blocked the durable archive: %v", err)
	}
	if archived.Status != model.TaskAuthoringArchived || archived.Revision != task.Revision {
		t.Fatalf("degraded Hub archive=%#v want revision %d", archived, task.Revision)
	}
	rows, err := db.Shared.Query(ctx, `SELECT event_kind,from_status,to_status FROM shared_lifecycle_events WHERE entity_id=?`, task.ID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != sqlitestore.SharedLifecycleEventKindArchive {
		t.Fatalf("degraded Hub lifecycle authority=%#v err=%v", rows.Rows, err)
	}
	entries, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(entries) != 1 {
		t.Fatalf("degraded Hub pending outbox=%#v err=%v", entries, err)
	}
	if err := s.publishSharedOutboxEntry(ctx, entries[0]); err == nil {
		t.Fatal("degraded Hub publication unexpectedly succeeded")
	}
	if err := s.Durability.MarkOutboxRetry(ctx, entries[0].ID, time.Now().UTC().Add(-time.Second), errSharedOutboxNoop); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(retry) != 1 || retry[0].ID != entries[0].ID {
		t.Fatalf("degraded Hub retry entry=%#v err=%v", retry, err)
	}
	s.Hub.Config.Hub.RepositoryURL = healthy
	if err := s.publishSharedOutboxEntry(ctx, retry[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Durability.MarkOutboxPublished(ctx, retry[0].ID, s.durableNow()); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Durability.PendingOutbox(ctx, 32)
	if err != nil || len(pending) != 0 {
		t.Fatalf("recovered outbox=%#v err=%v", pending, err)
	}
	current, err := s.TaskLifecycleRead(ctx, "example", task.ID, archived.Revision)
	if err != nil || current.Status != model.TaskAuthoringPlanned {
		t.Fatalf("archived content revision must stay immutable: %#v err=%v", current, err)
	}
}

func TestTSK531TaskHistoryMultipageWalksSharedCursor(t *testing.T) {
	s, db := tsk585Setup(t)
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk531-multipage", "Multipage history Task")
	for i := 0; i < 3; i++ {
		objective := fmt.Sprintf("Revised objective %d.", i)
		updated, _, err := s.TaskLifecycleUpdate(ctx, TaskAuthoringUpdateInput{
			ProjectID:        "example",
			TaskID:           task.ID,
			ExpectedRevision: task.Revision,
			Objective:        &objective,
			Reason:           "revise",
			UpdatedBy:        "planner",
		})
		if err != nil {
			t.Fatal(err)
		}
		task = updated
	}
	if _, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "retire"); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	revisions := map[int64]int{}
	after := sqlitestore.SharedLifecycleHistoryCursor{}
	pages := 0
	for {
		if pages > 16 {
			t.Fatal("history pagination did not terminate")
		}
		storePage, err := db.ListSharedLifecycleHistoryPage(ctx, "task", "example", task.ID, after, 1)
		if err != nil || len(storePage.Records) != 1 {
			t.Fatalf("store page %d=%#v err=%v", pages, storePage, err)
		}
		pages++
		revisions[storePage.Records[0].Revision]++
		kinds[storePage.Records[0].MutationKind]++
		if !storePage.HasMore {
			if storePage.NextCursor != "" {
				t.Fatalf("final store page=%#v", storePage)
			}
			break
		}
		if pagination.EncodeOpaqueKeyset("task-history:example:"+task.ID, storePage.NextCursor) == "" {
			t.Fatalf("page %d continuation cursor must encode", pages)
		}
		decoded, err := sqlitestore.DecodeSharedLifecycleHistoryCursor(storePage.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		after = decoded
	}
	if pages != 5 || len(revisions) != 4 || revisions[1] != 1 || revisions[2] != 1 || revisions[3] != 1 || revisions[4] != 2 ||
		kinds["create"] != 1 || kinds["update"] != 3 || kinds[sqlitestore.SharedLifecycleEventKindArchive] != 1 {
		t.Fatalf("history walk pages=%d revisions=%#v kinds=%#v", pages, revisions, kinds)
	}
	servicePage, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "")
	if err != nil || len(servicePage.Records) != 5 || servicePage.HasMore {
		t.Fatalf("service history page=%#v err=%v", servicePage, err)
	}
	scope := pagination.EncodeOpaqueKeyset("task-history:example:EXM-TSK9999", `{"recorded_at":"2026-09-17T10:00:00Z","source":0,"id":1}`)
	if _, err := s.TaskLifecycleHistory(ctx, "example", task.ID, scope); err == nil {
		t.Fatal("cross-Task history cursor must fail closed")
	}
	if _, err := s.TaskLifecycleHistory(ctx, "example", task.ID, "not-a-cursor"); err == nil {
		t.Fatal("invalid history cursor must fail closed")
	}
}

func TestTSK531TaskArchiveQueryAndListAuthority(t *testing.T) {
	s, _ := tsk585Setup(t)
	ctx := context.Background()
	active := tsk585Task(t, s, "tsk531-list-active", "Active authority Task")
	archived := tsk585Task(t, s, "tsk531-list-archived", "Archived authority Task")
	if _, err := s.TaskLifecycleArchive(ctx, "example", archived.ID, "planner", "retire"); err != nil {
		t.Fatal(err)
	}
	page, err := s.TaskLifecycleListQuery(ctx, "example", "", "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Tasks {
		if item.ID == archived.ID {
			t.Fatalf("archived Task leaked into the default list: %#v", item)
		}
	}
	included, err := s.TaskLifecycleListQuery(ctx, "example", "", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range included.Tasks {
		if item.ID == archived.ID {
			found = true
			if item.Status != model.TaskAuthoringArchived {
				t.Fatalf("included archived Task=%#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("archived Task missing from the include_archived list: %#v", included.Tasks)
	}
	queried, err := s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringArchived, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(queried.Tasks) != 1 || queried.Tasks[0].ID != archived.ID {
		t.Fatalf("archived status query=%#v", queried.Tasks)
	}
	search, err := s.TaskLifecycleListQuery(ctx, "example", "Active authority", "", "", "", false)
	if err != nil || len(search.Tasks) != 1 || search.Tasks[0].ID != active.ID {
		t.Fatalf("text query=%#v err=%v", search.Tasks, err)
	}
	read, err := s.TaskLifecycleRead(ctx, "example", archived.ID, archived.Revision)
	if err != nil || read.Status != model.TaskAuthoringPlanned || read.Revision != archived.Revision || read.RevisionSHA256 != archived.RevisionSHA256 {
		t.Fatalf("archived historical read=%#v err=%v", read, err)
	}
	current, err := s.TaskLifecycleListQuery(ctx, "example", "", model.TaskAuthoringArchived, "", "", true)
	if err != nil || len(current.Tasks) != 1 || current.Tasks[0].Status != model.TaskAuthoringArchived {
		t.Fatalf("archived current projection=%#v err=%v", current.Tasks, err)
	}
}

func TestTSK531TaskLifecycleAuthoritySurvivesRestart(t *testing.T) {
	s, db := tsk585Setup(t)
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk531-restart", "Restart authority Task")
	if _, err := s.TaskLifecycleArchive(ctx, "example", task.ID, "planner", "retire"); err != nil {
		t.Fatal(err)
	}
	state := s.Config.StateDir
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	gone, err := reopened.Shared.Query(ctx, `SELECT name FROM sqlite_master WHERE name='shared_task_lifecycle_events'`)
	if err != nil || len(gone.Rows) != 0 {
		t.Fatalf("restart restored the old task lifecycle table: rows=%#v err=%v", gone.Rows, err)
	}
	rows, err := reopened.Shared.Query(ctx, `SELECT entity_type,event_kind,from_status,to_status FROM shared_lifecycle_events WHERE entity_id=?`, task.ID)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "task" || rows.Rows[0][1] != sqlitestore.SharedLifecycleEventKindArchive {
		t.Fatalf("restart lifecycle authority=%#v err=%v", rows.Rows, err)
	}
	outbox, err := reopened.Shared.Query(ctx, `SELECT COUNT(*) FROM hub_outbox WHERE entity_id=? AND kind='task-archive'`, task.ID)
	if err != nil || outbox.Rows[0][0] != int64(1) {
		t.Fatalf("restart outbox=%#v err=%v", outbox.Rows, err)
	}
	events, err := reopened.ListTaskLifecycleEvents(ctx, "example", task.ID, 64)
	if err != nil || len(events) != 1 || events[0].EventKind != sqlitestore.TaskLifecycleEventKindArchive || events[0].ToStatus != model.TaskAuthoringArchived {
		t.Fatalf("restart task lifecycle read=%#v err=%v", events, err)
	}
	history, err := reopened.ListSharedLifecycleHistoryPage(ctx, "task", "example", task.ID, sqlitestore.SharedLifecycleHistoryCursor{}, 64)
	if err != nil || len(history.Records) != 2 {
		t.Fatalf("restart shared history=%#v err=%v", history, err)
	}
}

func TestTSK531TaskCompletionKeepsExecutionOrchestrationSeparate(t *testing.T) {
	s, db := tsk585Setup(t)
	ctx := context.Background()
	task := tsk585Task(t, s, "tsk531-separation", "Execution separation Task")
	tsk585Dispatch(t, s, task.ID)
	tsk585DriveToVerification(t, s, task.ID)
	operation := tsk585VerifyTask(t, s, task.ID)
	if operation.Status != "completed" || operation.Error != "" {
		t.Fatalf("verification operation=%#v", operation)
	}
	state, found, err := db.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found {
		t.Fatalf("execution state=%#v found=%v err=%v", state, found, err)
	}
	rows, err := db.Shared.Query(ctx, `SELECT COUNT(*) FROM shared_lifecycle_events WHERE entity_id=?`, task.ID)
	if err != nil || rows.Rows[0][0] != int64(0) {
		t.Fatalf("execution orchestration wrote lifecycle authority: rows=%#v err=%v", rows.Rows, err)
	}
	if state.Status == model.TaskExecutionDone {
		t.Fatalf("execution orchestration reached a completion state without a lifecycle event: %#v", state)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, found, err := reopened.ReadTaskExecutionState(ctx, "example", task.ID)
	if err != nil || !found || after.ExecutionRevision != state.ExecutionRevision || after.Status != state.Status {
		t.Fatalf("execution state after restart=%#v found=%v err=%v", after, found, err)
	}
}
