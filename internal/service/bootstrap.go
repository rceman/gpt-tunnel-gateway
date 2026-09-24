package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	workflowSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/workflowrole"
)

type ProjectOnboardInput struct {
	Root        string `json:"root,omitempty"`
	ProjectCode string `json:"project_code"`
	WorkerRelay string `json:"worker_relay,omitempty"`
	LeadRelay   string `json:"lead_relay,omitempty"`
}

const ProjectOnboardTokenUsage = "Use this project token to start new Planner Sessions. It grants the highest project-scoped semantic authority for this project; keep it secret."

type ProjectOnboardResult struct {
	ProjectID     string                 `json:"project_id"`
	ProjectCode   string                 `json:"project_code"`
	Root          string                 `json:"root"`
	Remote        string                 `json:"remote"`
	DefaultBranch string                 `json:"default_branch"`
	Status        string                 `json:"status"`
	Token         string                 `json:"token,omitempty"`
	TokenUsage    string                 `json:"token_usage,omitempty"`
	Agents        []AgentBootstrapResult `json:"agents,omitempty"`
}

type AgentBootstrapInput struct {
	ProjectID string `json:"project_id"`
	AgentID   string `json:"agent_id,omitempty"`
	Role      string `json:"role"`
	Relay     string `json:"relay"`
}

type AgentBootstrapResult struct {
	ProjectID string `json:"project_id"`
	AgentID   string `json:"agent_id"`
	Role      string `json:"role"`
	Relay     string `json:"relay"`
	Status    string `json:"status"`
}

func (s *Service) ProjectOnboard(ctx context.Context, in ProjectOnboardInput) (ProjectOnboardResult, error) {
	if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
		return ProjectOnboardResult{}, err
	}
	identity, err := s.discoverOnboardProject(ctx, in.Root, in.ProjectCode)
	if err != nil {
		return ProjectOnboardResult{}, err
	}
	if existing, ok := identity.registry.Projects[identity.projectID]; ok {
		if err := validateOnboardEntry(identity.projectID, existing, identity.entry); err != nil {
			return ProjectOnboardResult{}, err
		}
		if err := s.reconcileOnboardedProjectShared(ctx, identity.projectID, in.ProjectCode); err != nil {
			return ProjectOnboardResult{}, err
		}
		if err := s.verifyOnboardedProject(ctx, identity.projectID, in.ProjectCode); err != nil {
			return ProjectOnboardResult{}, err
		}
		grant, err := s.ensureOnboardSessionBootstrapGrant(ctx, identity.projectID, in.ProjectCode)
		if err != nil {
			return ProjectOnboardResult{}, err
		}
		result := identity.result("already_registered")
		result.Token = grant.Token
		if grant.Token != "" {
			result.TokenUsage = ProjectOnboardTokenUsage
		}
		return s.registerOnboardAgents(ctx, result, in.WorkerRelay, in.LeadRelay)
	}
	if static, ok := s.Config.Projects[identity.projectID]; ok {
		if filepath.Clean(static.Root) != identity.entry.Root || static.ProjectCode != in.ProjectCode {
			return ProjectOnboardResult{}, fmt.Errorf("project %q conflicts with existing static project authority", identity.projectID)
		}
		return ProjectOnboardResult{}, fmt.Errorf("project %q is statically configured; managed onboarding would create duplicate authority", identity.projectID)
	}

	hubRevision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		return ProjectOnboardResult{}, fmt.Errorf("read Hub revision: %w", err)
	}
	registered, err := s.projectRegister(ctx, ProjectRegisterInput{
		Project: model.Project{ID: identity.projectID, RepositoryURL: identity.entry.RepositoryURL, DefaultBranch: identity.entry.DefaultBranch, Status: "active"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: hubRevision,
		},
	}, true)
	if err != nil {
		return ProjectOnboardResult{}, err
	}
	_, adopted, err := s.ProjectIdentifiersAdopt(ctx, ProjectIdentifiersAdoptInput{
		ProjectID:   identity.projectID,
		ProjectCode: in.ProjectCode,
		WriteOptions: WriteOptions{
			ExpectedHubRevision: registered.Hub.After,
		},
	})
	if err != nil {
		_ = s.rollbackOnboardHub(ctx, registered.Hub.After, identity.projectID)
		return ProjectOnboardResult{}, err
	}
	if _, err := config.WriteManagedProjectRegistry(s.Config.StateDir, identity.digest, identity.nextRegistry); err != nil {
		_ = s.rollbackOnboardHub(ctx, adopted.Hub.After, identity.projectID)
		return ProjectOnboardResult{}, fmt.Errorf("publish managed project registry: %w", err)
	}
	if err := s.reconcileOnboardedProjectShared(ctx, identity.projectID, in.ProjectCode); err != nil {
		return ProjectOnboardResult{}, err
	}
	grant, err := s.ensureOnboardSessionBootstrapGrant(ctx, identity.projectID, in.ProjectCode)
	if err != nil {
		return ProjectOnboardResult{}, err
	}
	result := identity.result("onboarded")
	result.Token = grant.Token
	if grant.Token != "" {
		result.TokenUsage = ProjectOnboardTokenUsage
	}
	return s.registerOnboardAgents(ctx, result, in.WorkerRelay, in.LeadRelay)
}

