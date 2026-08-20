package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestSystemAwaitCompletesWithTimingResult(t *testing.T) {
	result, err := awaitDuration(context.Background(), 15*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result.StartedAt.IsZero() || result.FinishedAt.Before(result.StartedAt) || result.ElapsedSeconds < 0.01 {
		t.Fatalf("invalid await timing result: %#v", result)
	}
}

func TestSystemAwaitCancellationIsPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if _, err := awaitDuration(ctx, time.Minute); err != context.Canceled {
		t.Fatalf("await cancellation error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("await cancellation took %s", elapsed)
	}
}

func TestSystemAwaitSchemaIsBoundedAndRegistered(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "await-test", StateDir: t.TempDir()})}
	entry, ok := server.genericActionRegistry(server.tools())["system/await"]
	if !ok {
		t.Fatal("system/await is not registered")
	}
	branches := entry.InputSchema["oneOf"].([]any)
	if len(branches) != 2 {
		t.Fatalf("unexpected await input branches: %#v", entry.InputSchema)
	}
	minutes := branches[0].(map[string]any)["properties"].(map[string]any)["minutes"].(map[string]any)
	seconds := branches[1].(map[string]any)["properties"].(map[string]any)["seconds"].(map[string]any)
	if minutes["minimum"] != minAwaitMinutes || minutes["maximum"] != maxAwaitMinutes {
		t.Fatalf("unexpected await minute bounds: %#v", minutes)
	}
	if seconds["minimum"] != minAwaitSeconds || seconds["maximum"] != maxAwaitSeconds {
		t.Fatalf("unexpected await second bounds: %#v", seconds)
	}
	onComplete := branches[1].(map[string]any)["properties"].(map[string]any)["on_complete"].(map[string]any)["enum"].([]any)
	if len(onComplete) != 1 || onComplete[0] != "agent/status" {
		t.Fatalf("unexpected await continuation allowlist: %#v", onComplete)
	}
	if agentStatusTailLines != 20 {
		t.Fatalf("agent/status tail default changed: %d", agentStatusTailLines)
	}
}

func TestSystemAwaitRejectsMutationContinuation(t *testing.T) {
	if err := validateAwaitContinuation("task/create"); err == nil {
		t.Fatal("mutation action was accepted as an await continuation")
	}
	if err := validateAwaitContinuation("agent/status"); err != nil {
		t.Fatalf("read-only continuation was rejected: %v", err)
	}
}

func TestSystemAwaitSecondsAndMinutesAreMutuallyExclusive(t *testing.T) {
	seconds := 60
	if duration, err := awaitInputDuration(awaitInput{Seconds: &seconds}); err != nil || duration != time.Minute {
		t.Fatalf("seconds input was not accepted: duration=%s err=%v", duration, err)
	}
	minutes := 1
	if _, err := awaitInputDuration(awaitInput{
		Minutes: &minutes,
		Seconds: &seconds,
	}); err == nil {
		t.Fatal("minutes and seconds were accepted together")
	}
}

func TestSystemAwaitRejectsInvalidBoundsThroughGenericDispatch(t *testing.T) {
	server := &Server{Service: service.New(config.Config{GatewayID: "await-test", StateDir: t.TempDir()})}
	entries := server.genericActionRegistry(server.tools())
	for _, minutes := range []int{0, -1, maxAwaitMinutes + 1} {
		result, err := server.genericDispatch(context.Background(), entries, durableSession.Record{}, "system/await", mustJSON(t, map[string]any{"minutes": minutes}))
		if err != nil {
			t.Fatalf("minutes=%d dispatch error: %v", minutes, err)
		}
		encoded, _ := json.Marshal(result)
		if !containsAny(string(encoded), "minutes", "between", "matching output shape") {
			t.Fatalf("minutes=%d was not rejected by generic dispatch: %s", minutes, encoded)
		}
	}
}

func containsAny(value string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(value, part) {
			return true
		}
	}
	return false
}
