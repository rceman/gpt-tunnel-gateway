package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

type AdminProjectOnboardInput struct {
	Repository string `json:"repository"`
	Code       string `json:"code"`
	Harness    string `json:"harness,omitempty"`
}

type AdminWorkerReadiness struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

type AdminProjectOnboardResult struct {
	ProjectID string               `json:"project_id"`
	Status    string               `json:"status"`
	Worker    AdminWorkerReadiness `json:"worker"`
}

type adminRepository struct {
	Owner     string
	Name      string
	ProjectID string
	URL       string
}

var adminRepositoryRE = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?)/([A-Za-z0-9][A-Za-z0-9_-]{0,63})$`)
var adminHarnessRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var adminSSHRepositoryRE = regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`)

func (s *Service) AdminProjectOnboard(ctx context.Context, in AdminProjectOnboardInput) (AdminProjectOnboardResult, error) {
	if err := requireLinuxAdmin(); err != nil {
		return AdminProjectOnboardResult{}, err
	}
	repository, err := s.validateAdminRepository(in.Repository)
	if err != nil {
		return AdminProjectOnboardResult{}, err
	}
	if err := model.ValidateProjectCode(in.Code); err != nil {
		return AdminProjectOnboardResult{}, err
	}
	harness, err := s.adminHarness(in.Harness)
	if err != nil {
		return AdminProjectOnboardResult{}, err
	}
	root, err := s.ensureAdminRepository(ctx, repository)
	if err != nil {
		s.recordAdminOnboardingEvent("", "failure", "repository_materialization_failed")
		return AdminProjectOnboardResult{}, fmt.Errorf("repository onboarding failed")
	}
	project, err := s.ProjectOnboard(ctx, ProjectOnboardInput{
		Root:        root,
		ProjectCode: in.Code,
	})
	if err != nil {
		s.recordAdminOnboardingEvent(repository.ProjectID, "failure", "project_onboarding_failed")
		return AdminProjectOnboardResult{}, fmt.Errorf("project onboarding failed")
	}
	workerKey := repository.ProjectID + "_worker"
	worker := AdminWorkerReadiness{
		Key:    workerKey,
		Status: "not_ready",
	}
	if err := s.ensureAdminWorker(ctx, project.ProjectID, workerKey, harness); err != nil {
		s.recordAdminOnboardingEvent(project.ProjectID, "partial", "worker_not_ready")
		return AdminProjectOnboardResult{
			ProjectID: project.ProjectID,
			Status:    "partial",
			Worker:    worker,
		}, nil
	}
	worker.Status = "ready"
	s.recordAdminOnboardingEvent(project.ProjectID, "ready", "worker_ready")
	return AdminProjectOnboardResult{
		ProjectID: project.ProjectID,
		Status:    "ready",
		Worker:    worker,
	}, nil
}

func (s *Service) validateAdminRepository(value string) (adminRepository, error) {
	if value == "" || !adminRepositoryRE.MatchString(value) {
		return adminRepository{}, fmt.Errorf("repository must be canonical GitHub owner/name")
	}
	parts := adminRepositoryRE.FindStringSubmatch(value)
	owner, name := strings.ToLower(parts[1]), strings.ToLower(parts[2])
	allowed := false
	for _, candidate := range s.Config.Admin.GitHubAllowedOwners {
		if strings.EqualFold(candidate, owner) {
			allowed = true
			break
		}
	}
	if !allowed {
		return adminRepository{}, fmt.Errorf("repository owner is not allowed")
	}
	if err := model.ValidateProjectIdentifier(name); err != nil {
		return adminRepository{}, fmt.Errorf("repository name is not a supported project identity")
	}
	return adminRepository{
		Owner:     owner,
		Name:      name,
		ProjectID: name,
		URL:       "git@github.com:" + owner + "/" + name + ".git",
	}, nil
}

