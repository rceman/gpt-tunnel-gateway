package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func task(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "read":
		require(args, 2)
		result, err := taskReadGatewayCall(ctx, s, args[1])
		if err != nil {
			fatal(err)
		}
		output(result)
	case "current":
		result, err := taskExecutionGatewayCall(ctx, s)
		if err != nil {
			fatal(err)
		}
		output(result)
	case "submit-code", "submit-tests", "submit-rebase":
		result, err := taskSubmitGatewayCall(ctx, s, args[0])
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}

func taskReadGatewayCall(ctx context.Context, s *service.Service, key string) (any, error) {
	if err := model.ValidateCanonicalTaskID(key); err != nil {
		return nil, err
	}
	session := strings.TrimSpace(os.Getenv("AIRELAY_SESSION_KEY"))
	if session == "" {
		return nil, fmt.Errorf("Gateway session authority is required; run this Agent command from a managed Airelay runtime")
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "call",
			"arguments": map[string]any{
				"session": session,
				"action":  "task/read",
				"input":   map[string]any{"key": key},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.Config.ListenAddr+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Gateway Task read request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("Gateway Task read request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gateway Task read returned HTTP %d", response.StatusCode)
	}
	var envelope map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("Gateway Task read response is invalid: %w", err)
	}
	result, _ := envelope["result"].(map[string]any)
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		if structured["ok"] == false {
			return nil, fmt.Errorf("Gateway Task read failed: %v", structured["error"])
		}
		if value, ok := structured["result"]; ok {
			return value, nil
		}
	}
	if result["isError"] == true {
		return nil, fmt.Errorf("Gateway Task read failed")
	}
	return result, nil
}

func taskExecutionGatewayCall(ctx context.Context, s *service.Service) (any, error) {
	session := strings.TrimSpace(os.Getenv("AIRELAY_SESSION_KEY"))
	if session == "" {
		return nil, fmt.Errorf("Gateway session authority is required; run this Agent command from a managed Airelay runtime")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "call", "arguments": map[string]any{"session": session, "action": "task/current", "input": map[string]any{}}}})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.Config.ListenAddr+"/mcp", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("Gateway Task request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("Gateway Task request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gateway Task request returned HTTP %d", response.StatusCode)
	}
	var envelope map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("Gateway Task response is invalid: %w", err)
	}
	result, _ := envelope["result"].(map[string]any)
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		if structured["ok"] == false {
			return nil, fmt.Errorf("Gateway Task action failed: %v", structured["error"])
		}
		if value, ok := structured["result"]; ok {
			return value, nil
		}
	}
	if result["isError"] == true {
		return nil, fmt.Errorf("Gateway Task action failed")
	}
	return result, nil
}

var agentCLISubmitEndpoints = map[string]string{
	"submit-code":   "/agent-cli/task/submit-code",
	"submit-tests":  "/agent-cli/task/submit-tests",
	"submit-rebase": "/agent-cli/task/submit-rebase",
}

func taskSubmitGatewayCall(ctx context.Context, s *service.Service, command string) (any, error) {
	endpoint, ok := agentCLISubmitEndpoints[command]
	if !ok {
		return nil, fmt.Errorf("unsupported Gateway Task submission %q", command)
	}
	runtime := os.Getenv("AIRELAY_SESSION_KEY")
	if runtime == "" || strings.TrimSpace(runtime) != runtime {
		return nil, fmt.Errorf("managed Airelay runtime identity is required; run this Agent command from a managed Airelay runtime")
	}
	body, err := json.Marshal(map[string]any{"runtime": runtime})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.Config.ListenAddr+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Gateway Task submission request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("Gateway Task submission request failed: %w", err)
	}
	defer response.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("Gateway Task submission returned HTTP %d", response.StatusCode)
	}
	if envelope["ok"] == false {
		return nil, fmt.Errorf("Gateway Task submission failed: %v", envelope["error"])
	}
	value, ok := envelope["result"]
	if !ok {
		return nil, fmt.Errorf("Gateway Task submission returned no result")
	}
	return value, nil
}
