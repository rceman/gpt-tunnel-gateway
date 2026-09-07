package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

func assertInstalledCandidate(t *testing.T, installDir, source, version string) {
	t.Helper()
	for _, name := range releaseartifacts.BinaryNames {
		path := filepath.Join(installDir, name)
		gotSource, _, err := releaseartifacts.BinarySourceRevision(path)
		if err != nil || gotSource != source {
			t.Fatalf("installed %s source=%q err=%v want %q", name, gotSource, err, source)
		}
		gotVersion, err := releaseartifacts.BinaryVersion(path)
		if err != nil || gotVersion != version {
			t.Fatalf("installed %s version=%q err=%v want %q", name, gotVersion, err, version)
		}
	}
}

func waitCandidateArtifactRestore(t *testing.T, installDir string, want map[string]string, pidPath string, oldPID int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid := readCandidatePID(pidPath)
		if pid > 0 && pid != oldPID && processExists(pid) {
			matches := true
			for name, expected := range want {
				got, err := releaseartifacts.HashFile(filepath.Join(installDir, name))
				if err != nil || got != expected {
					matches = false
					break
				}
			}
			if matches {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

type candidateMCPResponse struct {
	StatusCode    int
	ContentLength int64
	Body          []byte
}

type candidateMCPClient struct {
	client   *http.Client
	endpoint string
	nextID   int
}

func (c *candidateMCPClient) request(method string, params any) (candidateMCPResponse, error) {
	c.nextID++
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	if err != nil {
		return candidateMCPResponse{}, err
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return candidateMCPResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return candidateMCPResponse{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return candidateMCPResponse{
		StatusCode:    resp.StatusCode,
		ContentLength: resp.ContentLength,
		Body:          data,
	}, err
}

func (c *candidateMCPClient) call(sessionID, action string, input map[string]any) (candidateMCPResponse, error) {
	return c.request("tools/call", map[string]any{
		"name": "call", "arguments": map[string]any{"session": sessionID, "action": action, "input": input},
	})
}

func candidateMCPOutcome(t *testing.T, body []byte) string {
	t.Helper()
	value := candidateMCPStructured(t, body)
	outcome, _ := value["outcome"].(string)
	return outcome
}

func candidateMCPStructured(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("MCP response JSON: %v: %s", err, body)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP response result=%#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("MCP structuredContent=%#v", response)
	}
	value, ok := structured["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP structured result=%#v", response)
	}
	return value
}

func waitCandidateDebugActivationSuccess(t *testing.T, client *candidateMCPClient, sessionID, sourceHead string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.call(sessionID, "debug/activate", map[string]any{"main_sha": sourceHead})
		if err != nil {
			t.Fatalf("debug/activate terminal success probe returned EOF/transport error: %v", err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("debug/activate terminal success status=%d body=%s", response.StatusCode, response.Body)
		}
		value := candidateMCPStructured(t, response.Body)
		if value["source_head"] != sourceHead {
			t.Fatalf("debug/activate terminal success source=%#v want %s", value["source_head"], sourceHead)
		}
		if value["outcome"] == "succeeded" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("debug activation %s did not reach terminal success", sourceHead)
}

func waitCandidateDebugActivationFailureReceipt(t *testing.T, stateDir, sourceHead string, timeout time.Duration) {
	t.Helper()
	id := "gateway-debug-activation-" + sourceHead
	digest := sha256.Sum256([]byte(id))
	path := filepath.Join(stateDir, "gateway-debug-activation", hex.EncodeToString(digest[:])+".json")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			var receipt struct {
				OperationID string `json:"operation_id"`
				SourceHead  string `json:"source_head"`
				Outcome     string `json:"outcome"`
				Error       string `json:"error"`
			}
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatalf("debug activation terminal receipt JSON: %v", err)
			}
			if receipt.OperationID != id || receipt.SourceHead != sourceHead {
				t.Fatalf("unexpected debug activation terminal receipt=%#v", receipt)
			}
			if receipt.Outcome == "failed" {
				if receipt.Error == "" || len(receipt.Error) > 2048 {
					t.Fatalf("unexpected debug activation terminal receipt=%#v", receipt)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("debug activation terminal failure receipt did not settle within %s", timeout)
}

func waitCandidateRecoverySuccess(t *testing.T, client *candidateMCPClient, sessionID, operationID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.call(sessionID, "runtime/logs", map[string]any{"limit": 100, "operation_id": operationID})
		if err == nil && response.StatusCode == http.StatusOK {
			result := candidateMCPStructured(t, response.Body)
			if events, ok := result["events"].([]any); ok {
				for _, raw := range events {
					event, ok := raw.(map[string]any)
					if ok && event["event"] == "recovery_finish" && event["operation_id"] == operationID && event["message"] == "succeeded" {
						return
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("recovery operation %q did not reach succeeded terminal event", operationID)
}

func reserveCandidateListenAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func waitCandidateHTTP(listenAddr, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + listenAddr + path)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("candidate HTTP %s did not become ready within %s", path, timeout)
}

func waitCandidatePIDChange(path string, oldPID int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid := readCandidatePID(path); pid > 0 && pid != oldPID && processExists(pid) {
			return pid
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0
}

func readCandidatePID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var record struct {
			PID int `json:"pid"`
		}
		if json.Unmarshal(data, &record) == nil {
			return record.PID
		}
		return 0
	}
	pid, _ := strconv.Atoi(trimmed)
	return pid
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func killCandidatePID(t *testing.T, pid int) {
	t.Helper()
	if pid < 1 || !processExists(pid) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processExists(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