type onboardIdentity struct {
	projectID    string
	entry        config.ManagedProjectEntry
	registry     config.ManagedProjectRegistry
	digest       string
	nextRegistry config.ManagedProjectRegistry
}

func (s *Service) discoverOnboardProject(ctx context.Context, root, code string) (onboardIdentity, error) {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return onboardIdentity{}, fmt.Errorf("resolve current directory: %w", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return onboardIdentity{}, fmt.Errorf("resolve project root: %w", err)
	}
	resolvedRoot, err := s.Git.RepositoryRoot(ctx, root)
	if err != nil {
		return onboardIdentity{}, err
	}
	resolvedRoot, err = filepath.EvalSymlinks(resolvedRoot)
	if err != nil {
		return onboardIdentity{}, fmt.Errorf("canonicalize project root: %w", err)
	}
	projectID := filepath.Base(resolvedRoot)
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return onboardIdentity{}, fmt.Errorf("repository basename %q is not a valid project identifier: %w", projectID, err)
	}
	local := config.ProjectConfig{Root: resolvedRoot, Remote: "origin"}
	remotes, err := s.Git.RemoteNames(ctx, local)
	if err != nil {
		return onboardIdentity{}, err
	}
	foundOrigin := false
	for _, remote := range remotes {
		if remote == local.Remote {
			foundOrigin = true
			break
		}
	}
	if !foundOrigin {
		return onboardIdentity{}, fmt.Errorf("repository must have an origin remote")
	}
	repositoryURL, err := s.Git.RemoteURL(ctx, local)
	if err != nil || strings.TrimSpace(repositoryURL) == "" {
		return onboardIdentity{}, fmt.Errorf("resolve origin URL: %w", err)
	}
	status, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return onboardIdentity{}, fmt.Errorf("inspect project worktree: %w", err)
	}
	if !status.Clean || status.Branch == "" || status.Branch == "(detached)" {
		return onboardIdentity{}, fmt.Errorf("project repository must be clean and on a named branch")
	}
	defaultBranch, err := s.Git.RemoteDefaultBranch(ctx, local)
	if err != nil {
		return onboardIdentity{}, fmt.Errorf("resolve origin default branch: %w", err)
	}
	if err := model.ValidateBranch(defaultBranch); err != nil {
		return onboardIdentity{}, err
	}
	registry, err := config.LoadManagedProjects(s.Config.StateDir)
	if err != nil {
		return onboardIdentity{}, err
	}
	digest, err := registry.Digest()
	if err != nil {
		return onboardIdentity{}, err
	}
	entry := config.ManagedProjectEntry{
		Root: resolvedRoot, RepositoryURL: repositoryURL, Remote: local.Remote,
		DefaultBranch: defaultBranch, ProjectCode: code,
	}
	if err := entry.Validate(projectID); err != nil {
		return onboardIdentity{}, err
	}
	for id, static := range s.Config.Projects {
		if filepath.Clean(static.Root) == resolvedRoot && id != projectID {
			return onboardIdentity{}, fmt.Errorf("repository root is already bound to project %q", id)
		}
	}
	next := registry
	next.Projects = cloneManagedProjects(registry.Projects)
	next.Projects[projectID] = entry
	next.Revision = registry.Revision + 1
	return onboardIdentity{
		projectID:    projectID,
		entry:        entry,
		registry:     registry,
		digest:       digest,
		nextRegistry: next,
	}, nil
}

