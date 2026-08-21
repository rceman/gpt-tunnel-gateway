package activation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

const candidateSmokeTimeout = 15 * time.Second

// SmokeCandidate starts the built Gateway on an isolated loopback port and
// exercises readiness plus the canonical MCP ABI before installed artifacts
// are replaced. Candidate durability and Hub locks are isolated from the
// production StateDir for the entire process lifetime.
func SmokeCandidate(ctx context.Context, c config.Config, gatewayPath, expectedVersion string) error {
	probeCtx, cancel := context.WithTimeout(ctx, candidateSmokeTimeout)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	candidate := c
	candidate.ListenAddr = addr
	candidateStateDir, err := os.MkdirTemp("", "gpt-tunnel-candidate-state-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(candidateStateDir)
	candidate.StateDir = candidateStateDir
	candidateHubPath, cleanupHub, err := createOfflineCandidateHub(probeCtx, c.Hub)
	if err != nil {
		return err
	}
	defer cleanupHub()
	candidate.Hub.RepositoryURL = candidateHubPath
	configFile, err := os.CreateTemp("", "gpt-tunnel-candidate-config-*.json")
	if err != nil {
		return err
	}
	configPath := configFile.Name()
	defer os.Remove(configPath)
	data, err := json.Marshal(candidate)
	if err != nil {
		_ = configFile.Close()
		return err
	}
	if _, err := configFile.Write(data); err != nil {
		_ = configFile.Close()
		return err
	}
	if err := configFile.Close(); err != nil {
		return err
	}
	command := exec.CommandContext(probeCtx, gatewayPath, "-config", configPath)
	var output boundedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return fmt.Errorf("candidate start: %w", err)
	}
	defer func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()
	readyURL := "http://" + addr + "/readyz"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		request, requestErr := http.NewRequestWithContext(probeCtx, http.MethodGet, readyURL, nil)
		if requestErr == nil {
			response, getErr := client.Do(request)
			if getErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					candidate.ListenAddr = addr
					if err := liveMCPSmoke(probeCtx, candidate, expectedVersion); err != nil {
						return fmt.Errorf("candidate MCP smoke: %w", err)
					}
					return nil
				}
			}
		}
		select {
		case <-probeCtx.Done():
			return fmt.Errorf("candidate readiness failed: %w (%s)", probeCtx.Err(), BoundedOutput(output.Bytes()))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func createOfflineCandidateHub(ctx context.Context, hub config.HubConfig) (string, func(), error) {
	root, err := os.MkdirTemp("", "gpt-tunnel-candidate-hub-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	bare := filepath.Join(root, "repository.git")
	seed := filepath.Join(root, "seed")
	if err := os.MkdirAll(seed, 0o700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := runCandidateGit(ctx, seed, "init"); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.MkdirAll(bare, 0o700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := runCandidateGit(ctx, bare, "init", "--bare"); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("offline candidate Hub fixture\n"), 0o600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := runCandidateGit(ctx, seed, "add", "README.md"); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := runCandidateGit(ctx, seed, "-c", "user.name="+hub.AuthorName, "-c", "user.email="+hub.AuthorEmail, "commit", "-m", "candidate Hub fixture"); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := runCandidateGit(ctx, seed, "remote", "add", "origin", bare); err != nil {
		cleanup()
		return "", func() {}, err
	}
	branch := strings.TrimSpace(hub.Branch)
	if branch == "" {
		cleanup()
		return "", func() {}, fmt.Errorf("candidate Hub branch is empty")
	}
	if err := runCandidateGit(ctx, seed, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := runCandidateGit(ctx, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return bare, cleanup, nil
}

func runCandidateGit(ctx context.Context, dir string, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("candidate Hub fixture git %s: %w (%s)", strings.Join(args, " "), err, BoundedOutput(output))
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	const maxBytes = 16 << 10
	n := len(p)
	if b.Len() < maxBytes {
		remaining := maxBytes - b.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
