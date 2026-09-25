package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

type ProjectTokenInput struct {
	Root    string            `json:"root,omitempty"`
	Remote  string            `json:"remote,omitempty"`
	Remotes map[string]string `json:"remotes,omitempty"`
	Project string            `json:"project,omitempty"`
}

type ProjectTokenResult struct {
	ProjectID   string `json:"project_id"`
	ProjectCode string `json:"project_code"`
	Token       string `json:"token"`
	TokenUsage  string `json:"token_usage"`
}

func (s *Service) ProjectToken(ctx context.Context, input ProjectTokenInput) (ProjectTokenResult, error) {
	explicitCode := strings.TrimSpace(input.Project)
	resolution, err := s.EffectiveProjectSnapshot()
	if err != nil {
		if strings.Contains(err.Error(), "duplicate project root") {
			return ProjectTokenResult{}, fmt.Errorf("repository identity is ambiguous")
		}
		return ProjectTokenResult{}, fmt.Errorf("project registry unavailable: %w", err)
	}
	var projectID string
	var project config.ProjectConfig
	var actualRemote string
	if explicitCode != "" {
		if err := model.ValidateProjectCode(explicitCode); err != nil {
			return ProjectTokenResult{}, fmt.Errorf("project code is invalid")
		}
		ids := make([]string, 0, 1)
		for id, registered := range resolution.Projects {
			if strings.EqualFold(registered.ProjectCode, explicitCode) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return ProjectTokenResult{}, fmt.Errorf("project %q is not a registered project", explicitCode)
		}
		if len(ids) != 1 {
			return ProjectTokenResult{}, fmt.Errorf("project code %q is ambiguous", explicitCode)
		}
		projectID = ids[0]
		project = resolution.Projects[projectID]
	} else {
		root := filepath.Clean(strings.TrimSpace(input.Root))
		remotes := make(map[string]string, len(input.Remotes))
		for name, value := range input.Remotes {
			name = strings.TrimSpace(name)
			value = normalizeProjectTokenRemote(value)
			if name == "" || value == "" || strings.ContainsAny(name+value, "\x00\r\n") {
				return ProjectTokenResult{}, fmt.Errorf("project token repository remote is invalid")
			}
			remotes[name] = value
		}
		if len(remotes) == 0 {
			remote := normalizeProjectTokenRemote(input.Remote)
			if remote != "" {
				remotes["origin"] = remote
			}
		}
		if root == "." || !filepath.IsAbs(root) {
			return ProjectTokenResult{}, fmt.Errorf("project token requires an absolute repository root or an explicit project code")
		}
		if len(remotes) == 0 {
			return ProjectTokenResult{}, fmt.Errorf("project token requires a repository remote or an explicit project code")
		}
		ids := make([]string, 0, 1)
		for id, registered := range resolution.Projects {
			if filepath.Clean(registered.Root) == root {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return ProjectTokenResult{}, fmt.Errorf("repository is not a registered project")
		}
		if len(ids) != 1 {
			return ProjectTokenResult{}, fmt.Errorf("repository identity is ambiguous")
		}
		projectID = ids[0]
		project = resolution.Projects[projectID]
		observedRemote, ok := remotes[project.Remote]
		if !ok {
			return ProjectTokenResult{}, fmt.Errorf("repository identity conflicts with the registered project")
		}
		remote, err := s.Git.RemoteURL(ctx, project)
		if err != nil {
			return ProjectTokenResult{}, fmt.Errorf("registered repository remote is unavailable")
		}
		actualRemote = remote
		if normalizeProjectTokenRemote(actualRemote) != observedRemote {
			return ProjectTokenResult{}, fmt.Errorf("repository identity conflicts with the registered project")
		}
	}
	projectCode := project.ProjectCode
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return ProjectTokenResult{}, fmt.Errorf("registered project has no valid project code")
	}
	var grant sqlitestore.SessionBootstrapGrant
	if managed, ok := resolution.ManagedProjects[projectID]; ok {
		if actualRemote == "" {
			remote, err := s.Git.RemoteURL(ctx, project)
			if err != nil {
				return ProjectTokenResult{}, fmt.Errorf("registered repository remote is unavailable")
			}
			actualRemote = remote
		}
		if normalizeProjectTokenRemote(managed.RepositoryURL) != normalizeProjectTokenRemote(actualRemote) {
			return ProjectTokenResult{}, fmt.Errorf("repository identity conflicts with the managed project")
		}
		if s.Durability == nil || s.Durability.Local == nil {
			return ProjectTokenResult{}, fmt.Errorf("local session bootstrap grant store is unavailable")
		}
		grant, err = s.Durability.ReadSessionBootstrapGrant(ctx, projectID)
		if err != nil {
			return ProjectTokenResult{}, fmt.Errorf("managed project bootstrap token is unavailable")
		}
	} else {
		grant, err = s.EnsureProjectSessionBootstrapGrant(ctx, projectID, projectCode)
		if err != nil {
			return ProjectTokenResult{}, err
		}
	}
	if grant.ProjectID != projectID || grant.ProjectCode != projectCode || grant.GatewayID != s.Config.GatewayID || grant.Role != durableSession.RolePlanner || grant.Token == "" {
		return ProjectTokenResult{}, fmt.Errorf("project bootstrap token is unavailable")
	}
	return ProjectTokenResult{
		ProjectID:   projectID,
		ProjectCode: projectCode,
		Token:       grant.Token,
		TokenUsage:  ProjectOnboardTokenUsage,
	}, nil
}

func normalizeProjectTokenRemote(value string) string {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return value
}
