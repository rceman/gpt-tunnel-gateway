package activation

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
)

type DebugSourceStatus struct {
	Root   string `json:"root"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Clean  bool   `json:"clean"`
	Error  string `json:"error,omitempty"`
}

type DebugStatusResult struct {
	GatewayID    string                     `json:"gateway_id"`
	DebugEnabled bool                       `json:"debug_enabled"`
	Source       DebugSourceStatus          `json:"source"`
	Runtime      controller.RuntimeIdentity `json:"runtime"`
}

// DebugStatus reads only configured source metadata and local controller
// runtime identity. It deliberately does not load Hub, Shared, Task, Train,
// Agent, or Journal state.
func DebugStatus(ctx context.Context, c config.Config, configPath string, project config.ProjectConfig) DebugStatusResult {
	source := DebugSourceStatus{Root: project.Root}
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
	return DebugStatusResult{GatewayID: c.GatewayID, DebugEnabled: c.Debug.Enabled, Source: source, Runtime: runtime}
}
