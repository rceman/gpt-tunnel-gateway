package mcp

import (
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
)

func TestGenericTimingUsesSparseMillisecondThreshold(t *testing.T) {
	fast := genericActionSuccess(map[string]any{"value": "ok"})
	addSparseExecTime(fast, 999*time.Millisecond)
	if _, found := fast["exec_time_ms"]; found {
		t.Fatalf("fast result exposed timing: %#v", fast)
	}
	slow := genericActionError("test/action", "failed")
	addSparseExecTime(slow, time.Second)
	if got, ok := slow["exec_time_ms"].(int64); !ok || got != 1000 {
		t.Fatalf("slow result timing=%#v", slow)
	}

	items := []map[string]any{
		genericBatchResult("fast/action", fast),
		genericBatchResult("slow/action", slow),
	}
	if _, ok := items[0]["exec_time_ms"]; ok {
		t.Fatalf("fast batch item exposed timing: %#v", items[0])
	}
	if got, ok := items[1]["exec_time_ms"].(int64); !ok || got != 1000 {
		t.Fatalf("slow batch item timing=%#v", items[1])
	}
	batch := map[string]any{"results": items}
	addSparseExecTime(batch, 1001*time.Millisecond)
	if got, ok := batch["exec_time_ms"].(int64); !ok || got != 1001 {
		t.Fatalf("aggregate batch timing=%#v", batch)
	}
}

func TestRuntimeTimingIsLocalTelemetryOnly(t *testing.T) {
	event := runtime_log.Event{Timestamp: time.Now().UTC(), Level: "info", Component: "mcp", Event: "action_finish", ExecTimeMS: 1234}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if event.ExecTimeMS != 1234 {
		t.Fatalf("timing changed: %#v", event)
	}
}
