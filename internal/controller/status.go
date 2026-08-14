package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (c Controller) gatewayReadyURL() string { return "http://" + c.Config.ListenAddr + "/readyz" }
func (c Controller) tunnelReadyURL() string {
	return "http://" + c.Config.Controller.TunnelHealthListenAddr + "/readyz"
}
func (c Controller) Status(ctx context.Context) (Status, error) {
	identity := c.RuntimeIdentity(ctx)
	return Status{
		Gateway:          c.process("gateway", mustEval(c.Config.Controller.GatewayBinary)),
		Tunnel:           c.process("tunnel", mustEval(c.Config.Controller.TunnelClientBinary)),
		GatewayReady:     identity.GatewayReady,
		TunnelReady:      identity.TunnelReady,
		InstalledVersion: identity.InstalledVersion,
		RunningVersion:   identity.RunningVersion,
		VersionMatch:     identity.VersionMatch,
		RuntimeIdentity:  identity,
	}, nil
}

func mustEval(path string) string {
	resolved, _ := filepath.EvalSymlinks(path)
	return resolved
}
func installedVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func runningVersion(ctx context.Context, readyURL, gatewayID string) string {
	return runningVersionProbe(ctx, readyURL, gatewayID, "status")
}

func runningVersionProbe(ctx context.Context, readyURL, gatewayID, toolName string) string {
	endpoint := strings.TrimSuffix(readyURL, "/readyz") + "/mcp"
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": toolName, "arguments": map[string]any{}}}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return ""
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	var envelope struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Version   string `json:"version"`
				GatewayID string `json:"gateway_id"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&envelope); err != nil || envelope.Result.IsError {
		return ""
	}
	if gatewayID != "" && envelope.Result.StructuredContent.GatewayID != gatewayID {
		return ""
	}
	return envelope.Result.StructuredContent.Version
}
func checkURL(ctx context.Context, url string) bool {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
func waitURL(url string, want bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		ok := checkURL(ctx, url)
		cancel()
		if ok == want {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("readiness timeout for %s", url)
}
