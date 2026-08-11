package service

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

const taskActivationOutputLimit = 1 << 20

func activateTaskSource(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig, sourceHead string) (TaskActivationResult, error) {
	if project.Root == "" || sourceHead == "" {
		return TaskActivationResult{}, fmt.Errorf("task activation source is incomplete")
	}
	if got, err := taskGitOutput(ctx, project.Root, "rev-parse", "--verify", "HEAD^{commit}"); err != nil || got != sourceHead {
		return TaskActivationResult{}, fmt.Errorf("project source head is not the reviewed head")
	}
	if dirty, err := taskGitOutput(ctx, project.Root, "status", "--porcelain", "--untracked-files=all"); err != nil || dirty != "" {
		return TaskActivationResult{}, fmt.Errorf("project source worktree is dirty")
	}
	versionBytes, err := os.ReadFile(filepath.Join(project.Root, "VERSION"))
	if err != nil {
		return TaskActivationResult{}, err
	}
	targetVersion := strings.TrimSpace(string(versionBytes))
	if targetVersion == "" {
		return TaskActivationResult{}, fmt.Errorf("project VERSION is empty")
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	before, err := ctl.Status(ctx)
	if err != nil {
		return TaskActivationResult{}, err
	}
	if !before.Gateway.Running || !before.Tunnel.Running || !before.GatewayReady || !before.TunnelReady {
		return TaskActivationResult{}, fmt.Errorf("runtime is not healthy before task activation")
	}
	release, err := os.MkdirTemp("", "gpt-tunnel-task-activation-")
	if err != nil {
		return TaskActivationResult{}, err
	}
	defer os.RemoveAll(release)
	build := exec.CommandContext(ctx, filepath.Join(project.Root, "scripts", "build-release.sh"), release)
	build.Dir = project.Root
	output, err := build.CombinedOutput()
	if err != nil {
		return TaskActivationResult{}, fmt.Errorf("task release build failed: %s", boundedTaskOutput(output))
	}
	gatewayArtifact := filepath.Join(release, "gpt-tunnel-gatewayd")
	if info, err := os.Stat(gatewayArtifact); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return TaskActivationResult{}, fmt.Errorf("task release gateway artifact is invalid")
	}
	if got, err := taskBinaryVersion(gatewayArtifact); err != nil || got != targetVersion {
		return TaskActivationResult{}, fmt.Errorf("task release gateway version does not match VERSION")
	}
	installed := c.Controller.GatewayBinary
	old, err := os.ReadFile(installed)
	if err != nil {
		return TaskActivationResult{}, err
	}
	artifact, err := os.ReadFile(gatewayArtifact)
	if err != nil {
		return TaskActivationResult{}, err
	}
	if err := fsutil.WriteFileAtomic(installed, artifact, 0o755); err != nil {
		return TaskActivationResult{}, err
	}
	restore := func() {
		_ = fsutil.WriteFileAtomic(installed, old, 0o755)
		_ = ctl.RestartGatewayAfterUpgrade()
	}
	if err := ctl.RestartGatewayAfterUpgrade(); err != nil {
		restore()
		return TaskActivationResult{}, fmt.Errorf("task gateway activation failed: %w", err)
	}
	after, err := ctl.Status(ctx)
	if err != nil || after.Tunnel.PID != before.Tunnel.PID || !after.GatewayReady || !after.TunnelReady || !after.VersionMatch {
		restore()
		if err != nil {
			return TaskActivationResult{}, err
		}
		return TaskActivationResult{}, fmt.Errorf("task activation runtime identity/readiness proof failed")
	}
	if err := ctl.Doctor(ctx); err != nil {
		restore()
		return TaskActivationResult{}, err
	}
	if err := taskLiveMCPSmoke(ctx, c, targetVersion); err != nil {
		restore()
		return TaskActivationResult{}, err
	}
	return TaskActivationResult{
		SourceHead: sourceHead,
		Activation: "passed",
		Smoke:      "passed",
		TunnelPID:  after.Tunnel.PID,
		GatewayPID: after.Gateway.PID,
	}, nil
}

func taskGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func taskBinaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	return strings.TrimSpace(string(out)), err
}

func boundedTaskOutput(data []byte) string {
	if len(data) > taskActivationOutputLimit {
		data = data[:taskActivationOutputLimit]
	}
	return strings.TrimSpace(string(data))
}

func taskLiveMCPSmoke(ctx context.Context, c config.Config, expectedVersion string) error {
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
