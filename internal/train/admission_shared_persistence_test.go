package train

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestConcurrentSharedTrainAddsReserveTaskInOneTransaction(t *testing.T) {
	db, err := sqlitestore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	first := model.TrainV2{SchemaVersion: model.TrainV2SchemaVersion, ID: "GTW-TRN1", ProjectID: "gateway", Revision: 1, Status: model.TrainV2Planned, CreatedBy: "planner", CreatedAt: now, UpdatedAt: now, Items: []model.TrainV2Item{{Position: 0, TaskID: "GTW-TSK0", Status: model.TrainV2ItemQueued}}}
	second := first
	second.ID = "GTW-TRN2"
	second.Items = append([]model.TrainV2Item(nil), first.Items...)
	second.Items[0].TaskID = "GTW-TSK00"
	for _, train := range []model.TrainV2{first, second} {
		if err := CommitSharedTrain(context.Background(), StartDependencies{Shared: db, OperationID: "seed-" + train.ID}, train, "train-v2-create", now); err != nil {
			t.Fatal(err)
		}
	}

	updatedFirst := first
	updatedFirst.Revision = 2
	updatedFirst.UpdatedAt = now.Add(time.Minute)
	updatedSecond := second
	updatedSecond.Revision = 2
	updatedSecond.UpdatedAt = now.Add(time.Minute)
	for _, train := range []*model.TrainV2{&updatedFirst, &updatedSecond} {
		train.Items = append(train.Items, model.TrainV2Item{Position: 1, TaskID: "GTW-TSK1", Status: model.TrainV2ItemQueued})
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, train := range []*model.TrainV2{&updatedFirst, &updatedSecond} {
		train := train
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- CommitSharedTrain(context.Background(), StartDependencies{Shared: db, OperationID: "add-" + train.ID}, *train, "train-v2-add", train.UpdatedAt)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, failures int
	for err := range errs {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent Train admissions successes=%d failures=%d", successes, failures)
	}
	rows, err := db.Shared.Query(context.Background(), `SELECT project_id,task_id,train_id FROM shared_train_task_admissions`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 3 {
		t.Fatalf("unexpected admission rows=%#v", rows.Rows)
	}
	var sharedTaskOwners int
	for _, row := range rows.Rows {
		if row[0] == "gateway" && row[1] == "GTW-TSK1" {
			sharedTaskOwners++
		}
	}
	if sharedTaskOwners != 1 {
		t.Fatalf("concurrent Task admission was not unique: rows=%#v", rows.Rows)
	}
}
