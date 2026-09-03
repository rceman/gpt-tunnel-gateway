package debug

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

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
	source := SourceStatus{Root: project.Root}
	branch, branchErr := gitOutput(ctx, project.Root, "symbolic-ref", "--quiet", "--short", "HEAD")
	source.Branch = branch
	head, headErr := gitOutput(ctx, project.Root, "rev-parse", "--verify", "HEAD^{commit}")
	source.Head = head
	dirty, dirtyErr := gitOutput(ctx, project.Root, "status", "--porcelain", "--untracked-files=all")
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
	runtime := (controller.Controller{Config: c, ConfigPath: configPath}).RuntimeIdentity(ctx)
	return StatusResult{GatewayID: c.GatewayID, DebugEnabled: c.Debug.Enabled, Source: source, Runtime: runtime}
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...).Output()
	return strings.TrimSpace(string(output)), err
}