func (s *Service) adminHarness(value string) (string, error) {
	harness := strings.TrimSpace(value)
	if harness == "" {
		harness = "codex"
	}
	if !adminHarnessRE.MatchString(harness) {
		return "", fmt.Errorf("invalid Worker harness")
	}
	if len(s.Config.Admin.WorkerHarnesses) == 0 {
		if harness != "codex" {
			return "", fmt.Errorf("Worker harness is not configured")
		}
		return harness, nil
	}
	for _, configured := range s.Config.Admin.WorkerHarnesses {
		if configured == harness {
			return harness, nil
		}
	}
	return "", fmt.Errorf("Worker harness is not configured")
}

func (s *Service) adminOnboardingRoot() (string, error) {
	root := s.Config.Admin.OnboardingRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, "git")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsAny(root, "\x00\r\n") {
		return "", fmt.Errorf("invalid onboarding root")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("onboarding root is not a directory")
	}
	return filepath.Clean(resolved), nil
}

func (s *Service) ensureAdminRepository(ctx context.Context, repository adminRepository) (string, error) {
	root, err := s.adminOnboardingRoot()
	if err != nil {
		return "", err
	}
	destination := filepath.Join(root, repository.ProjectID)
	if filepath.Dir(destination) != root {
		return "", fmt.Errorf("invalid derived onboarding path")
	}
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		if err := s.Git.Clone(ctx, repository.URL, destination); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	} else {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("derived onboarding path conflicts")
		}
		resolved, err := filepath.EvalSymlinks(destination)
		if err != nil || filepath.Clean(resolved) != destination {
			return "", fmt.Errorf("derived onboarding path is not canonical")
		}
	}
	project := config.ProjectConfig{Root: destination, Remote: "origin"}
	remote, err := s.Git.RemoteURL(ctx, project)
	if err != nil {
		return "", err
	}
	if !sameGitHubRepository(remote, repository) {
		return "", fmt.Errorf("repository remote conflicts")
	}
	hasCommit, err := s.Git.RepositoryHasCommit(ctx, project)
	if err != nil {
		return "", err
	}
	if !hasCommit {
		if err := s.Git.BootstrapEmpty(ctx, project, "# "+repository.ProjectID+"\n"); err != nil {
			return "", err
		}
	}
	status, err := s.Git.WorktreeStatus(ctx, project)
	if err != nil {
		return "", err
	}
	if !status.Clean || status.Branch == "" || status.Branch == "(detached)" {
		return "", fmt.Errorf("repository state is unsafe")
	}
	return destination, nil
}

func sameGitHubRepository(value string, expected adminRepository) bool {
	owner, name, ok := parseGitHubRemote(value)
	return ok && strings.EqualFold(owner, expected.Owner) && strings.EqualFold(name, expected.Name)
}

func parseGitHubRemote(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if matches := adminSSHRepositoryRE.FindStringSubmatch(value); len(matches) == 3 {
		return matches[1], matches[2], true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", false
	}
	if parsed.Scheme != "https" {
		return "", "", false
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *Service) ensureAdminWorker(ctx context.Context, projectID, workerKey, harness string) error {
	ensured, err := s.Airelay.EnsureDetached(ctx, harness, workerKey)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		if !ensured.Started {
			return cause
		}
		if stopErr := s.Airelay.StopDetached(context.Background(), workerKey); stopErr != nil {
			return fmt.Errorf("%w; detached Worker rollback failed", cause)
		}
		return cause
	}
	s.EnableHostConfigRefresh()
	if _, err := s.AgentBootstrap(ctx, AgentBootstrapInput{
		ProjectID: projectID,
		Role:      durableSession.RoleWorker,
		Relay:     workerKey,
	}); err != nil {
		return rollback(err)
	}
	if _, err := s.Airelay.ResolveSessionAuthority(ctx, workerKey, true); err != nil {
		return rollback(err)
	}
	return nil
}

func (s *Service) recordAdminOnboardingEvent(projectID, status, message string) {
	_ = runtime_log.New(s.Config.StateDir).Append(runtime_log.Event{
		Timestamp: time.Now().UTC(),
		Level:     "info",
		Component: "admin",
		Event:     "admin_project_onboard",
		Action:    "admin/project/onboard",
		ProjectID: projectID,
		Message:   status + ":" + message,
	})
}
