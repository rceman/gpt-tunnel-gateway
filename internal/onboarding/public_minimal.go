package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// MinimalInput is the server-owned top-level onboarding contract. All
// repository, Hub, registry, workflow and Agent details are derived here.
type MinimalInput struct {
	ProjectID        string `json:"project_id"`
	Root             string `json:"root"`
	ProjectCode      string `json:"project_code,omitempty"`
	InitialObjective string `json:"initial_objective,omitempty"`
}

type MinimalResult struct {
	ProjectID       string       `json:"project_id"`
	ProjectCode     string       `json:"project_code"`
	State           ReceiptState `json:"state"`
	Activated       bool         `json:"activated"`
	RepositoryURL   string       `json:"repository_url"`
	DefaultBranch   string       `json:"default_branch"`
	Head            string       `json:"head"`
	Clean           bool         `json:"clean"`
	AgentSessionKey string       `json:"agent_session_key"`
	AgentReady      bool         `json:"agent_ready"`
	NextStep        string       `json:"next_step"`
	OperationID     string       `json:"operation_id"`
}

func (o *PublicOrchestrator) OnboardMinimal(ctx context.Context, in MinimalInput) (MinimalResult, error) {
	if err := validateMinimalInput(in); err != nil {
		return MinimalResult{}, err
	}
	request, status, err := o.minimalRequest(ctx, in)
	if err != nil {
		return MinimalResult{}, err
	}
	operationID, err := model.NewID()
	if err != nil {
		return MinimalResult{}, fmt.Errorf("allocate onboarding operation: %w", err)
	}
	result, err := o.Onboard(ctx, PublicInput{
		OperationID: operationID,
		Request:     request,
	})
	if err != nil {
		return MinimalResult{}, err
	}
	return MinimalResult{
		ProjectID:       request.ProjectID,
		ProjectCode:     request.ProjectCode,
		State:           result.State,
		Activated:       result.State == StateActivated,
		RepositoryURL:   request.RepositoryURL,
		DefaultBranch:   request.DefaultBranch,
		Head:            status.Head,
		Clean:           status.Clean,
		AgentSessionKey: sessionKey(request),
		AgentReady:      false,
		NextStep:        "session_start(project=" + request.ProjectCode + ")",
		OperationID:     result.OperationID,
	}, nil
}

func validateMinimalInput(in MinimalInput) error {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return fmt.Errorf("invalid project_id: %w", err)
	}
	if in.Root == "" || !filepath.IsAbs(in.Root) {
		return fmt.Errorf("root must be an absolute path")
	}
	if len(in.InitialObjective) > 20000 || strings.ContainsAny(in.InitialObjective, "\x00\r\n") {
		return fmt.Errorf("initial_objective is invalid")
	}
	if in.ProjectCode != "" {
		if err := model.ValidateProjectCode(in.ProjectCode); err != nil {
			return err
		}
	}
	return nil
}

func (o *PublicOrchestrator) minimalRequest(ctx context.Context, in MinimalInput) (Request, gitx.WorktreeStatus, error) {
	root, err := filepath.EvalSymlinks(filepath.Clean(in.Root))
	if err != nil {
		return Request{}, gitx.WorktreeStatus{}, fmt.Errorf("inspect root: %w", err)
	}
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return Request{}, gitx.WorktreeStatus{}, fmt.Errorf("root is not a directory")
	}
	runner := gitx.Runner{MaxReadBytes: o.Hub.Config.MaxReadBytes, MaxDiffBytes: o.Hub.Config.MaxDiffBytes, MaxListItems: o.Hub.Config.MaxListItems}
	local := config.ProjectConfig{Root: root, Remote: "origin", DefaultBranch: "main", Mirror: config.ManagedProjectMirrorPath(o.StateDir, in.ProjectID)}
	status, err := runner.WorktreeStatus(ctx, local)
	if err != nil || status.Head == "" || status.Branch == "" || status.Branch == "(detached)" || !status.Clean {
		return Request{}, status, fmt.Errorf("root is not a clean Git worktree")
	}
	repositoryURL, err := runner.RemoteURL(ctx, local)
	if err != nil {
		return Request{}, status, fmt.Errorf("inspect Git remote: %w", err)
	}
	branch := status.Branch
	if remoteBranch, branchErr := runner.RemoteDefaultBranch(ctx, local); branchErr == nil && remoteBranch != "" {
		branch = remoteBranch
	}
	code, err := o.allocateMinimalProjectCode(ctx, in.ProjectID, in.ProjectCode)
	if err != nil {
		return Request{}, status, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	objective := in.InitialObjective
	if objective == "" {
		objective = "Bootstrap the registered project workflow."
	}
	key := in.ProjectID + "_master"
	expected, err := o.Hub.RemoteRevision(ctx)
	if err != nil {
		return Request{}, status, fmt.Errorf("snapshot Hub revision: %w", err)
	}
	return Request{
		SchemaVersion: 1,
		ProjectID:     in.ProjectID,
		Root:          root,
		Remote:        "origin",
		RepositoryURL: repositoryURL,
		DefaultBranch: branch,
		Airelay: Airelay{
			SessionRequired: false,
			SessionKey:      &key,
		},
		ProjectCode:     code,
		GatewayStateDir: o.StateDir,
		InitialPlan: InitialPlan{
			SchemaVersion:    2,
			ProjectID:        in.ProjectID,
			Revision:         1,
			Title:            "Initial project bootstrap",
			Summary:          "Server-owned initial workflow plan.",
			CurrentObjective: objective,
			Queue:            []string{},
			Sections:         []InitialPlanSection{},
			UpdatedBy:        "gateway",
			UpdatedAt:        now,
		},
		ExpectedHubRevision: expected,
	}, status, nil
}

func (o *PublicOrchestrator) allocateMinimalProjectCode(ctx context.Context, projectID, requested string) (string, error) {
	used := map[string]bool{}
	for _, project := range o.Hub.Config.Projects {
		if project.ProjectCode != "" {
			used[project.ProjectCode] = true
		}
	}
	managed, err := config.LoadManagedProjects(o.StateDir)
	if err != nil {
		return "", err
	}
	for _, project := range managed.Projects {
		if project.ProjectCode != "" {
			used[project.ProjectCode] = true
		}
	}
	snapshot, err := o.Hub.ReadSnapshot(ctx)
	if err != nil {
		return "", err
	}
	defer snapshot.Close()
	paths, err := snapshot.List(ctx, "gpt-tunnel/v1/projects/", "/identifiers.json")
	if err == nil && len(paths) > 0 {
		files, readErr := snapshot.ReadFiles(ctx, paths)
		if readErr != nil {
			return "", readErr
		}
		for _, data := range files {
			var identifiers model.ProjectIdentifiers
			if json.Unmarshal(data, &identifiers) == nil && identifiers.ProjectCode != "" {
				used[identifiers.ProjectCode] = true
			}
		}
	}
	if requested != "" {
		if used[requested] {
			return "", fmt.Errorf("project code %q is already allocated", requested)
		}
		return requested, nil
	}
	hash := sha256.Sum256([]byte(projectID))
	start := int(hash[0])<<8 | int(hash[1])
	for offset := 0; offset < 26*26*26; offset++ {
		value := (start + offset) % (26 * 26 * 26)
		candidate := string([]byte{'A' + byte(value/(26*26)), 'A' + byte((value/26)%26), 'A' + byte(value%26)})
		if !used[candidate] {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no project code is available")
}
