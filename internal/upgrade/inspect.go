package upgrade

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/controller"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type PreflightBlocker struct {
	Code      string `json:"code"`
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail"`
}

type InspectResult struct {
	Status             string             `json:"status"`
	SourceRoot         string             `json:"source_root,omitempty"`
	SourceSHA          string             `json:"source_sha,omitempty"`
	SourceBranch       string             `json:"source_branch,omitempty"`
	TargetVersion      string             `json:"target_version,omitempty"`
	InstalledVersions  map[string]string  `json:"installed_versions"`
	RunningVersion     string             `json:"running_version,omitempty"`
	VersionMatch       bool               `json:"version_match"`
	GatewayPID         int                `json:"gateway_pid,omitempty"`
	TunnelPID          int                `json:"tunnel_pid,omitempty"`
	GatewayReady       bool               `json:"gateway_ready"`
	TunnelReady        bool               `json:"tunnel_ready"`
	HubRevision        string             `json:"hub_revision,omitempty"`
	ConfiguredProjects []string           `json:"configured_projects"`
	DurableProjects    []string           `json:"durable_projects"`
	ValidCurrentPlans  []string           `json:"valid_current_plans"`
	RollbackReady      bool               `json:"rollback_ready"`
	Blockers           []PreflightBlocker `json:"blockers"`
}

func Inspect(ctx context.Context, c config.Config, configPath string) (InspectResult, error) {
	result := InspectResult{Status: "blocked", InstalledVersions: map[string]string{}, ConfiguredProjects: []string{}, DurableProjects: []string{}, ValidCurrentPlans: []string{}, Blockers: []PreflightBlocker{}}
	add := func(code, project, task, run, path, detail string) {
		result.Blockers = append(result.Blockers, PreflightBlocker{Code: code, ProjectID: project, TaskID: task, RunID: run, Path: path, Detail: detail})
	}
	root, sha, sourceErr := sourceRootFn()
	if sourceErr != nil {
		add("SOURCE_UNAVAILABLE", "", "", "", "", sourceErr.Error())
	} else {
		result.SourceRoot, result.SourceSHA = root, sha
		result.SourceBranch, _ = runGit(root, "branch", "--show-current")
		versionBytes, err := os.ReadFile(filepath.Join(root, "VERSION"))
		if err != nil {
			add("TARGET_VERSION_UNAVAILABLE", "", "", "", filepath.Join(root, "VERSION"), err.Error())
		} else {
			result.TargetVersion = strings.TrimSpace(string(versionBytes))
		}
		if err := validateSourceFn(root, sha); err != nil {
			add("SOURCE_NOT_RELEASE_READY", "", "", "", root, err.Error())
		}
		for _, name := range []string{"scripts/build-release.sh", "scripts/static-check.py"} {
			path := filepath.Join(root, name)
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				add("RELEASE_BUILDER_MISSING", "", "", "", path, "required release gate is unavailable")
			}
		}
	}
	installedPaths := map[string]string{}
	home, _ := os.UserHomeDir()
	installedPaths["gpt-tunnel-gatewayd"] = filepath.Join(home, ".local", "bin", "gpt-tunnel-gatewayd")
	installedPaths["gpt-tunnel"] = filepath.Join(home, ".local", "bin", "gpt-tunnel")
	installedPaths["gpt-tunnelctl"] = filepath.Join(home, ".local", "bin", "gpt-tunnelctl")
	for _, name := range binaryOrder {
		path := installedPaths[name]
		v, err := installedVersion(path)
		if err != nil {
			add("INSTALLED_BINARY_INVALID", "", "", "", path, err.Error())
		} else {
			result.InstalledVersions[name] = v
		}
	}
	if len(result.InstalledVersions) == len(binaryOrder) {
		first := result.InstalledVersions[binaryOrder[0]]
		for _, name := range binaryOrder[1:] {
			if result.InstalledVersions[name] != first {
				add("INSTALLED_VERSION_MISMATCH", "", "", "", "", "installed binaries report different versions")
			}
		}
	}
	if err := controller.ValidateTunnelEnv(c.Controller.TunnelEnvFile); err != nil {
		add("TUNNEL_ENV_INVALID", "", "", "", c.Controller.TunnelEnvFile, err.Error())
	}
	ctl := controller.Controller{Config: c, ConfigPath: configPath}
	status, statusErr := ctl.Status(ctx)
	if statusErr != nil {
		add("PROCESS_STATUS_FAILED", "", "", "", "", statusErr.Error())
	} else {
		result.GatewayPID, result.TunnelPID = status.Gateway.PID, status.Tunnel.PID
		result.GatewayReady, result.TunnelReady = status.GatewayReady, status.TunnelReady
		result.RunningVersion, result.VersionMatch = status.RunningVersion, status.VersionMatch
		if status.Gateway.Running && !status.Gateway.IdentityValid {
			add("GATEWAY_PROCESS_IDENTITY_INVALID", "", "", "", "", status.Gateway.IdentityReason)
		}
		if status.Tunnel.Running && !status.Tunnel.IdentityValid {
			add("TUNNEL_PROCESS_IDENTITY_INVALID", "", "", "", "", status.Tunnel.IdentityReason)
		}
		if !status.Gateway.Running || !status.GatewayReady {
			add("GATEWAY_NOT_HEALTHY", "", "", "", "", "gateway must be running and ready before upgrade")
		}
		if !status.Tunnel.Running || !status.TunnelReady {
			add("TUNNEL_NOT_HEALTHY", "", "", "", "", "tunnel must be running and ready before upgrade")
		}
		if status.RunningVersion != "" && status.InstalledVersion != "" && !status.VersionMatch {
			add("INSTALLED_RUNNING_VERSION_MISMATCH", "", "", "", "", "installed and running gateway versions differ")
		}
	}
	s := service.New(c)
	inspectConfiguredProjects(ctx, s.Git, c, add)
	state, stateErr := s.StateCheck(ctx)
	if stateErr != nil {
		add("STATE_CHECK_FAILED", "", "", "", "", stateErr.Error())
	} else {
		result.HubRevision = state.HubRevision
		result.ConfiguredProjects = append(result.ConfiguredProjects, state.ConfiguredProjectIDs...)
		result.DurableProjects = append(result.DurableProjects, state.DurableProjectIDs...)
		result.ValidCurrentPlans = append(result.ValidCurrentPlans, state.ValidCurrentPlans...)
		for _, issue := range state.Issues {
			add(issue.Code, issue.ProjectID, issue.TaskID, issue.RunID, issue.Path, issue.Detail)
		}
	}
	if sourceErr == nil && result.TargetVersion != "" {
		if parsed, err := parseVersion(result.TargetVersion); err != nil {
			add("TARGET_VERSION_INVALID", "", "", "", filepath.Join(root, "VERSION"), err.Error())
		} else if len(result.InstalledVersions) > 0 && compareVersion(parsed, result.InstalledVersions["gpt-tunnelctl"]) <= 0 {
			add("TARGET_NOT_NEWER", "", "", "", "", "target version is not newer than installed version")
		}
	}
	if result.HubRevision == "" {
		add("HUB_REVISION_UNAVAILABLE", "", "", "", "", "authoritative hub revision unavailable")
	}
	result.RollbackReady = result.TunnelPID > 0 && result.HubRevision != ""
	sort.Strings(result.ConfiguredProjects)
	sort.Strings(result.DurableProjects)
	sort.Strings(result.ValidCurrentPlans)
	if len(result.Blockers) == 0 {
		result.Status = "ready"
	}
	return result, nil
}

