package debug

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

const maxGitOutputBytes = 64 << 10

var errGitOutputLimit = errors.New("git output exceeded bounded limit")

type SourceStatus struct {
	Root   string `json:"root"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Clean  bool   `json:"clean"`
	Error  string `json:"error,omitempty"`
}

type StatusResult struct {
	GatewayID    string                     `json:"gateway_id"`
	DebugEnabled bool                       `json:"debug_enabled"`
	Source       SourceStatus               `json:"source"`
	Runtime      controller.RuntimeIdentity `json:"runtime"`
}

// Status is the host-local debug status primitive. It deliberately does not
// load Hub, Shared, Task, Train, Agent, or Journal state.
func Status(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig) StatusResult {
	probeTimeout := time.Duration(c.DispatchTimeoutSeconds) * time.Second
	if probeTimeout <= 0 {
		probeTimeout = time.Second
	}
	probeContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	source := SourceStatus{Root: project.Root}
	branch, branchErr := gitOutput(probeContext, project.Root, "symbolic-ref", "--quiet", "--short", "HEAD")
	source.Branch = branch
	head, headErr := gitOutput(probeContext, project.Root, "rev-parse", "--verify", "HEAD^{commit}")
	source.Head = head
	dirty, dirtyErr := gitOutput(probeContext, project.Root, "status", "--porcelain", "--untracked-files=all")
	source.Clean = branchErr == nil && headErr == nil && dirtyErr == nil && dirty == ""
	if branchErr != nil {
		source.Error = fmt.Sprintf("source branch unavailable: %v", branchErr)
	} else if headErr != nil {
		source.Error = fmt.Sprintf("source HEAD unavailable: %v", headErr)
	} else if dirtyErr != nil {
		source.Error = fmt.Sprintf("source cleanliness unavailable: %v", dirtyErr)
	} else if dirty != "" {
		source.Error = "source worktree is dirty"
	}
	runtime := (controller.Controller{Config: c, ConfigPath: configPath}).RuntimeIdentity(probeContext)
	return StatusResult{GatewayID: c.GatewayID, DebugEnabled: c.Debug.Enabled, Source: source, Runtime: runtime}
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	var output boundedOutput
	var stderr boundedOutput
	output.limit = maxGitOutputBytes
	stderr.limit = maxGitOutputBytes
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	command.Stdout = &output
	command.Stderr = &stderr
	err := command.Run()
	if output.exceeded || stderr.exceeded {
		return strings.TrimSpace(string(output.data)), errGitOutputLimit
	}
	return strings.TrimSpace(string(output.data)), err
}

type boundedOutput struct {
	data     []byte
	limit    int
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-len(b.data) {
		b.exceeded = true
		return 0, errGitOutputLimit
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
