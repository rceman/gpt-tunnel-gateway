package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func smoke(ctx context.Context, c config.Config, expectedVersion, previousVersion string) error {
	url := "http://" + c.ListenAddr + "/mcp"
	call := func(id int, method string, params any) (map[string]any, error) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, e := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("MCP HTTP status %d", resp.StatusCode)
		}
		var v map[string]any
		e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&v)
		if e != nil || v["jsonrpc"] != "2.0" || v["error"] != nil {
			return nil, fmt.Errorf("invalid MCP JSON-RPC response")
		}
		gotID, ok := v["id"].(float64)
		if !ok || int(gotID) != id {
			return nil, fmt.Errorf("MCP response id mismatch")
		}
		if _, ok := v["result"].(map[string]any); !ok {
			return nil, fmt.Errorf("MCP result missing")
		}
		return v, e
	}
	init, err := call(1, "initialize", map[string]any{})
	if err != nil {
		return err
	}
	result, ok := init["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("initialize result missing")
	}
	info, ok := result["serverInfo"].(map[string]any)
	protocolVersion, protocolOK := result["protocolVersion"].(string)
	if !ok || info["version"] != expectedVersion || !protocolOK || protocolVersion == "" {
		return fmt.Errorf("MCP version mismatch")
	}
	list, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	listResult, ok := list["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("tools/list result missing")
	}
	tools, ok := listResult["tools"].([]any)
	if !ok || len(tools) == 0 {
		return fmt.Errorf("no MCP tools")
	}
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid tool descriptor")
		}
		name, ok := tool["name"].(string)
		if !ok || name == "" {
			return fmt.Errorf("tool name missing")
		}
		inputSchema, ok := tool["inputSchema"].(map[string]any)
		if !ok || inputSchema["type"] != "object" {
			return fmt.Errorf("tool input schema missing")
		}
		outputSchema, ok := tool["outputSchema"].(map[string]any)
		if !ok || outputSchema["type"] != "object" {
			return fmt.Errorf("tool output schema missing")
		}
		annotations, ok := tool["annotations"].(map[string]any)
		if !ok {
			return fmt.Errorf("tool annotations missing")
		}
		for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := annotations[key].(bool); !ok {
				return fmt.Errorf("tool annotation missing")
			}
		}
	}
	ping, err := call(3, "tools/call", map[string]any{"name": "system_ping", "arguments": map[string]any{}, "_meta": map[string]any{"upgrade": true}})
	if err != nil {
		return err
	}
	pingResult, ok := ping["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP ping failed")
	}
	isError, isErrorOK := pingResult["isError"].(bool)
	if !isErrorOK || isError {
		return fmt.Errorf("MCP ping returned error")
	}
	pingContent, ok := pingResult["structuredContent"].(map[string]any)
	if !ok || pingContent["version"] != expectedVersion || pingContent["gateway_id"] != c.GatewayID || pingContent["service"] != "gpt-tunnel-gatewayd" {
		return fmt.Errorf("MCP ping structured content missing")
	}
	cap, err := call(4, "tools/call", map[string]any{"name": "gateway_capabilities", "arguments": map[string]any{}})
	if err != nil {
		return err
	}
	capResult, ok := cap["result"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP capabilities failed")
	}
	capError, capErrorOK := capResult["isError"].(bool)
	if !capErrorOK || capError {
		return fmt.Errorf("MCP capabilities returned error")
	}
	structured, ok := capResult["structuredContent"].(map[string]any)
	if !ok {
		return fmt.Errorf("MCP capabilities structured content missing")
	}
	if structured["gateway_id"] != c.GatewayID || structured["hub_protocol_root"] != "gpt-tunnel/v1" || structured["hub_branch"] != c.Hub.Branch || structured["hub_managed_root"] != filepath.Join(c.StateDir, "hub", "repository") || expectedVersion == previousVersion {
		return fmt.Errorf("MCP capabilities mismatch")
	}
	return nil
}
