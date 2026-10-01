package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func tsk681FastRetry(t *testing.T) {
	t.Helper()
	oldTimeout, oldDelays := postReadyHubAttemptTimeout, postReadyHubRetryDelays
	postReadyHubAttemptTimeout = 300 * time.Millisecond
	postReadyHubRetryDelays = []time.Duration{10 * time.Millisecond}
	t.Cleanup(func() {
		postReadyHubAttemptTimeout = oldTimeout
		postReadyHubRetryDelays = oldDelays
	})
}

// TestPostReadyHubSyncLoopRetriesTransientStateCheck reproduces the TSK678
// incident shape: a transient stateCheck failure (timeout/lock/fetch class)
// must not permanently end convergence — the next bounded attempt retries
// the whole check and can reach readiness without a daemon restart.
func TestPostReadyHubSyncLoopRetriesTransientStateCheck(t *testing.T) {
	tsk681FastRetry(t)
	var mu sync.Mutex
	var phases []string
	collect := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, name)
	}
	bootstrapCalls, stateCheckCalls := 0, 0
	err := postReadyHubSyncLoop(context.Background(), collect, func(context.Context) error {
		bootstrapCalls++
		return nil
	}, func(ctx context.Context) error {
		stateCheckCalls++
		if stateCheckCalls == 1 {
			<-ctx.Done()
			return fmt.Errorf("state check timed out waiting for hub lock: %w", ctx.Err())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transient stateCheck failure terminated convergence: %v", err)
	}
	if bootstrapCalls != 2 || stateCheckCalls != 2 {
		t.Fatalf("bootstrap=%d stateCheck=%d, want a full second attempt", bootstrapCalls, stateCheckCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"POST_READY_HUB_ATTEMPT_1", "POST_READY_HUB_RETRY_WAIT_1", "POST_READY_HUB_ATTEMPT_2"} {
		found := false
		for _, name := range phases {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing attempt evidence %q in phases=%v", want, phases)
		}
	}
}

// TestPostReadyHubSyncLoopTerminalFailuresStop proves permanent semantic
// outcomes fail closed instead of looping: an invalid durable state check
// and an irreconcilable retirement conflict both end the loop immediately.
func TestPostReadyHubSyncLoopTerminalFailuresStop(t *testing.T) {
	tsk681FastRetry(t)
	t.Run("invalid durable state", func(t *testing.T) {
		attempts := 0
		degraded := false
		err := postReadyHubSyncLoop(context.Background(), func(name string) {
			if name == "HUB_SYNC_DEGRADED" {
				degraded = true
			}
		}, func(context.Context) error {
			return nil
		}, func(context.Context) error {
			attempts++
			return terminalHubSyncError{err: errors.New("durable state validation failed: X")}
		})
		if err == nil || attempts != 1 || !degraded {
			t.Fatalf("terminal state check: err=%v attempts=%d degraded=%v", err, attempts, degraded)
		}
	})
	t.Run("retirement conflict", func(t *testing.T) {
		attempts := 0
		err := postReadyHubSyncLoop(context.Background(), func(string) {}, func(context.Context) error {
			attempts++
			return sqlitestore.ErrProjectRetirementConflict
		}, func(context.Context) error {
			t.Fatal("state check ran after terminal bootstrap conflict")
			return nil
		})
		if !errors.Is(err, sqlitestore.ErrProjectRetirementConflict) || attempts != 1 {
			t.Fatalf("terminal bootstrap conflict: err=%v attempts=%d", err, attempts)
		}
	})
}

// TestPostReadyHubSyncConvergesAfterRepositoryLockContention reproduces the
// incident regression end to end: an outbox-style writer holds the
// hub-repository lock while post-ready convergence starts; attempts expire
// retryably until the contention clears, then the loop reaches
// HUB_SYNC_READY without a daemon restart.
func TestPostReadyHubSyncConvergesAfterRepositoryLockContention(t *testing.T) {
	bare, _, _ := testutil.RepoWithBareRemote(t)
	c := testBootstrapConfig(t)
	c.Hub.RepositoryURL = bare
	c.Hub.Branch = "main"

	helper := exec.Command(os.Args[0], "-test.run", "^TestHoldHubRepositoryLockHelper$")
	helper.Env = append(os.Environ(), "GTW_HOLD_HUB_LOCK=1", "GTW_HOLD_HUB_LOCK_DIR="+c.StateDir)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("lock helper readiness=%q err=%v", line, err)
	}

	runtime, err := bootstrapGateway(c, nil, writeBootstrapConfig(t, c))
	if err != nil {
		t.Fatal(err)
	}
	defer closeBootstrap(t, runtime)
	tsk681FastRetry(t)

	var mu sync.Mutex
	var phases []string
	collect := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		phases = append(phases, name)
	}
	done := make(chan error, 1)
	go func() {
		done <- postReadyHubSyncLoop(context.Background(), collect,
			func(attemptCtx context.Context) error {
				if err := postReadyHubEnsureAndReconcileContext(runtime.service, attemptCtx, collect); err != nil {
					return err
				}
				runtime.service.StartSharedOutboxWorker()
				return nil
			},
			func(attemptCtx context.Context) error {
				return postReadyHubStateCheckContext(runtime.service, attemptCtx, collect)
			},
		)
	}()

	// Let contention force at least one expired attempt before clearing it.
	time.Sleep(700 * time.Millisecond)
	_ = stdin.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("convergence failed after lock cleared: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("convergence did not complete after lock cleared")
	}

	mu.Lock()
	defer mu.Unlock()
	attempts, ready := 0, false
	for _, name := range phases {
		if strings.HasPrefix(name, "POST_READY_HUB_ATTEMPT_") {
			attempts++
		}
		if name == "HUB_SYNC_READY" {
			ready = true
		}
	}
	if !ready || attempts < 2 {
		t.Fatalf("attempts=%d ready=%v phases=%v", attempts, ready, phases)
	}
}
