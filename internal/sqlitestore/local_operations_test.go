package sqlitestore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestLocalOperationAllocationIsCompactMonotonicIsolatedAndRestartSafe(t *testing.T) {
	state := t.TempDir()
	db, err := Open(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	first, err := db.AllocateLocalOperation(ctx, "example", "EXM", strings.Repeat("a", 64), "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != "EXM-OPR1" || model.ValidateOperationID(first.OperationID) != nil {
		t.Fatalf("first operation=%#v", first)
	}
	duplicate, err := db.AllocateLocalOperation(ctx, "example", "EXM", strings.Repeat("a", 64), "test", now)
	if err != nil || duplicate.OperationID != first.OperationID {
		t.Fatalf("duplicate allocation=%#v err=%v", duplicate, err)
	}
	other, err := db.AllocateLocalOperation(ctx, "other", "OTH", strings.Repeat("b", 64), "test", now)
	if err != nil || other.OperationID != "OTH-OPR1" {
		t.Fatalf("isolated allocation=%#v err=%v", other, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	afterRestart, err := db.AllocateLocalOperation(ctx, "example", "EXM", strings.Repeat("c", 64), "test", now.Add(time.Second))
	if err != nil || afterRestart.OperationID != "EXM-OPR2" {
		t.Fatalf("restart allocation=%#v err=%v", afterRestart, err)
	}
}

func TestLocalOperationAdmissionCoordinateQueryAndCorruptionValidation(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	inputSHA256 := strings.Repeat("b", 64)
	op, err := db.AllocateLocalOperationWithAdmissionCoordinate(ctx, "example", "EXM", strings.Repeat("a", 64), "agent-prompt", "HOM_EXM_P_coord", inputSHA256, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	matched, err := db.ListLocalOperationsByAdmissionCoordinate(ctx, "example", "agent-prompt", "HOM_EXM_P_coord", inputSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 || matched[0].OperationID != op.OperationID {
		t.Fatalf("matched operations=%#v", matched)
	}
	if err := db.SetLocalOperationAdmissionCoordinate(ctx, op.OperationID, op.ProjectID, "HOM_EXM_P_other", inputSHA256); err == nil {
		t.Fatal("admission coordinate was overwritten")
	}
	if _, err := db.Local.Exec(ctx, `UPDATE local_operations SET admission_input_sha256=? WHERE operation_id=?`, "corrupt", op.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListLocalOperations(ctx, "example"); err == nil {
		t.Fatal("corrupt admission coordinate was accepted")
	}
}

func TestLocalOperationConcurrentAllocationDoesNotCollide(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const count = 24
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			operation, allocationErr := db.AllocateLocalOperation(context.Background(), "example", "EXM", fmt.Sprintf("%064x", i+1), "test", time.Now().UTC())
			if allocationErr != nil {
				errs <- allocationErr
				return
			}
			ids <- operation.OperationID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	got := make([]string, 0, count)
	for id := range ids {
		got = append(got, id)
	}
	if len(got) != count {
		t.Fatalf("allocated %d operations, want %d", len(got), count)
	}
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for i := 0; i < count; i++ {
		want, _ := model.FormatOperationID("EXM", uint64(i+1))
		if !seen[want] {
			t.Fatalf("missing allocated operation %q: %#v", want, got)
		}
	}
}
