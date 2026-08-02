package service

import (
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

// MarshalJSON keeps the internal session mapping available to the service
// while ensuring public project status never exposes the raw Airelay key.
func (p ProjectStatus) MarshalJSON() ([]byte, error) {
	type localView struct {
		Root          string `json:"root"`
		Mirror        string `json:"mirror"`
		Remote        string `json:"remote"`
		DefaultBranch string `json:"default_branch"`
	}
	local := localView{Root: p.Local.Root, Mirror: p.Local.Mirror, Remote: p.Local.Remote, DefaultBranch: p.Local.DefaultBranch}
	return json.Marshal(struct {
		Project     interface{}     `json:"project"`
		Local       localView       `json:"local"`
		Worktree    interface{}     `json:"worktree"`
		Plan        interface{}     `json:"plan"`
		HubRevision string          `json:"hub_revision"`
		Progress    ProjectProgress `json:"progress"`
	}{Project: p.Project, Local: local, Worktree: p.Worktree, Plan: p.Plan, HubRevision: p.HubRevision, Progress: p.Progress})
}

// PublicProjectConfig is used by future output contracts that need to expose
// local repository metadata without a session identity.
type PublicProjectConfig struct {
	Root          string `json:"root"`
	Mirror        string `json:"mirror"`
	Remote        string `json:"remote"`
	DefaultBranch string `json:"default_branch"`
}

func publicProjectConfig(v config.ProjectConfig) PublicProjectConfig {
	return PublicProjectConfig{Root: v.Root, Mirror: v.Mirror, Remote: v.Remote, DefaultBranch: v.DefaultBranch}
}
