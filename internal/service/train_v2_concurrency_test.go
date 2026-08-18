package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTrainV2IntegrationWaiterRefreshesTargetAfterLockRelease(t *testing.T) {
	s, _, initialHead := testServiceWithoutIdentifiers(t)
	project := s.Config.Projects["example"]
	before, exists, err := s.trainV2IntegrationTargetHead(context.Background(), project, "main")
	if err != nil || !exists || before != initialHead {
		t.Fatalf("read initial integration target: head=%q exists=%v err=%v", before, exists, err)
	}
	first, err := s.acquireTrainV2IntegrationLock(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}

	type targetResult struct {
		head   string
		exists bool
		err    error
	}
	result := make(chan targetResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		second, err := s.acquireTrainV2IntegrationLock(ctx, "example")
		if err != nil {
			result <- targetResult{err: err}
			return
		}
		defer second.Release()
		head, exists, err := s.trainV2IntegrationTargetHead(ctx, project, "main")
		result <- targetResult{
			head:   head,
			exists: exists,
			err:    err,
		}
	}()
	select {
	case value := <-result:
		t.Fatalf("second integration owner bypassed the first: %#v", value)
	case <-time.After(30 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(project.Root, "after-lock.txt"), []byte("after lock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, project.Root, "add", "after-lock.txt")
	testutil.Git(t, project.Root, "commit", "-m", "advance integration target")
	testutil.Git(t, project.Root, "push", "origin", "main")
	after := testutil.Git(t, project.Root, "rev-parse", "HEAD")
	after = trimTestOutput(after)
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	value := <-result
	if value.err != nil || !value.exists {
		t.Fatalf("queued integration target read failed: %#v", value)
	}
	if value.head != after || value.head == before {
		t.Fatalf("queued integration did not re-read updated main state: before=%s after=%s observed=%s", before, after, value.head)
	}
	other, err := s.acquireTrainV2IntegrationLock(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
}

func trimTestOutput(value string) string {
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r' || value[len(value)-1] == ' ') {
		value = value[:len(value)-1]
	}
	return value
}

func TestTrainV2IntegrationLockFailsClosedOnLockDirectoryError(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	statePath := filepath.Join(t.TempDir(), "state-file")
	if err := os.WriteFile(statePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Config.StateDir = statePath
	if _, err := s.acquireTrainV2IntegrationLock(context.Background(), "example"); err == nil {
		t.Fatal("lock directory failure was retried instead of returned")
	}
}

func TestTrainV2IntegrationLockUsesDistinctProjectNames(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	first, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-v2-integration-example")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	second, err := s.acquireTrainV2IntegrationLock(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
}
