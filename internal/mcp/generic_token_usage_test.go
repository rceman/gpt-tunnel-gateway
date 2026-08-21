package mcp

import (
	"encoding/json"
	"testing"
)

func TestGenericTokenUsageCountsCanonicalRequestAndResponse(t *testing.T) {
	server := &Server{}
	result := map[string]any{"result": map[string]any{"ok": true}, "is_error": false}
	addTokenUsage(server, result, json.RawMessage(`{"action":"status","input":{}}`))
	if intValue(result["request_tokens"]) < 1 || intValue(result["response_tokens"]) < 1 {
		t.Fatalf("token usage=%#v", result)
	}
	if _, ok := result["token_count_ms"].(int64); !ok {
		t.Fatalf("token_count_ms=%T", result["token_count_ms"])
	}
}