func inspectConfiguredProjects(ctx context.Context, git gitx.Runner, c config.Config, add func(string, string, string, string, string, string)) {
	ids := make([]string, 0, len(c.Projects))
	for id := range c.Projects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		project := c.Projects[id]
		if info, err := os.Stat(project.Root); err != nil || !info.IsDir() {
			add("PROJECT_ROOT_INVALID", id, "", "", project.Root, "configured project root is not an accessible directory")
			continue
		}
		if remote, err := git.RemoteURL(ctx, project); err != nil || remote == "" {
			add("PROJECT_REMOTE_UNAVAILABLE", id, "", "", project.Root, "configured project remote is unavailable")
		}
		if branch, err := git.RemoteDefaultBranch(ctx, project); err != nil {
			add("PROJECT_DEFAULT_BRANCH_UNRESOLVED", id, "", "", project.Root, "configured project remote HEAD is unavailable")
		} else if branch != project.DefaultBranch {
			add("PROJECT_DEFAULT_BRANCH_MISMATCH", id, "", "", project.Root, "configured default branch does not match remote HEAD")
		}
	}
}

func inspectLegacyPlans(ctx context.Context, c config.Config) []PreflightBlocker {
	// Kept as a small helper for future migration previews. Strict reads in
	// StateCheck already identify the blocker; this function only classifies a
	// raw legacy body when the caller has an explicit migration preview.
	result := []PreflightBlocker{}
	store := service.New(c).Hub
	for id := range c.Projects {
		path := "gpt-tunnel/v1/projects/" + id + "/plan/current.json"
		data, err := store.ReadFile(ctx, path)
		if err != nil {
			continue
		}
		var obj map[string]any
		if json.Unmarshal(data, &obj) == nil {
			if _, ok := obj["body"]; ok {
				result = append(result, PreflightBlocker{Code: "LEGACY_PLAN_BODY", ProjectID: id, Path: path, Detail: "workflow-v1 plan contains obsolete body field"})
			}
		}
	}
	return result
}

var _ = inspectLegacyPlans
