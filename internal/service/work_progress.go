package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type WorkProgressInput struct {
	Root      string
	ProjectID string
}

type WorkProgressReceipt struct {
	OperationID       string                       `json:"operation_id"`
	Status            string                       `json:"status"`
	ProjectID         string                       `json:"project_id,omitempty"`
	ChangedFiles      []string                     `json:"changed_files,omitempty"`
	SourceFingerprint string                       `json:"source_fingerprint"`
	GateIdentity      string                       `json:"gate_identity"`
	GateNames         []string                     `json:"gate_names,omitempty"`
	Gates             []model.CompletionGateResult `json:"gates,omitempty"`
	BaselineAdvanced  bool                         `json:"baseline_advanced,omitempty"`
	Reused            bool                         `json:"reused,omitempty"`
	Error             string                       `json:"error,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
}

type workProgressState struct {
	Root         string              `json:"root"`
	ProjectID    string              `json:"project_id,omitempty"`
	Baseline     map[string]string   `json:"baseline"`
	GateIdentity string              `json:"gate_identity"`
	GateNames    []string            `json:"gate_names,omitempty"`
	LastReceipt  WorkProgressReceipt `json:"last_receipt"`
	UpdatedAt    time.Time           `json:"updated_at"`
}

func workCheckpointStatePath(stateDir, root, projectID string) string {
	digest := sha256.Sum256([]byte(root + "\x00" + projectID))
	return filepath.Join(stateDir, "operations", "work-checkpoint", hex.EncodeToString(digest[:])+".json")
}

func (s *Service) WorkCheckpoint(ctx context.Context, in WorkProgressInput) (WorkProgressReceipt, error) {
	if in.Root == "" {
		return WorkProgressReceipt{}, fmt.Errorf("work checkpoint root is required")
	}
	if in.ProjectID == "" {
		return WorkProgressReceipt{}, fmt.Errorf("work checkpoint project is required")
	}
	lock, receipt, err := s.acquireWorkCheckpointLock(ctx, in)
	if err != nil {
		return WorkProgressReceipt{}, err
	}
	if receipt != nil {
		return *receipt, nil
	}
	defer lock.Release()
	if err := s.persistWorkCheckpointClaim(in); err != nil {
		return WorkProgressReceipt{}, err
	}
	return s.workCheckpointLocked(ctx, in)
}

const workCheckpointBusyWait = 2 * time.Second

// acquireWorkCheckpointLock makes project+root checkpoint execution
// single-flight. A busy lock is expected when another caller is already
// running the same checkpoint; it is not a gate failure. Observe the durable
// running receipt and return it, or wait briefly for that receipt to appear.
func (s *Service) acquireWorkCheckpointLock(ctx context.Context, in WorkProgressInput) (*lockfile.Lock, *WorkProgressReceipt, error) {
	lockDir := filepath.Join(s.Config.StateDir, "locks")
	statePath := workCheckpointStatePath(s.Config.StateDir, in.Root, in.ProjectID)
	deadline := time.NewTimer(workCheckpointBusyWait)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := lockfile.Acquire(lockDir, workCheckpointLockName(in))
		if err == nil {
			return lock, nil, nil
		}
		if !lockfile.IsBusy(err) {
			return nil, nil, err
		}
		state, stateErr := readWorkProgressState(statePath)
		if stateErr != nil && !os.IsNotExist(stateErr) {
			return nil, nil, stateErr
		}
		if stateErr == nil && state.LastReceipt.Status == "running" && state.LastReceipt.OperationID != "" {
			receipt := state.LastReceipt
			receipt.Reused = true
			return nil, &receipt, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-deadline.C:
			return nil, nil, fmt.Errorf("checkpoint lock busy without durable running receipt")
		case <-ticker.C:
		}
	}
}

func workCheckpointClaimOperationID(in WorkProgressInput) string {
	digest := sha256.Sum256([]byte(in.Root + "\x00" + in.ProjectID))
	return "work-checkpoint-claim-" + hex.EncodeToString(digest[:])
}

func (s *Service) persistWorkCheckpointClaim(in WorkProgressInput) error {
	path := workCheckpointStatePath(s.Config.StateDir, in.Root, in.ProjectID)
	state, err := readWorkProgressState(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if os.IsNotExist(err) {
		state = workProgressState{Root: in.Root, ProjectID: in.ProjectID, Baseline: map[string]string{}}
	}
	if state.Root != in.Root || state.ProjectID != in.ProjectID {
		return fmt.Errorf("work checkpoint baseline identity mismatch")
	}
	now := time.Now().UTC()
	state.LastReceipt = WorkProgressReceipt{OperationID: workCheckpointClaimOperationID(in), Status: "running", ProjectID: in.ProjectID, CreatedAt: now, UpdatedAt: now}
	state.UpdatedAt = now
	return fsutil.WriteJSONAtomic(path, state, 0o600)
}

func workCheckpointLockName(in WorkProgressInput) string {
	digest := sha256.Sum256([]byte(in.Root + "\x00" + in.ProjectID))
	return "work-checkpoint-lock-" + hex.EncodeToString(digest[:])
}

func (s *Service) workCheckpointLocked(ctx context.Context, in WorkProgressInput) (WorkProgressReceipt, error) {
	current, err := s.Git.WorktreeFileHashes(ctx, in.Root)
	if err != nil {
		return WorkProgressReceipt{}, fmt.Errorf("worktree file hashes: %w", err)
	}
	sourceFingerprint, err := s.Git.WorktreeFingerprint(ctx, in.Root)
	if err != nil {
		return WorkProgressReceipt{}, fmt.Errorf("worktree fingerprint: %w", err)
	}
	statePath := workCheckpointStatePath(s.Config.StateDir, in.Root, in.ProjectID)
	state, err := readWorkProgressState(statePath)
	if err != nil && !os.IsNotExist(err) {
		return WorkProgressReceipt{}, err
	}
	if os.IsNotExist(err) {
		state = workProgressState{Root: in.Root, ProjectID: in.ProjectID, Baseline: map[string]string{}}
	}
	if state.Root != in.Root || state.ProjectID != in.ProjectID {
		return WorkProgressReceipt{}, fmt.Errorf("work checkpoint baseline identity mismatch")
	}
	delta := workProgressDelta(state.Baseline, current)
	gateNames, gateIdentity, err := s.resolveProjectGateProfile(ctx, in.ProjectID)
	if err != nil {
		return WorkProgressReceipt{}, err
	}
	if state.GateIdentity != "" && state.GateIdentity != gateIdentity && len(delta) == 0 {
		delta = workProgressDelta(nil, current)
		gateNames, gateIdentity, err = s.resolveProjectGateProfile(ctx, in.ProjectID)
		if err != nil {
			return WorkProgressReceipt{}, err
		}
	}
	operationID, err := workProgressOperationID(in, sourceFingerprint, gateIdentity, delta)
	if err != nil {
		return WorkProgressReceipt{}, err
	}
	if len(delta) == 0 {
		receipt := WorkProgressReceipt{OperationID: operationID, Status: "completed", ProjectID: in.ProjectID, SourceFingerprint: sourceFingerprint, GateIdentity: gateIdentity, GateNames: gateNames, UpdatedAt: time.Now().UTC(), Reused: true}
		state.LastReceipt = receipt
		state.UpdatedAt = receipt.UpdatedAt
		if err := fsutil.WriteJSONAtomic(statePath, state, 0o600); err != nil {
			return WorkProgressReceipt{}, fmt.Errorf("persist reused work checkpoint receipt: %w", err)
		}
		return receipt, nil
	}
	adapter, err := s.resolveWorkCheckpointAdapter(ctx, in.ProjectID)
	if err != nil {
		return s.persistWorkCheckpointFailure(statePath, state, WorkProgressReceipt{OperationID: operationID, Status: "failed", ProjectID: in.ProjectID, ChangedFiles: append([]string{}, delta...), SourceFingerprint: sourceFingerprint, GateIdentity: gateIdentity, GateNames: append([]string{}, gateNames...)}, err)
	}
	if s.workCheckpointExecutor != nil {
		adapter = s.workCheckpointExecutor
	}
	now := time.Now().UTC()
	receipt := WorkProgressReceipt{OperationID: operationID, Status: "running", ProjectID: in.ProjectID, ChangedFiles: append([]string{}, delta...), SourceFingerprint: sourceFingerprint, GateIdentity: gateIdentity, GateNames: append([]string{}, gateNames...), CreatedAt: now, UpdatedAt: now}
	state.LastReceipt = receipt
	state.UpdatedAt = receipt.UpdatedAt
	if err := fsutil.WriteJSONAtomic(statePath, state, 0o600); err != nil {
		return WorkProgressReceipt{}, fmt.Errorf("persist running work checkpoint receipt: %w", err)
	}
	results, runErr := adapter(ctx, in.ProjectID, in.Root, append([]string{}, delta...), append([]string{}, gateNames...))
	receipt.Gates = results
	receipt.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		return s.persistWorkCheckpointFailure(statePath, state, receipt, runErr)
	}
	postHashes, err := s.Git.WorktreeFileHashes(ctx, in.Root)
	if err != nil {
		return s.persistWorkCheckpointFailure(statePath, state, receipt, fmt.Errorf("post-gate file hashes: %w", err))
	}
	postFingerprint, err := s.Git.WorktreeFingerprint(ctx, in.Root)
	if err != nil {
		return s.persistWorkCheckpointFailure(statePath, state, receipt, fmt.Errorf("post-gate worktree fingerprint: %w", err))
	}
	receipt.Status = "completed"
	receipt.SourceFingerprint = postFingerprint
	receipt.BaselineAdvanced = true
	receipt.UpdatedAt = time.Now().UTC()
	state.Baseline = postHashes
	state.GateIdentity = gateIdentity
	state.GateNames = append([]string{}, gateNames...)
	state.LastReceipt = receipt
	state.UpdatedAt = receipt.UpdatedAt
	if err := fsutil.WriteJSONAtomic(statePath, state, 0o600); err != nil {
		return WorkProgressReceipt{}, err
	}
	return receipt, nil
}

func (s *Service) persistWorkCheckpointFailure(path string, state workProgressState, receipt WorkProgressReceipt, runErr error) (WorkProgressReceipt, error) {
	receipt.Status = "failed"
	receipt.Error = boundedVerifyError(runErr.Error())
	receipt.UpdatedAt = time.Now().UTC()
	state.LastReceipt = receipt
	state.UpdatedAt = receipt.UpdatedAt
	if err := fsutil.WriteJSONAtomic(path, state, 0o600); err != nil {
		return receipt, fmt.Errorf("%v; persist failed work checkpoint receipt: %w", runErr, err)
	}
	return receipt, runErr
}

func (s *Service) executeVerifyPlanGates(ctx context.Context, plan verifyPlan) ([]model.CompletionGateResult, error) {
	if plan.Input.ProjectID != "" {
		return s.executeProjectGatesWithProjectCommandsAndScope(ctx, plan.Input.ProjectID, plan.Input.Root, plan.GateNames, "task", plan.Scope)
	}
	return s.executeGateNamesWithScope(ctx, plan.Input.Root, plan.GateNames, plan.Scope)
}

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
