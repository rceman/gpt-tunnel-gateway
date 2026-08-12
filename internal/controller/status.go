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
	gatewayExpected, _ := filepath.EvalSymlinks(c.Config.Controller.GatewayBinary)
	tunnelExpected, _ := filepath.EvalSymlinks(c.Config.Controller.TunnelClientBinary)
	s := Status{
		Gateway: c.process("gateway", gatewayExpected),
		Tunnel:  c.process("tunnel", tunnelExpected),
	}
	s.GatewayReady = checkURL(ctx, c.gatewayReadyURL())
	s.TunnelReady = checkURL(ctx, c.tunnelReadyURL())
	s.InstalledVersion = installedVersion(c.Config.Controller.GatewayBinary)
	if s.GatewayReady {
		s.RunningVersion = runningVersion(ctx, c.gatewayReadyURL(), c.Config.GatewayID)
	}
	s.VersionMatch = s.InstalledVersion != "" && s.RunningVersion != "" && s.InstalledVersion == s.RunningVersion
	recovery, err := c.readGatewayRecoveryState()
	if err != nil {
		return Status{}, fmt.Errorf("read Gateway recovery state: %w", err)
	}
	s.Degraded = recovery.Status == "degraded"
	s.DegradedReason = recovery.Reason
	return projectRuntimeStatus(s, recovery), nil
}
func installedVersion(path string) string {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func runningVersion(ctx context.Context, readyURL, gatewayID string) string {
	endpoint := strings.TrimSuffix(readyURL, "/readyz") + "/mcp"
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "system_ping", "arguments": map[string]any{}}}
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
