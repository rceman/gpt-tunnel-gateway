package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestVerifySingleFlightReusesCompletedReceipt(t *testing.T) {
	s := New(config.Config{StateDir: t.TempDir()})
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	s.gateExecutor = func(ctx context.Context, _ string, names []string) ([]model.CompletionGateResult, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		results := make([]model.CompletionGateResult, 0, len(names))
		for _, name := range names {
			results = append(results, model.CompletionGateResult{ID: name, ExitCode: 0})
		}
		return results, nil
	}
	in := VerifyInput{Root: t.TempDir(), Scope: "full"}
	firstDone := make(chan VerifyReceipt, 1)
	firstErr := make(chan error, 1)
	go func() { receipt, err := s.Verify(context.Background(), in); firstDone <- receipt; firstErr <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first verify did not start")
	}
	secondDone := make(chan VerifyReceipt, 1)
	secondErr := make(chan error, 1)
	go func() { receipt, err := s.Verify(context.Background(), in); secondDone <- receipt; secondErr <- err }()
	select {
	case <-secondDone:
		t.Fatal("identical verify did not wait")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	first := <-firstDone
	if err := <-firstErr; err != nil || first.Status != "completed" {
		t.Fatalf("first verify=%#v err=%v", first, err)
	}
	second := <-secondDone
	if err := <-secondErr; err != nil || second.Status != "completed" || !second.Reused {
		t.Fatalf("second verify=%#v err=%v", second, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("single-flight executed %d times", calls)
	}
}
