package mcp

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestGenericTransportPreservesStructuredLifecycleConflict(t *testing.T) {
	err := &service.LifecycleConflictError{
		Code:    "TRAIN_REVISION_STATUS_CONFLICT",
		Phase:   "transaction",
		Details: map[string]any{"guard": "revision", "expected_revision": 6, "current_revision": 7},
	}
	response := genericActionError("train/add", err)
	result := response["result"].(map[string]any)
	structured := result["error"].(map[string]any)
	if structured["code"] != "TRAIN_REVISION_STATUS_CONFLICT" || structured["phase"] != "transaction" {
		t.Fatalf("structured error=%#v", structured)
	}
	if _, ok := structured["details"].(map[string]any); !ok {
		t.Fatalf("missing structured details=%#v", structured)
	}
}
