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
	properties := entry.InputSchema["properties"].(map[string]any)
	minutes := properties["minutes"].(map[string]any)
	if minutes["minimum"] != minAwaitMinutes || minutes["maximum"] != maxAwaitMinutes {
		t.Fatalf("unexpected await bounds: %#v", minutes)
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
		if !containsAny(string(encoded), "minutes", "between") {
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
