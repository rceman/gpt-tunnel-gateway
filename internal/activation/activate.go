package activation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
)

const OutputLimit = 1 << 20

type Result struct {
	SourceHead string `json:"source_head"`
	Activation string `json:"activation"`
	Smoke      string `json:"smoke"`
	TunnelPID  int    `json:"tunnel_pid,omitempty"`
	GatewayPID int    `json:"gateway_pid,omitempty"`
}

// Source builds and activates one exact source revision into an external
// operation directory. It restarts only Gateway, preserves Tunnel identity,
// verifies source/version/readiness/doctor/MCP, and removes all artifacts.
func Source(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (Result, error) {
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
	gatewayArtifact := filepath.Join(release, "gpt-tunnel-gatewayd")
	if info, err := os.Stat(gatewayArtifact); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return Result{}, fmt.Errorf("release gateway artifact is invalid")
	}
	if got, err := binaryVersion(gatewayArtifact); err != nil || got != targetVersion {
		return Result{}, fmt.Errorf("release gateway version does not match VERSION")
	}
	installed := c.Controller.GatewayBinary
	old, err := os.ReadFile(installed)
	if err != nil {
		return Result{}, err
	}
	artifact, err := os.ReadFile(gatewayArtifact)
	if err != nil {
		return Result{}, err
	}
	if err := fsutil.WriteFileAtomic(installed, artifact, 0o755); err != nil {
		return Result{}, err
	}
	restore := func() {
		_ = fsutil.WriteFileAtomic(installed, old, 0o755)
		_ = ctl.RestartGatewayAfterUpgrade()
	}
	if err := ctl.RestartGatewayAfterUpgrade(); err != nil {
		restore()
		return Result{}, fmt.Errorf("gateway activation failed: %w", err)
	}
	after, err := ctl.Status(ctx)
	if err != nil || after.Tunnel.PID != before.Tunnel.PID || !after.GatewayReady || !after.TunnelReady || !after.VersionMatch {
		restore()
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("activation runtime identity/readiness proof failed")
	}
	if err := ctl.Doctor(ctx); err != nil {
		restore()
		return Result{}, err
	}
	if err := liveMCPSmoke(ctx, c, targetVersion); err != nil {
		restore()
		return Result{}, err
	}
	return Result{SourceHead: sourceHead, Activation: "passed", Smoke: "passed", TunnelPID: after.Tunnel.PID, GatewayPID: after.Gateway.PID}, nil
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

func liveMCPSmoke(ctx context.Context, c config.Config, expectedVersion string) error {
	call := func(id int, method string, params map[string]any) (map[string]any, error) {
		payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+c.ListenAddr+"/mcp", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("MCP status %d", response.StatusCode)
		}
		var value map[string]any
		if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&value); err != nil {
			return nil, err
		}
		if value["error"] != nil {
			return nil, fmt.Errorf("MCP %s returned an error", method)
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
	if _, err := call(2, "tools/list", map[string]any{}); err != nil {
		return err
	}
	if _, err := call(3, "tools/call", map[string]any{"name": "system_ping", "arguments": map[string]any{}}); err != nil {
		return err
	}
	return nil
}
