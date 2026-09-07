package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) executeGoWorkCheckpoint(ctx context.Context, projectID, root string, changedFiles, gateNames []string) ([]model.CompletionGateResult, error) {
	if projectID == "" {
		return nil, fmt.Errorf("work checkpoint project adapter requires project")
	}
	scope, err := resolveWorkCheckpointGoScope(ctx, root, changedFiles)
	if err != nil {
		return nil, err
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if s.gateExecutorWithProjectCommandsAndScope == nil {
		return nil, fmt.Errorf("project checkpoint adapter executor is not configured")
	}
	results, err := s.gateExecutorWithProjectCommandsAndScope(ctx, root, gateNames, configuration.Workflow.GateCommands, "task", scope)
	if err != nil {
		return results, err
	}
	if err := validateProjectGateEvidence(results, gateNames); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Service) resolveWorkCheckpointAdapter(ctx context.Context, projectID string) (func(context.Context, string, string, []string, []string) ([]model.CompletionGateResult, error), error) {
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, err
	}
	switch configuration.Checkpoint.Adapter {
	case "go":
		return s.executeGoWorkCheckpoint, nil
	case "":
		return nil, fmt.Errorf("project %s has no work checkpoint adapter", projectID)
	default:
		return nil, fmt.Errorf("project %s work checkpoint adapter %q is unsupported", projectID, configuration.Checkpoint.Adapter)
	}
}

// resolveWorkCheckpointGoScope is the Go project adapter. The checkpoint
// engine itself carries only neutral changed paths; other projects provide a
// different adapter through Service.workCheckpointExecutor.

func resolveWorkCheckpointGoScope(ctx context.Context, root string, changedFiles []string) (gates.TestScope, error) {
	if len(changedFiles) == 0 {
		return gates.TestScope{Mode: gates.TestScopePackages, Packages: []string{}}, nil
	}
	scope, err := gates.ResolveTestScope(ctx, root, changedFiles)
	if err != nil {
		return gates.TestScope{}, fmt.Errorf("resolve project incremental scope: %w", err)
	}
	return scope, nil
}

func workProgressDelta(baseline, current map[string]string) []string {
	seen := make(map[string]struct{}, len(baseline)+len(current))
	for path := range baseline {
		seen[path] = struct{}{}
	}
	for path := range current {
		seen[path] = struct{}{}
	}
	delta := make([]string, 0, len(seen))
	for path := range seen {
		if baseline[path] != current[path] {
			delta = append(delta, path)
		}
	}
	sort.Strings(delta)
	return delta
}

func workProgressOperationID(in WorkProgressInput, sourceFingerprint, gateIdentity string, delta []string) (string, error) {
	identity, err := json.Marshal(struct {
		Root, ProjectID, SourceFingerprint, GateIdentity string
		Delta                                            []string
	}{in.Root, in.ProjectID, sourceFingerprint, gateIdentity, delta})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return "work-progress-" + hex.EncodeToString(digest[:]), nil
}

func readWorkProgressState(path string) (workProgressState, error) {
	var state workProgressState
	if err := fsutil.ReadJSONBounded(path, 8<<20, &state); err != nil {
		return workProgressState{}, err
	}
	if state.Root == "" || state.Baseline == nil {
		return workProgressState{}, fmt.Errorf("invalid work progress state")
	}
	return state, nil
}

type WorkCheckpointInput = WorkProgressInput

type WorkCheckpointReceipt = WorkProgressReceipt

func (s *Service) WorkProgress(ctx context.Context, in WorkProgressInput) (WorkProgressReceipt, error) {
	return s.WorkCheckpoint(ctx, in)
}

type WorkCheckpointStatus struct {
	Root              string                `json:"root"`
	ProjectID         string                `json:"project_id"`
	BaselinePresent   bool                  `json:"baseline_present"`
	BaselineFileCount int                   `json:"baseline_file_count"`
	ChangedFiles      []string              `json:"changed_files,omitempty"`
	SourceFingerprint string                `json:"source_fingerprint"`
	GateIdentity      string                `json:"gate_identity"`
	GateNames         []string              `json:"gate_names"`
	LastReceipt       WorkCheckpointReceipt `json:"last_receipt"`
	UpdatedAt         time.Time             `json:"updated_at"`
}

func (s *Service) WorkCheckpointStatus(ctx context.Context, in WorkCheckpointInput) (WorkCheckpointStatus, error) {
	if in.Root == "" || in.ProjectID == "" {
		return WorkCheckpointStatus{}, fmt.Errorf("work status requires root and project")
	}
	if _, err := s.resolveWorkCheckpointAdapter(ctx, in.ProjectID); err != nil {
		return WorkCheckpointStatus{}, err
	}
	current, err := s.Git.WorktreeFileHashes(ctx, in.Root)
	if err != nil {
		return WorkCheckpointStatus{}, fmt.Errorf("worktree file hashes: %w", err)
	}
	fingerprint, err := s.Git.WorktreeFingerprint(ctx, in.Root)
	if err != nil {
		return WorkCheckpointStatus{}, fmt.Errorf("worktree fingerprint: %w", err)
	}
	state, err := readWorkProgressState(workCheckpointStatePath(s.Config.StateDir, in.Root, in.ProjectID))
	if err != nil && !os.IsNotExist(err) {
		return WorkCheckpointStatus{}, err
	}
	if os.IsNotExist(err) {
		state = workProgressState{Root: in.Root, ProjectID: in.ProjectID, Baseline: map[string]string{}}
	}
	delta := workProgressDelta(state.Baseline, current)
	names, gateIdentity, err := s.resolveProjectGateProfile(ctx, in.ProjectID)
	if err != nil {
		return WorkCheckpointStatus{}, err
	}
	return WorkCheckpointStatus{Root: in.Root, ProjectID: in.ProjectID, BaselinePresent: len(state.Baseline) > 0, BaselineFileCount: len(state.Baseline), ChangedFiles: delta, SourceFingerprint: fingerprint, GateIdentity: gateIdentity, GateNames: names, LastReceipt: state.LastReceipt, UpdatedAt: state.UpdatedAt}, nil
}
