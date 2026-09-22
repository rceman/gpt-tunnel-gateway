package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestTSK661CurrentRevisionReadFallbackAndFrozenAdmission(t *testing.T) {
	t.Run("current revision fallback reaches frozen execution admission", func(t *testing.T) {
		s, db := tsk585Setup(t)
		defer db.Close()
		ctx := context.Background()
		task := tsk585Task(t, s, "tsk661-legacy-current", "Legacy current Task")
		title := "Legacy current Task revised"
		updated, _, err := s.taskAuthoringUpdateShared(ctx, "tsk661-legacy-update", TaskAuthoringUpdateInput{
			ProjectID:              "example",
			TaskID:                 task.ID,
			ExpectedRevision:       task.Revision,
			ExpectedRevisionSHA256: task.RevisionSHA256,
			Title:                  &title,
			UpdatedBy:              "planner",
			Reason:                 "legacy current revision fixture",
		})
		if err != nil || updated.Revision != 2 {
			t.Fatalf("rev2 update=%#v err=%v", updated, err)
		}
		if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? AND revision=?`, task.ID, updated.Revision); err != nil {
			t.Fatal(err)
		}

		read, err := s.TaskLifecycleRead(ctx, "example", task.ID, updated.Revision)
		if err != nil || read.Revision != updated.Revision || read.Title != updated.Title || read.RevisionSHA256 != updated.RevisionSHA256 {
			t.Fatalf("explicit current read=%#v err=%v", read, err)
		}
		rows, err := db.Shared.Query(ctx, `SELECT revision FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? ORDER BY revision`, task.ID)
		if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != int64(1) {
			t.Fatalf("fallback read changed history rows=%#v err=%v", rows.Rows, err)
		}

		dispatched, err := s.TaskExecutionDispatch(ctx, TaskExecutionDispatchInput{
			ProjectID: "example",
			Key:       task.ID,
		})
		if err != nil || dispatched.Status != model.TaskExecutionDispatched {
			t.Fatalf("dispatch after fallback read=%#v err=%v", dispatched, err)
		}
		blocked, err := s.TaskExecutionBlock(ctx, TaskExecutionBlockInput{
			ProjectID: "example",
			Key:       task.ID,
			Reason:    "pause legacy execution",
		})
		if err != nil || blocked.Status != model.TaskExecutionBlocked {
			t.Fatalf("frozen execution admission=%#v err=%v", blocked, err)
		}
	})

	t.Run("older missing revision remains fail closed after current advances", func(t *testing.T) {
		s, db := tsk585Setup(t)
		defer db.Close()
		ctx := context.Background()
		task := tsk585Task(t, s, "tsk661-legacy-advance", "Legacy advancing Task")
		title := "Legacy rev2"
		rev2, _, err := s.taskAuthoringUpdateShared(ctx, "tsk661-legacy-advance-2", TaskAuthoringUpdateInput{
			ProjectID:              "example",
			TaskID:                 task.ID,
			ExpectedRevision:       task.Revision,
			ExpectedRevisionSHA256: task.RevisionSHA256,
			Title:                  &title,
			UpdatedBy:              "planner",
			Reason:                 "create legacy gap",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(ctx, `DELETE FROM shared_entity_revisions WHERE entity_type='task' AND entity_id=? AND revision=?`, task.ID, rev2.Revision); err != nil {
			t.Fatal(err)
		}
		title = "Current rev3"
		rev3, _, err := s.taskAuthoringUpdateShared(ctx, "tsk661-legacy-advance-3", TaskAuthoringUpdateInput{
			ProjectID:              "example",
			TaskID:                 task.ID,
			ExpectedRevision:       rev2.Revision,
			ExpectedRevisionSHA256: rev2.RevisionSHA256,
			Title:                  &title,
			UpdatedBy:              "planner",
			Reason:                 "advance current revision",
		})
		if err != nil || rev3.Revision != 3 {
			t.Fatalf("rev3 update=%#v err=%v", rev3, err)
		}
		if _, err := s.TaskLifecycleRead(ctx, "example", task.ID, rev2.Revision); err == nil {
			t.Fatal("missing older revision was served from the live current payload")
		}
		if current, err := s.TaskLifecycleRead(ctx, "example", task.ID, rev3.Revision); err != nil || current.Title != rev3.Title {
			t.Fatalf("current rev3 read=%#v err=%v", current, err)
		}
		if _, err := s.TaskLifecycleRead(ctx, "example", task.ID, rev3.Revision+1); err == nil {
			t.Fatal("revision newer than current was accepted")
		}
	})

	t.Run("complete history remains exact", func(t *testing.T) {
		s, db := tsk585Setup(t)
		defer db.Close()
		ctx := context.Background()
		task := tsk585Task(t, s, "tsk661-complete-history", "Complete history Task")
		read, err := s.TaskLifecycleRead(ctx, "example", task.ID, task.Revision)
		if err != nil || read.ID != task.ID || read.Revision != task.Revision || read.Title != task.Title || read.RevisionSHA256 != task.RevisionSHA256 {
			t.Fatalf("complete-history read=%#v want=%#v err=%v", read, task, err)
		}
	})
}