func (i onboardIdentity) result(status string) ProjectOnboardResult {
	return ProjectOnboardResult{
		ProjectID:     i.projectID,
		ProjectCode:   i.entry.ProjectCode,
		Root:          i.entry.Root,
		Remote:        i.entry.Remote,
		DefaultBranch: i.entry.DefaultBranch,
		Status:        status,
	}
}

func validateOnboardEntry(projectID string, actual, expected config.ManagedProjectEntry) error {
	if actual.Root != expected.Root || actual.RepositoryURL != expected.RepositoryURL || actual.Remote != expected.Remote || actual.DefaultBranch != expected.DefaultBranch || actual.ProjectCode != expected.ProjectCode {
		return fmt.Errorf("managed project %q conflicts with repository identity or project code", projectID)
	}
	return nil
}

func (s *Service) reconcileOnboardedProjectShared(ctx context.Context, projectID, code string) error {
	if s.Durability == nil {
		return nil
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read Hub project identifiers: %w", err)
	}
	if identifiers.ProjectCode != code {
		return fmt.Errorf("Hub project code %q conflicts with requested %q", identifiers.ProjectCode, code)
	}
	configuration, err := s.migrateHubProjectConfiguration(ctx, projectID)
	if err != nil {
		return fmt.Errorf("migrate Hub project configuration: %w", err)
	}
	if err := s.Durability.ReconcileProjectBootstrap(ctx, sqlitestore.ProjectBootstrapUpdate{
		ProjectID: projectID, PreviousProjectCode: code, ProjectCode: code,
		HubIdentifiers: identifiers, Configuration: configuration,
	}); err != nil {
		return fmt.Errorf("reconcile Shared project bootstrap: %w", err)
	}
	if err := s.restoreHubProjectSemantics(ctx, projectID, code); err != nil {
		return fmt.Errorf("restore portable Shared project semantics: %w", err)
	}
	return nil
}

func (s *Service) verifyOnboardedProject(ctx context.Context, projectID, code string) error {
	project, err := s.ProjectRead(ctx, projectID)
	if err != nil {
		return fmt.Errorf("managed project %q has no matching durable project: %w", projectID, err)
	}
	if project.ID != projectID {
		return fmt.Errorf("durable project identity mismatch for %q", projectID)
	}
	identifiers, err := s.ProjectIdentifiersRead(ctx, projectID)
	if err != nil {
		return fmt.Errorf("read onboarded project identifiers: %w", err)
	}
	if identifiers.ProjectCode != code {
		return fmt.Errorf("durable project code %q conflicts with requested %q", identifiers.ProjectCode, code)
	}
	if _, err := s.EffectiveProjectConfig(projectID); err != nil {
		return fmt.Errorf("managed project %q has no effective local configuration: %w", projectID, err)
	}
	if s.Durability != nil {
		sharedIdentifiers, err := s.Durability.ReadSharedProjectIdentifiers(ctx, projectID)
		if err != nil {
			return fmt.Errorf("Shared project identifiers are unavailable: %w", err)
		}
		if sharedIdentifiers.ProjectCode != code {
			return fmt.Errorf("Shared project code %q conflicts with requested %q", sharedIdentifiers.ProjectCode, code)
		}
		if _, err := s.ProjectConfigurationRead(ctx, projectID); err != nil {
			return fmt.Errorf("Shared project configuration is unavailable: %w", err)
		}
		if _, err := s.ProjectWorkflowPolicyRead(ctx, projectID); err != nil {
			return fmt.Errorf("Shared workflow policy is unavailable: %w", err)
		}
	}
	return nil
}

