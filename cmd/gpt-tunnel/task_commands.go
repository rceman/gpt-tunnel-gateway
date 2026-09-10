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
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func task(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "work":
		require(args, 2)
		result, e := s.TaskWork(ctx, service.TaskWorkInput{TaskID: args[1]})
		if e != nil {
			fatal(e)
		}
		output(result)
	case "finalize":
		require(args, 2)
		result, e := s.TaskFinalize(ctx, service.TaskFinalizeInput{TaskID: args[1]})
		if e != nil {
			fatal(e)
		}
		output(result)
	case "read":
		require(args, 2)
		result, err := taskReadGatewayCall(ctx, s, args[1])
	case "current", "submit-code", "submit-tests", "submit-rebase":
		require(args, 2)
		projectID, err := taskProjectForCLI(s, args[1])
		if err != nil {
			fatal(err)
		}
		db, err := sqlitestore.Open(s.Config.StateDir)
		if err != nil {
			fatal(fmt.Errorf("open Task execution authority: %w", err))
		}
		defer db.Close()
		s.Durability = db
		var result service.TaskExecutionPublicOutput
		switch args[0] {
		case "current":
			result, err = s.TaskExecutionStatus(ctx, projectID, args[1])
		case "submit-code":
			result, err = s.TaskExecutionSubmitCode(ctx, projectID, args[1])
		case "submit-tests":
			result, err = s.TaskExecutionSubmitTests(ctx, projectID, args[1])
		case "submit-rebase":
			result, err = s.TaskExecutionSubmitRebase(ctx, projectID, args[1])
		}
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
	session := os.Getenv("GPT_TUNNEL_SESSION")
	if session == "" {
		return nil, fmt.Errorf("Gateway session authority is required; run this Agent command from a Gateway-bound session")
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

func taskProjectForCLI(s *service.Service, key string) (string, error) {
	if err := model.ValidateCanonicalTaskID(key); err != nil {
		return "", err
	}
	code := key[:strings.Index(key, "-TSK")]
	found := ""
	for projectID, project := range s.Config.Projects {
		if project.ProjectCode == code {
			if found != "" {
				return "", fmt.Errorf("Task key %q matches multiple configured projects", key)
			}
			found = projectID
		}
	}
	if found == "" {
		return "", fmt.Errorf("no configured project matches Task key %q", key)
	}
	return found, nil
}
