package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func prompt(ctx context.Context, s *service.Service, args []string) {
	if len(args) != 1 {
		fatal(fmt.Errorf("usage: gpt-tunnel prompt <PMT-ID>"))
	}
	result, err := promptReadViaMCP(ctx, s, args[0])
	if err != nil {
		fatal(err)
	}
	output(result)
}

func promptReadViaMCP(ctx context.Context, s *service.Service, pmtID string) (service.PMTReadResult, error) {
	records, err := session.NewStore(s.Config.StateDir).List()
	if err != nil {
		return service.PMTReadResult{}, fmt.Errorf("read Agent sessions: %w", err)
	}
	var lastErr error
	found := false
	for _, record := range records {
		if record.Status != session.StatusActive || record.Role != session.RoleAgent {
			continue
		}
		found = true
		result, callErr := promptReadMCPCall(ctx, s.Config.ListenAddr, record.ID, pmtID)
		if callErr == nil {
			return result, nil
		}
		lastErr = callErr
	}
	if !found {
		return service.PMTReadResult{}, fmt.Errorf("no active Agent session is available")
	}
	return service.PMTReadResult{}, fmt.Errorf("agent/prompt_read failed for active Agent sessions: %w", lastErr)
}

func promptReadMCPCall(ctx context.Context, listenAddr, sessionID, pmtID string) (service.PMTReadResult, error) {
	endpoint := listenAddr
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/mcp"
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "call",
			"arguments": map[string]any{
				"session": sessionID,
				"action":  "agent/prompt_read",
				"input":   map[string]any{"pmt_id": pmtID},
			},
		},
	})
	if err != nil {
		return service.PMTReadResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return service.PMTReadResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return service.PMTReadResult{}, fmt.Errorf("MCP request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return service.PMTReadResult{}, err
	}
	if response.StatusCode != http.StatusOK {
		return service.PMTReadResult{}, fmt.Errorf("MCP status %d", response.StatusCode)
	}
	var envelope struct {
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return service.PMTReadResult{}, fmt.Errorf("decode MCP response: %w", err)
	}
	if envelope.Error != nil {
		return service.PMTReadResult{}, fmt.Errorf("MCP error: %s", envelope.Error.Message)
	}
	var structured struct {
		Result  json.RawMessage `json:"result"`
		IsError bool            `json:"is_error"`
	}
	if err := json.Unmarshal(envelope.Result.StructuredContent, &structured); err != nil {
		return service.PMTReadResult{}, fmt.Errorf("decode agent/prompt_read result: %w", err)
	}
	if structured.IsError {
		return service.PMTReadResult{}, fmt.Errorf("agent/prompt_read rejected: %s", string(structured.Result))
	}
	var result service.PMTReadResult
	if err := json.Unmarshal(structured.Result, &result); err != nil {
		return service.PMTReadResult{}, fmt.Errorf("decode agent/prompt_read payload: %w", err)
	}
	return result, nil
}
