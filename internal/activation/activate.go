package activation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/mcpmanifest"
	"github.com/rceman/gpt-tunnel-gateway/internal/releaseartifacts"
)

const OutputLimit = 1 << 20

var canonicalRuntimeTools = mcpmanifest.CanonicalToolNames()

type Result struct {
	SourceHead string `json:"source_head"`
	Activation string `json:"activation"`
	Smoke      string `json:"smoke"`
	TunnelPID  int    `json:"tunnel_pid,omitempty"`
	GatewayPID int    `json:"gateway_pid,omitempty"`
}

// ProveSource verifies that the requested source is already the live runtime
// without changing installed artifacts or restarting a process. It is used by
// in-process control-plane mutations, where calling Source would terminate
// the serving gateway before the mutation can commit.
func ProveSource(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (Result, error) {
	if project.Root == "" || sourceHead == "" {
		return Result{}, fmt.Errorf("activation source is incomplete")
	}
	if got, err := gitOutput(ctx, project.Root, "rev-parse", "--verify", "HEAD^{commit}"); err != nil || got != sourceHead {
		return Result{}, fmt.Errorf("project source head is not the reviewed head")
	}
	if dirty, err := gitOutput(ctx, project.Root, "status", "--porcelain", "--untracked-files=all"); err != nil || dirty != "" {
		return Result{}, fmt.Errorf("project source worktree is dirty")
	}
	versionBytes, err := os.ReadFile(filepath.Join(project.Root, "VERSION"))
	if err != nil {
		return Result{}, err
	}
	targetVersion := strings.TrimSpace(string(versionBytes))
	if targetVersion == "" {
		return Result{}, fmt.Errorf("project VERSION is empty")
	}
	release, err := os.MkdirTemp("", "gpt-tunnel-source-proof-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(release)
	build := exec.CommandContext(ctx, filepath.Join(project.Root, "scripts", "build-release.sh"), release)
	build.Dir = project.Root
	if output, err := build.CombinedOutput(); err != nil {
		return Result{}, fmt.Errorf("source proof build failed: %s", BoundedOutput(output))
	}
	builtGateway := filepath.Join(release, "gpt-tunnel-gatewayd")
	installedGateway := c.Controller.GatewayBinary
	builtSHA, err := sha256File(builtGateway)
	if err != nil {
		return Result{}, fmt.Errorf("source proof artifact checksum failed: %w", err)
	}
	installedSHA, err := sha256File(installedGateway)
	if err != nil {
		return Result{}, fmt.Errorf("installed gateway checksum failed: %w", err)
	}
	if builtSHA != installedSHA {
		return Result{}, fmt.Errorf("installed gateway does not match exact source proof: built_sha256=%s installed_sha256=%s", builtSHA, installedSHA)
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	status, err := ctl.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if !status.Gateway.Running || !status.Tunnel.Running || !status.GatewayReady || !status.TunnelReady || !status.VersionMatch || status.InstalledVersion != targetVersion || status.RunningVersion != targetVersion {
		return Result{}, fmt.Errorf("runtime is not healthy and version-matched for the reviewed source")
	}
	if err := ctl.Doctor(ctx); err != nil {
		return Result{}, err
	}
	if err := LiveMCPSmoke(ctx, c, targetVersion); err != nil {
		return Result{}, err
	}
	return Result{
		SourceHead: sourceHead,
		Activation: "already_active",
		Smoke:      "passed",
		TunnelPID:  status.Tunnel.PID,
		GatewayPID: status.Gateway.PID,
	}, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// SelfActivate builds and activates one exact GTW source revision into an
// external operation directory. It performs offline verification first, then
// holds the controller handoff lock across Gateway stop, atomic replacement,
// start, readiness/provenance proof, and rollback. Tunnel is never touched.
func SelfActivate(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (Result, error) {
	if project.Root == "" || sourceHead == "" {
		return Result{}, fmt.Errorf("activation source is incomplete")
	}
	if got, err := gitOutput(ctx, project.Root, "rev-parse", "--verify", "HEAD^{commit}"); err != nil || got != sourceHead {
		return Result{}, fmt.Errorf("project source head is not the reviewed head")
	}
	if dirty, err := gitOutput(ctx, project.Root, "status", "--porcelain", "--untracked-files=all"); err != nil || dirty != "" {
		return Result{}, fmt.Errorf("project source worktree is dirty")
	}
	versionBytes, err := os.ReadFile(filepath.Join(project.Root, "VERSION"))
	if err != nil {
		return Result{}, err
	}
	targetVersion := strings.TrimSpace(string(versionBytes))
	if targetVersion == "" {
		return Result{}, fmt.Errorf("project VERSION is empty")
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	before, err := ctl.Status(ctx)
	if err != nil {
		return Result{}, err
	}
	if !before.Gateway.Running || !before.Tunnel.Running || !before.GatewayReady || !before.TunnelReady {
		return Result{}, fmt.Errorf("runtime is not healthy before activation")
	}
	release, err := os.MkdirTemp("", "gpt-tunnel-activation-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(release)
	build := exec.CommandContext(ctx, filepath.Join(project.Root, "scripts", "build-release.sh"), release)
	build.Dir = project.Root
	output, err := build.CombinedOutput()
	if err != nil {
		return Result{}, fmt.Errorf("release build failed: %s", BoundedOutput(output))
	}
	if err := releaseartifacts.ValidateRelease(release, targetVersion); err != nil {
		return Result{}, err
	}
	if err := SmokeCandidate(ctx, c, filepath.Join(release, "gpt-tunnel-gatewayd"), targetVersion); err != nil {
		return Result{}, err
	}
	paths := releaseartifacts.Paths(c.Controller.GatewayBinary)
	old, err := releaseartifacts.SnapshotAll(paths)
	if err != nil {
		return Result{}, err
	}
	var after controller.Status
	err = ctl.ActivateGateway(controller.GatewayActivation{
		Replace: func() error {
			return releaseartifacts.ReplaceAll(release, paths, old)
		},
		Restore: func() error {
			return releaseartifacts.RestoreAll(paths, old)
		},
		Verify: func() error {
			if err := releaseartifacts.VerifyInstalled(release, paths); err != nil {
				return err
			}
			var statusErr error
			after, statusErr = ctl.Status(ctx)
			if statusErr != nil {
				return statusErr
			}
			if after.Tunnel.PID != before.Tunnel.PID || !after.GatewayReady || !after.TunnelReady || !after.VersionMatch || !after.RuntimeIdentity.ExactSourceMatch || after.RuntimeIdentity.SourceSHA != sourceHead {
				return fmt.Errorf("activation runtime identity/readiness/source proof failed")
			}
			if err := ctl.Doctor(ctx); err != nil {
				return err
			}
			return LiveMCPSmoke(ctx, c, targetVersion)
		},
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		SourceHead: sourceHead,
		Activation: "passed",
		Smoke:      "passed",
		TunnelPID:  after.Tunnel.PID,
		GatewayPID: after.Gateway.PID,
	}, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func binaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	return strings.TrimSpace(string(out)), err
}

func BoundedOutput(data []byte) string {
	if len(data) > OutputLimit {
		data = data[:OutputLimit]
	}
	return strings.TrimSpace(string(data))
}

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