func (s *Service) registerOnboardAgents(ctx context.Context, result ProjectOnboardResult, workerRelay, leadRelay string) (ProjectOnboardResult, error) {
	for _, item := range []struct{ role, relay string }{{workflowSession.RoleWorker, workerRelay}, {workflowSession.RoleLead, leadRelay}} {
		if strings.TrimSpace(item.relay) == "" {
			continue
		}
		agent, err := s.AgentBootstrap(ctx, AgentBootstrapInput{
			ProjectID: result.ProjectID,
			Role:      item.role,
			Relay:     item.relay,
		})
		if err != nil {
			return ProjectOnboardResult{}, err
		}
		result.Agents = append(result.Agents, agent)
	}
	return result, nil
}

func cloneManagedProjects(input map[string]config.ManagedProjectEntry) map[string]config.ManagedProjectEntry {
	output := make(map[string]config.ManagedProjectEntry, len(input)+1)
	for id, entry := range input {
		output[id] = entry
	}
	return output
}

func (s *Service) rollbackOnboardHub(ctx context.Context, expected, projectID string) error {
	_, err := s.Hub.Transact(ctx, expected, "gateway: rollback project onboarding "+projectID, func(worktree string) ([]string, error) {
		paths := []string{s.projectPath(projectID), s.projectConfigurationPath(projectID), s.projectIdentifiersPath(projectID)}
		for _, path := range paths {
			if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(path))); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
		return paths, nil
	})
	return err
}

func (s *Service) validateLegacyAgentWorkflowRole(projectID, relay, requestedRole string) error {
	if s.Durability == nil || s.Durability.Local == nil {
		return nil
	}
	records, err := workflowSession.NewStoreWithDurability(s.Durability).List()
	if err != nil {
		return fmt.Errorf("read durable Session authority: %w", err)
	}
	for _, record := range records {
		if record.Status != workflowSession.StatusActive || record.ProjectID != projectID || record.SessionRef == nil || *record.SessionRef != relay {
			continue
		}
		role, ok := workflowrole.ByKey(record.Role)
		if !ok {
			return fmt.Errorf("active Session %q has unsupported workflow role %q", record.ID, record.Role)
		}
		if role.Key != requestedRole {
			return fmt.Errorf("legacy Agent workflow role conflicts with active Session role %q", role.Key)
		}
	}
	return nil
}

