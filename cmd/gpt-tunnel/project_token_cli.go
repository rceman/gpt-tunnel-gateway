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

	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

const operatorProjectTokenPath = "/operator/project/token"

func projectTokenGatewayCall(ctx context.Context, s *service.Service) (string, error) {
	adminSession := strings.TrimSpace(os.Getenv("GPT_TUNNEL_ADMIN_SESSION"))
	if adminSession == "" {
		return "", fmt.Errorf("active Admin Session is required; set GPT_TUNNEL_ADMIN_SESSION")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}
	maxReadBytes := s.Config.MaxReadBytes
	if maxReadBytes <= 0 {
		maxReadBytes = 1 << 20
	}
	git := gitx.Runner{MaxReadBytes: maxReadBytes}
	root, err := git.RepositoryRoot(ctx, cwd)
	if err != nil {
		return "", fmt.Errorf("current directory is not a Git repository: %w", err)
	}
	remotes, err := git.RepositoryRemotes(ctx, root)
	if err != nil {
		return "", fmt.Errorf("resolve current repository identity: %w", err)
	}
	requestBody, err := json.Marshal(map[string]any{"root": root, "remotes": remotes})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.Config.ListenAddr+operatorProjectTokenPath, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("project token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GPT-Tunnel-Admin-Session", adminSession)
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		return "", fmt.Errorf("project token request failed: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			Token string `json:"token"`
		} `json:"result"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope); err != nil {
		return "", fmt.Errorf("project token response is invalid: %w", err)
	}
	if response.StatusCode != http.StatusOK || !envelope.OK || envelope.Result.Token == "" {
		message := envelope.Error.Message
		if message == "" {
			message = "project token request was rejected"
		}
		return "", fmt.Errorf("project token request failed: %s", message)
	}
	return envelope.Result.Token, nil
}
