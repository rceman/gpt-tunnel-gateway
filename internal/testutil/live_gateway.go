package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

const liveGatewayReadyTimeout = 10 * time.Second

type LiveGatewayHooks struct {
	Configure   func(*config.Config)
	BeforeStart func(*LiveGateway)
}

type LiveGateway struct {
	Config         config.Config
	ConfigPath     string
	StateDir       string
	BaseDir        string
	ProjectRoot    string
	HubRemote      string
	GatewayBinary  string
	OperatorBinary string

	daemon     *exec.Cmd
	daemonDone chan struct{}
	daemonErr  error
	daemonMu   sync.Mutex
	stderr     *lockedBuffer
}

type LiveCommandOptions struct {
	Dir        string
	Env        map[string]string
	ConfigPath string
}

type LiveCommandResult struct {
	Stdout string
	Stderr string
	Err    error
}

func NewLiveGateway(t *testing.T, hooks LiveGatewayHooks) *LiveGateway {
	t.Helper()
	base := t.TempDir()
	_, projectRoot, _ := RepoWithBareRemote(t)
	hubRemote, _, _ := RepoWithBareRemote(t)
	Git(t, projectRoot, "remote", "set-head", "origin", "main")
	stateDir := filepath.Join(base, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		SchemaVersion:          1,
		GatewayID:              "HOM",
		ListenAddr:             reserveLoopbackAddress(t),
		StateDir:               stateDir,
		MaxReadBytes:           1 << 20,
		MaxDiffBytes:           1 << 20,
		MaxListItems:           1000,
		DispatchTimeoutSeconds: 5,
		RunTimeoutSeconds:      60,
		AirelayCommand:         "/bin/true",
		Hub:                    config.HubConfig{RepositoryURL: hubRemote, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		Controller:             config.ControllerConfig{TunnelHealthListenAddr: reserveLoopbackAddress(t)},
		Projects:               map[string]config.ProjectConfig{},
		ProjectAgentBindings:   map[string]map[string]config.AgentBinding{},
	}
	if hooks.Configure != nil {
		hooks.Configure(&cfg)
	}
	configPath := filepath.Join(base, "config.json")
	if err := writeLiveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	gateway := &LiveGateway{
		Config:      loaded,
		ConfigPath:  configPath,
		StateDir:    loaded.StateDir,
		BaseDir:     base,
		ProjectRoot: projectRoot,
		HubRemote:   hubRemote,
		stderr:      newLockedBuffer(64 << 10),
	}
	gateway.buildBinaries(t)
	if hooks.BeforeStart != nil {
		hooks.BeforeStart(gateway)
	}
	gateway.start(t)
	return gateway
}

func writeLiveConfig(path string, cfg config.Config) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (g *LiveGateway) WriteConfig(t *testing.T) {
	t.Helper()
	if err := writeLiveConfig(g.ConfigPath, g.Config); err != nil {
		t.Fatal(err)
	}
}

func (g *LiveGateway) WriteOperatorConfig(t *testing.T, listenAddr string) string {
	t.Helper()
	cfg := g.Config
	cfg.ListenAddr = listenAddr
	path := filepath.Join(g.BaseDir, "operator-"+strings.ReplaceAll(listenAddr, ":", "-")+".json")
	if err := writeLiveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}

func (g *LiveGateway) RunCLI(options LiveCommandOptions, args ...string) LiveCommandResult {
	cmd := exec.Command(g.OperatorBinary, args...)
	cmd.Dir = options.Dir
	if cmd.Dir == "" {
		cmd.Dir = g.ProjectRoot
	}
	cmd.Env = mergedEnv(os.Environ(), options.Env)
	configPath := options.ConfigPath
	if configPath == "" {
		configPath = g.ConfigPath
	}
	cmd.Env = append(cmd.Env, "GPT_TUNNEL_CONFIG="+configPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return LiveCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

func (g *LiveGateway) MustCLI(t *testing.T, options LiveCommandOptions, args ...string) LiveCommandResult {
	t.Helper()
	result := g.RunCLI(options, args...)
	if result.Err != nil {
		t.Fatalf("gpt-tunnel %v failed: %v\nstderr=%s", args, result.Err, result.Stderr)
	}
	return result
}

func (g *LiveGateway) MCPCall(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+g.ListenAddr()+"/mcp", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&envelope); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP HTTP status %d: %v", response.StatusCode, envelope)
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("MCP result missing: %v", envelope)
	}
	if result["isError"] == true {
		return nil, fmt.Errorf("MCP call failed: %v", result)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		return result, nil
	}
	return structured, nil
}

func (g *LiveGateway) ListenAddr() string { return g.Config.ListenAddr }

func (g *LiveGateway) OwnerLockActive() bool {
	db, err := sqlitestore.Open(g.StateDir)
	if err == nil {
		_ = db.Close()
		return false
	}
	return errors.Is(err, upstream.ErrAlreadyOpen) || strings.Contains(err.Error(), "already has an owner")
}
func (g *LiveGateway) DaemonStderr() string {
	return g.stderr.String()
}
func (g *LiveGateway) DaemonRunning() bool {
	select {
	case <-g.daemonDone:
		return false
	default:
		return g.daemon != nil && g.daemon.Process != nil
	}
}

func (g *LiveGateway) Stop() {
	if g == nil || g.daemon == nil {
		return
	}
	select {
	case <-g.daemonDone:
		return
	default:
	}
	_ = g.daemon.Process.Kill()
	select {
	case <-g.daemonDone:
	case <-time.After(5 * time.Second):
	}
}

func (g *LiveGateway) Restart(t *testing.T) {
	t.Helper()
	g.Stop()
	g.start(t)
}

func (g *LiveGateway) buildBinaries(t *testing.T) {
	t.Helper()
	root := repositoryRoot(t)
	binDir := filepath.Join(g.BaseDir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binaries := []struct {
		path string
		pkg  string
	}{
		{filepath.Join(binDir, "gpt-tunnel-gatewayd"), "./cmd/gpt-tunnel-gatewayd"},
		{filepath.Join(binDir, "gpt-tunnel"), "./cmd/gpt-tunnel"},
	}
	for _, binary := range binaries {
		cmd := exec.Command("go", "build", "-o", binary.path, binary.pkg)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", binary.pkg, err, output)
		}
	}
	g.GatewayBinary = binaries[0].path
	g.OperatorBinary = binaries[1].path
	output, err := exec.Command(g.OperatorBinary, "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		t.Fatalf("built gpt-tunnel smoke failed: output=%q err=%v", output, err)
	}
}

func (g *LiveGateway) start(t *testing.T) {
	t.Helper()
	cmd := exec.Command(g.GatewayBinary, "-config", g.ConfigPath)
	cmd.Env = append(os.Environ(), "GPT_TUNNEL_CONFIG="+g.ConfigPath)
	cmd.Stdout = newLockedBuffer(64 << 10)
	cmd.Stderr = g.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gatewayd: %v", err)
	}
	g.daemon = cmd
	g.daemonDone = make(chan struct{})
	go func() {
		err := cmd.Wait()
		g.daemonMu.Lock()
		g.daemonErr = err
		g.daemonMu.Unlock()
		close(g.daemonDone)
	}()
	t.Cleanup(g.Stop)
	deadline := time.Now().Add(liveGatewayReadyTimeout)
	client := &http.Client{Timeout: 250 * time.Millisecond}
	for {
		select {
		case <-g.daemonDone:
			g.daemonMu.Lock()
			err := g.daemonErr
			g.daemonMu.Unlock()
			t.Fatalf("gatewayd exited before readiness: %v\nstderr=%s", err, g.stderr.String())
		default:
		}
		response, err := client.Get("http://" + g.ListenAddr() + "/readyz")
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<10))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "ready" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("gatewayd did not become ready within %s\nstderr=%s", liveGatewayReadyTimeout, g.stderr.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func reserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

func mergedEnv(base []string, overrides map[string]string) []string {
	result := append([]string(nil), base...)
	for key, value := range overrides {
		prefix := key + "="
		replaced := false
		for i, entry := range result {
			if strings.HasPrefix(entry, prefix) {
				result[i] = prefix + value
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, prefix+value)
		}
	}
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

type lockedBuffer struct {
	mu      sync.Mutex
	limit   int
	data    bytes.Buffer
	skipped int
}

func newLockedBuffer(limit int) *lockedBuffer {
	return &lockedBuffer{limit: limit}
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.data.Len()
	if remaining > 0 {
		if len(data) > remaining {
			b.data.Write(data[:remaining])
			b.skipped += len(data) - remaining
		} else {
			b.data.Write(data)
		}
	} else {
		b.skipped += len(data)
	}
	return len(data), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.skipped == 0 {
		return b.data.String()
	}
	return b.data.String() + fmt.Sprintf("\n[truncated %d bytes]", b.skipped)
}