func (s *Service) AgentBootstrap(ctx context.Context, in AgentBootstrapInput) (AgentBootstrapResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return AgentBootstrapResult{}, err
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	if role == "" {
		role = workflowSession.RoleWorker
	}
	workflow, ok := workflowSession.WorkflowRoleByKey(role)
	if !ok || (role != workflowSession.RoleWorker && role != workflowSession.RoleLead) || !workflow.ManagedRuntime {
		return AgentBootstrapResult{}, fmt.Errorf("unsupported bootstrap Agent role %q", role)
	}
	relay := strings.TrimSpace(in.Relay)
	binding := config.AgentBinding{SessionKey: relay}
	if err := binding.Validate(); err != nil {
		return AgentBootstrapResult{}, err
	}
	if _, err := s.Airelay.ResolveSessionAuthority(ctx, relay, false); err != nil {
		return AgentBootstrapResult{}, fmt.Errorf("resolve existing Airelay relay %q: %w", relay, err)
	}
	project, err := s.EffectiveProjectConfig(in.ProjectID)
	if err != nil {
		return AgentBootstrapResult{}, err
	}
	if project.ProjectCode == "" {
		identifiers, readErr := s.ProjectIdentifiersRead(ctx, in.ProjectID)
		if readErr != nil {
			return AgentBootstrapResult{}, readErr
		}
		project.ProjectCode = identifiers.ProjectCode
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		agentID = project.ProjectCode + "-" + strings.ToUpper(role)
	}
	if err := model.ValidateObjectIdentifier(agentID); err != nil {
		return AgentBootstrapResult{}, err
	}
	if existingBinding, found := s.agentBinding(in.ProjectID, agentID); found && existingBinding.SessionKey != relay {
		return AgentBootstrapResult{}, fmt.Errorf("Agent %q is already bound to relay %q", agentID, existingBinding.SessionKey)
	}
	agent, readErr := s.AgentRead(ctx, in.ProjectID, agentID)
	status := "registered"
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return AgentBootstrapResult{}, readErr
	}
	if readErr != nil {
		agent, _, err = s.AgentRegister(ctx, AgentRegisterInput{
			ProjectID:    in.ProjectID,
			AgentID:      agentID,
			WorkflowRole: role,
		})
		if err != nil {
			return AgentBootstrapResult{}, err
		}
	} else {
		if agent.ProjectID != in.ProjectID || agent.AgentID != agentID || agent.Role != model.AgentRoleCoding || !agent.Enabled {
			return AgentBootstrapResult{}, fmt.Errorf("existing Agent %q has conflicting identity or state", agentID)
		}
		if err := validatePortableAgentWorkflowRole(agent.WorkflowRole); err != nil {
			return AgentBootstrapResult{}, err
		}
		if agent.WorkflowRole != role {
			if agent.WorkflowRole == "" {
				if err := s.validateLegacyAgentWorkflowRole(in.ProjectID, relay, role); err != nil {
					return AgentBootstrapResult{}, err
				}
				agent, err = s.ensurePortableAgentWorkflowRole(ctx, agent, role)
				if err != nil {
					return AgentBootstrapResult{}, err
				}
			} else {
				return AgentBootstrapResult{}, fmt.Errorf("Agent %q is already assigned workflow role %q", agentID, agent.WorkflowRole)
			}
		}
		status = "already_registered"
	}
	if err := s.persistAgentBinding(in.ProjectID, agentID, binding); err != nil {
		return AgentBootstrapResult{}, err
	}
	return AgentBootstrapResult{
		ProjectID: in.ProjectID,
		AgentID:   agent.AgentID,
		Role:      role,
		Relay:     relay,
		Status:    status,
	}, nil
}

func (s *Service) persistAgentBinding(projectID, agentID string, binding config.AgentBinding) error {
	path := s.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if existing, found := s.agentBinding(projectID, agentID); found && existing != binding {
			return fmt.Errorf("agent binding conflict for %q/%q", projectID, agentID)
		}
		if s.Config.ProjectAgentBindings == nil {
			s.Config.ProjectAgentBindings = map[string]map[string]config.AgentBinding{}
		}
		if s.Config.ProjectAgentBindings[projectID] == nil {
			s.Config.ProjectAgentBindings[projectID] = map[string]config.AgentBinding{}
		}
		s.Config.ProjectAgentBindings[projectID][agentID] = binding
		return nil
	}
	return s.persistAgentBindingWithRefresh(path, projectID, agentID, binding, s.refreshHostConfig)
}

func (s *Service) persistAgentBindingWithRefresh(path, projectID, agentID string, binding config.AgentBinding, refresh func() error) error {
	previousConfig, err := json.Marshal(s.Config)
	if err != nil {
		return fmt.Errorf("snapshot live host configuration: %w", err)
	}
	original, err := config.UpdateAgentBinding(path, projectID, agentID, nil, binding)
	if err != nil {
		return err
	}
	if err := refresh(); err != nil {
		var restoredConfig config.Config
		if inMemoryErr := json.Unmarshal(previousConfig, &restoredConfig); inMemoryErr != nil {
			return fmt.Errorf("live host configuration refresh failed: %v; restore in-memory configuration failed: %w", err, inMemoryErr)
		}
		s.Config = restoredConfig
		if restoreErr := config.Restore(path, original); restoreErr != nil {
			return fmt.Errorf("live host configuration refresh failed: %v; restore failed: %w", err, restoreErr)
		}
		return fmt.Errorf("live host configuration refresh failed; configuration restored: %w", err)
	}
	return nil
}
