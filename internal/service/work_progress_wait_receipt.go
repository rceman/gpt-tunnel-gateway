package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
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
		state = workProgressState{
			Root:      in.Root,
			ProjectID: in.ProjectID,
			Baseline:  map[string]string{},
		}
	}
	if state.Root != in.Root || state.ProjectID != in.ProjectID {
		return fmt.Errorf("work checkpoint baseline identity mismatch")
	}
	now := time.Now().UTC()
	state.LastReceipt = WorkProgressReceipt{
		OperationID: workCheckpointClaimOperationID(in),
		Status:      "running",
		ProjectID:   in.ProjectID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
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
		state = workProgressState{
			Root:      in.Root,
			ProjectID: in.ProjectID,
			Baseline:  map[string]string{},
		}
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
		receipt := WorkProgressReceipt{
			OperationID:       operationID,
			Status:            "completed",
			ProjectID:         in.ProjectID,
			SourceFingerprint: sourceFingerprint,
			GateIdentity:      gateIdentity,
			GateNames:         gateNames,
			UpdatedAt:         time.Now().UTC(),
			Reused:            true,
		}
		state.LastReceipt = receipt
		state.UpdatedAt = receipt.UpdatedAt
		if err := fsutil.WriteJSONAtomic(statePath, state, 0o600); err != nil {
			return WorkProgressReceipt{}, fmt.Errorf("persist reused work checkpoint receipt: %w", err)
		}
		return receipt, nil
	}
	adapter, err := s.resolveWorkCheckpointAdapter(ctx, in.ProjectID)
	if err != nil {
		return s.persistWorkCheckpointFailure(statePath, state, WorkProgressReceipt{
			OperationID:       operationID,
			Status:            "failed",
			ProjectID:         in.ProjectID,
			ChangedFiles:      append([]string{}, delta...),
			SourceFingerprint: sourceFingerprint,
			GateIdentity:      gateIdentity,
			GateNames:         append([]string{}, gateNames...),
		}, err)
	}
	if s.workCheckpointExecutor != nil {
		adapter = s.workCheckpointExecutor
	}
	now := time.Now().UTC()
	receipt := WorkProgressReceipt{
		OperationID:       operationID,
		Status:            "running",
		ProjectID:         in.ProjectID,
		ChangedFiles:      append([]string{}, delta...),
		SourceFingerprint: sourceFingerprint,
		GateIdentity:      gateIdentity,
		GateNames:         append([]string{}, gateNames...),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
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
