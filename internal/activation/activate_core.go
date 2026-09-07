package activation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// LiveMCPSmoke proves the canonical public MCP runtime contract used by both
// activation and transactional upgrade/rollback verification.
func LiveMCPSmoke(ctx context.Context, c config.Config, expectedVersion string) error {
	call := func(id int, method string, params map[string]any) (map[string]any, error) {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.ListenAddr+"/mcp", bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("MCP %s request construction failed: %w", method, err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			return nil, fmt.Errorf("MCP %s request failed: %w", method, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("MCP %s HTTP status %d", method, response.StatusCode)
		}
		var value map[string]any
		if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&value); err != nil {
			return nil, fmt.Errorf("MCP %s invalid JSON-RPC response: %w", method, err)
		}
		if value["jsonrpc"] != "2.0" {
			return nil, fmt.Errorf("MCP %s JSON-RPC version mismatch", method)
		}
		gotID, ok := value["id"].(float64)
		if !ok || gotID != float64(id) {
			return nil, fmt.Errorf("MCP %s response id mismatch", method)
		}
		if rawError, exists := value["error"]; exists && rawError != nil {
			return nil, fmt.Errorf("MCP %s JSON-RPC error: %s", method, boundedMCPError(rawError))
		}
		if _, ok := value["result"].(map[string]any); !ok {
			return nil, fmt.Errorf("MCP %s result missing", method)
		}
		return value, nil
	}
	initResult, err := call(1, "initialize", map[string]any{})
	if err != nil {
		return err
	}
	result, ok := initResult["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP initialize result missing")
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok || serverInfo["version"] != expectedVersion {
		return fmt.Errorf("MCP source/version proof failed")
	}
	if protocolVersion, ok := result["protocolVersion"].(string); !ok || protocolVersion == "" {
		return fmt.Errorf("MCP initialize protocol version missing")
	}
	list, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	listResult, ok := list["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP tools/list result missing")
	}
	rawTools, ok := listResult["tools"].([]any)
	if !ok {
		return fmt.Errorf("MCP tools/list tools missing")
	}
	got := make([]string, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			return fmt.Errorf("MCP tools/list contains an invalid tool")
		}
		name, ok := tool["name"].(string)
		if !ok || name == "" {
			return fmt.Errorf("MCP tools/list contains a tool without a name")
		}
		got = append(got, name)
	}
	sort.Strings(got)
	want := append([]string(nil), canonicalRuntimeTools...)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		return fmt.Errorf("MCP public tool manifest mismatch")
	}
	statusCall, err := call(3, "tools/call", map[string]any{
		"name": "status", "arguments": map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("status smoke: %w", err)
	}
	statusResult, ok := statusCall["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("status result missing")
	}
	if isError, ok := statusResult["isError"].(bool); ok && isError {
		return fmt.Errorf("status smoke returned an MCP tool error")
	}
	structured, ok := statusResult["structuredContent"].(map[string]any)
	if !ok {
		return fmt.Errorf("status structured result missing")
	}
	if _, ok := structured["ready"].(bool); !ok {
		return fmt.Errorf("status readiness field missing")
	}
	if _, ok := structured["gateways"].([]any); !ok {
		return fmt.Errorf("status gateways field missing")
	}
	return nil
}

func boundedMCPError(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "unserializable JSON-RPC error"
	}
	const maxMCPErrorBytes = 4096
	if len(data) > maxMCPErrorBytes {
		data = data[:maxMCPErrorBytes]
	}
	return string(data)
}
