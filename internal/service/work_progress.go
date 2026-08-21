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

func workProgressStatePath(stateDir, root, projectID string) string {
	digest := sha256.Sum256([]byte(root + "\x00" + projectID))
	return filepath.Join(stateDir, "operations", "work-progress", hex.EncodeToString(digest[:])+".json")
}

func (s *Service) WorkProgress(ctx context.Context, in WorkProgressInput) (WorkProgressReceipt, error) {
	if in.Root == "" {
		return WorkProgressReceipt{}, fmt.Errorf("work progress root is required")
	}
	current, err := s.Git.WorktreeFileHashes(ctx, in.Root)
	if err != nil {
		return WorkProgressReceipt{}, fmt.Errorf("worktree file hashes: %w", err)
	}
	sourceFingerprint, err := s.Git.WorktreeFingerprint(ctx, in.Root)
	if err != nil {
		return WorkProgressReceipt{}, fmt.Errorf("worktree fingerprint: %w", err)
	}
	statePath := workProgressStatePath(s.Config.StateDir, in.Root, in.ProjectID)
	state, err := readWorkProgressState(statePath)
	if err != nil && !os.IsNotExist(err) {
		return WorkProgressReceipt{}, err
	}
	if os.IsNotExist(err) {
		state = workProgressState{Root: in.Root, ProjectID: in.ProjectID, Baseline: map[string]string{}}
	}
	if state.Root != in.Root || state.ProjectID != in.ProjectID {
		return WorkProgressReceipt{}, fmt.Errorf("work progress baseline identity mismatch")
	}
	delta := workProgressDelta(state.Baseline, current)
	gateScope := gates.FullTestScope()
	if len(delta) > 0 {
		resolved, scopeErr := gates.ResolveTestScope(ctx, in.Root, delta)
		if scopeErr == nil {
			gateScope = resolved
		}
	}
	gateNames, gateIdentity, err := s.resolveVerifyGateProfile(ctx, in.ProjectID, gateScope)
	if err != nil {
		return WorkProgressReceipt{}, err
	}
	if state.GateIdentity != "" && state.GateIdentity != gateIdentity && len(delta) == 0 {
		delta = workProgressDelta(nil, current)
		gateScope = gates.FullTestScope()
		gateNames, gateIdentity, err = s.resolveVerifyGateProfile(ctx, in.ProjectID, gateScope)
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
		return receipt, nil
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), operationID)
	if err != nil {
		return WorkProgressReceipt{}, err
	}
	defer lock.Release()
	now := time.Now().UTC()
	receipt := WorkProgressReceipt{OperationID: operationID, Status: "running", ProjectID: in.ProjectID, ChangedFiles: append([]string{}, delta...), SourceFingerprint: sourceFingerprint, GateIdentity: gateIdentity, GateNames: append([]string{}, gateNames...), CreatedAt: now, UpdatedAt: now}
	plan := verifyPlan{Input: VerifyInput{Root: in.Root, ProjectID: in.ProjectID, Scope: gateScope.Mode, Packages: append([]string{}, gateScope.Packages...)}, Scope: gateScope, GateNames: gateNames, GateIdentity: gateIdentity, SourceFingerprint: sourceFingerprint}
	results, runErr := s.executeVerifyPlanGates(ctx, plan)
	receipt.Gates = results
	receipt.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		receipt.Status = "failed"
		receipt.Error = boundedVerifyError(runErr.Error())
		state.LastReceipt = receipt
		state.UpdatedAt = receipt.UpdatedAt
		_ = fsutil.WriteJSONAtomic(statePath, state, 0o600)
		return receipt, runErr
	}
	postHashes, err := s.Git.WorktreeFileHashes(ctx, in.Root)
	if err != nil {
		receipt.Status = "failed"
		receipt.Error = boundedVerifyError(fmt.Sprintf("post-gate file hashes: %v", err))
		state.LastReceipt = receipt
		state.UpdatedAt = time.Now().UTC()
		_ = fsutil.WriteJSONAtomic(statePath, state, 0o600)
		return receipt, err
	}
	postFingerprint, err := s.Git.WorktreeFingerprint(ctx, in.Root)
	if err != nil {
		return receipt, fmt.Errorf("post-gate worktree fingerprint: %w", err)
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

func (s *Service) executeVerifyPlanGates(ctx context.Context, plan verifyPlan) ([]model.CompletionGateResult, error) {
	if plan.Input.ProjectID != "" {
		return s.executeProjectGatesWithProjectCommandsAndScope(ctx, plan.Input.ProjectID, plan.Input.Root, plan.GateNames, "task", plan.Scope)
	}
	return s.executeGateNamesWithScope(ctx, plan.Input.Root, plan.GateNames, plan.Scope)
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
