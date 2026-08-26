package gatewayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

const (
	defaultHTTPTimeout = 5 * time.Second
	pollInterval       = 100 * time.Millisecond
)

type Client struct {
	endpoint   string
	httpClient *http.Client
	project    map[string]string
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type toolResult struct {
	IsError           bool           `json:"isError"`
	StructuredContent map[string]any `json:"structuredContent"`
	Content           []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func New(c config.Config) *Client {
	projects := make(map[string]string, len(c.Projects))
	for projectID, project := range c.Projects {
		if strings.TrimSpace(project.ProjectCode) != "" {
			projects[project.ProjectCode] = projectID
		}
	}
	return &Client{endpoint: "http://" + c.ListenAddr + "/mcp", httpClient: &http.Client{Timeout: defaultHTTPTimeout}, project: projects}
}

func (c *Client) TaskFinalize(ctx context.Context, taskID string) (map[string]any, error) {
	projectCode := ""
	if parts := strings.SplitN(taskID, "-", 2); len(parts) == 2 {
		projectCode = parts[0]
	}
	if _, ok := c.project[projectCode]; !ok {
		return nil, fmt.Errorf("no configured project code for task %q", taskID)
	}
	sessionID, err := c.startDeliverySession(ctx, projectCode)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), defaultHTTPTimeout)
		defer cancel()
		_, _ = c.callAction(cleanupCtx, sessionID, "session/end", map[string]any{})
	}()
	for {
		result, callErr := c.callAction(ctx, sessionID, "task/finalize", map[string]any{"task_id": taskID})
		if callErr != nil {
			return nil, callErr
		}
		status, ok := result["status"].(string)
		if !ok || status == "" {
			return nil, fmt.Errorf("Gateway task/finalize returned no durable status")
		}
		switch status {
		case "completed", "reused":
			return result, nil
		case "failed", "outcome_unknown":
			return nil, fmt.Errorf("Gateway task/finalize %s: %v", status, result["error"])
		case "accepted", "queued", "running", "pending", "in_progress":
		default:
			return nil, fmt.Errorf("Gateway task/finalize returned unknown status %q", status)
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) startDeliverySession(ctx context.Context, projectCode string) (string, error) {
	result, err := c.toolCall(ctx, "session_start", map[string]any{"project": projectCode, "role": "delivery"})
	if err != nil {
		return "", err
	}
	sessionID, ok := result["session"].(string)
	if !ok || sessionID == "" {
		return "", fmt.Errorf("Gateway session_start returned no session")
	}
	return sessionID, nil
}

func (c *Client) callAction(ctx context.Context, sessionID, action string, input map[string]any) (map[string]any, error) {
	result, err := c.toolCall(ctx, "call", map[string]any{"session": sessionID, "action": action, "input": input})
	if err != nil {
		return nil, err
	}
	if result["is_error"] == true {
		return nil, fmt.Errorf("Gateway action %s failed: %v", action, result["result"])
	}
	actionResult, ok := result["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Gateway action %s returned invalid result", action)
	}
	return actionResult, nil
}

func (c *Client) toolCall(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Gateway transport: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gateway HTTP status %d", response.StatusCode)
	}
	var rpc rpcResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&rpc); err != nil {
		return nil, fmt.Errorf("Gateway response decode: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("Gateway RPC error: %s", rpc.Error.Message)
	}
	var tool toolResult
	if err := json.Unmarshal(rpc.Result, &tool); err != nil {
		return nil, fmt.Errorf("Gateway tool result decode: %w", err)
	}
	if tool.IsError {
		if len(tool.Content) > 0 && tool.Content[0].Text != "" {
			return nil, fmt.Errorf("Gateway tool %s failed: %s", name, tool.Content[0].Text)
		}
		return nil, fmt.Errorf("Gateway tool %s failed", name)
	}
	if tool.StructuredContent == nil {
		return nil, fmt.Errorf("Gateway tool %s returned no structured content", name)
	}
	return tool.StructuredContent, nil
}
