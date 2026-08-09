package service

import (
	"encoding/json"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// MarshalJSON keeps the internal session mapping available to the service
// while ensuring public project status never exposes the raw Airelay key.
func (p ProjectStatus) MarshalJSON() ([]byte, error) {
	type localView struct {
		Remote        string `json:"remote"`
		DefaultBranch string `json:"default_branch"`
	}
	local := localView{
		Remote:        p.Local.Remote,
		DefaultBranch: p.Local.DefaultBranch,
	}
	progress := p.Progress
	if p.Local.Mirror != "" {
		progress.Tail = strings.ReplaceAll(progress.Tail, p.Local.Mirror, "[gateway-internal-path]")
	}
	return json.Marshal(struct {
		Project        interface{}     `json:"project"`
		Local          localView       `json:"local"`
		Worktree       interface{}     `json:"worktree"`
		Plan           interface{}     `json:"plan"`
		HubRevision    string          `json:"hub_revision"`
		Progress       ProjectProgress `json:"progress"`
		WorkflowPolicy interface{}     `json:"workflow_policy"`
	}{
		Project:        p.Project,
		Local:          local,
		Worktree:       p.Worktree,
		Plan:           p.Plan,
		HubRevision:    p.HubRevision,
		Progress:       progress,
		WorkflowPolicy: p.WorkflowPolicy,
	})
}

// PublicProjectConfig is used by future output contracts that need to expose
// local repository metadata without a session identity.
type PublicProjectConfig struct {
	Remote        string `json:"remote"`
	DefaultBranch string `json:"default_branch"`
}

func publicProjectConfig(v config.ProjectConfig) PublicProjectConfig {
	return PublicProjectConfig{
		Remote:        v.Remote,
		DefaultBranch: v.DefaultBranch,
	}
}
