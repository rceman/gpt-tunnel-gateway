package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type VerifyInput struct {
	Root      string
	ProjectID string
	Scope     string
	Packages  []string
}

type VerifyReceipt struct {
	OperationID       string                       `json:"operation_id"`
	Status            string                       `json:"status"`
	ProjectID         string                       `json:"project_id,omitempty"`
	Scope             string                       `json:"scope"`
	Packages          []string                     `json:"packages,omitempty"`
	SourceFingerprint string                       `json:"source_fingerprint"`
	GateIdentity      string                       `json:"gate_identity"`
	GateNames         []string                     `json:"gate_names,omitempty"`
	Gates             []model.CompletionGateResult `json:"gates,omitempty"`
	Error             string                       `json:"error,omitempty"`
	Reused            bool                         `json:"reused,omitempty"`
	CreatedAt         time.Time                    `json:"created_at"`
	UpdatedAt         time.Time                    `json:"updated_at"`
}

type verifyPlan struct {
	Input             VerifyInput
	Scope             gates.TestScope
	GateNames         []string
	GateIdentity      string
	SourceFingerprint string
}

func verifyReceiptPath(stateDir, operationID string) string {
	return filepath.Join(stateDir, "operations", "verify", operationID+".json")
}

func (s *Service) Verify(ctx context.Context, in VerifyInput) (VerifyReceipt, error) {
	plan, err := s.resolveVerifyPlan(ctx, in)
	if err != nil {
		return VerifyReceipt{}, err
	}
	operationID, err := verifyOperationID(plan)
	if err != nil {
		return VerifyReceipt{}, err
	}
	path := verifyReceiptPath(s.Config.StateDir, operationID)
	if existing, err := readVerifyReceipt(path); err == nil && existing.Status == "completed" {
		existing.Reused = true
		return existing, nil
	}
	lockDir := filepath.Join(s.Config.StateDir, "locks")
	for {
		lock, lockErr := lockfile.Acquire(lockDir, operationID)
		if lockErr == nil {
			receipt, runErr := s.runVerifyUnderLock(ctx, lock, path, operationID, plan)
			return receipt, runErr
		}
		if !lockfile.IsBusy(lockErr) {
			return VerifyReceipt{}, lockErr
		}
		select {
		case <-ctx.Done():
			return VerifyReceipt{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if existing, readErr := readVerifyReceipt(path); readErr == nil {
			if existing.Status == "completed" {
				existing.Reused = true
				return existing, nil
			}
			if existing.Status == "failed" {
				existing.Reused = true
				return existing, fmt.Errorf("verify failed: %s", existing.Error)
			}
		}
	}
}

func verifyOperationID(plan verifyPlan) (string, error) {
	identity, err := json.Marshal(struct {
		Root, ProjectID, Scope string
		Packages               []string
		SourceFingerprint      string
		GateIdentity           string
		GateNames              []string
	}{plan.Input.Root, plan.Input.ProjectID, plan.Input.Scope, plan.Input.Packages, plan.SourceFingerprint, plan.GateIdentity, plan.GateNames})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(identity)
	return "verify-" + hex.EncodeToString(digest[:]), nil
}

func (s *Service) resolveVerifyPlan(ctx context.Context, in VerifyInput) (verifyPlan, error) {
	if in.Root == "" {
		return verifyPlan{}, fmt.Errorf("verify root is required")
	}
	if in.Scope == "" {
		in.Scope = "full"
	}
	if in.Scope != "full" && in.Scope != "changed" && in.Scope != "packages" {
		return verifyPlan{}, fmt.Errorf("invalid verify scope %q", in.Scope)
	}
	if in.Scope == "packages" && len(in.Packages) == 0 {
		return verifyPlan{}, fmt.Errorf("package scope requires packages")
	}
	if in.Scope != "packages" {
		in.Packages = nil
	}
	if in.Scope == "changed" {
		changed, err := s.Git.ChangedWorkingFiles(ctx, in.Root)
		if err != nil {
			return verifyPlan{}, err
		}
		scope, scopeErr := gates.ResolveTestScope(ctx, in.Root, changed)
		if scopeErr != nil {
			in.Scope = "full"
			in.Packages = nil
		} else if scope.Mode == gates.TestScopePackages {
			in.Scope = "packages"
			in.Packages = append([]string{}, scope.Packages...)
		} else {
			in.Scope = "full"
			in.Packages = nil
		}
	}
	scope := gates.FullTestScope()
	if in.Scope == "packages" {
		scope = gates.TestScope{Mode: gates.TestScopePackages, Packages: append([]string{}, in.Packages...)}
	}
	names, gateIdentity, err := s.resolveVerifyGateProfile(ctx, in.ProjectID, scope)
	if err != nil {
		return verifyPlan{}, err
	}
	fingerprint := s.verifyWorktreeFingerprint
	if fingerprint == nil {
		fingerprint = func(ctx context.Context, root string) (string, error) {
			return s.Git.WorktreeFingerprint(ctx, root)
		}
	}
	sourceFingerprint, err := fingerprint(ctx, in.Root)
	if err != nil {
		return verifyPlan{}, fmt.Errorf("worktree fingerprint: %w", err)
	}
	return verifyPlan{Input: in, Scope: scope, GateNames: names, GateIdentity: gateIdentity, SourceFingerprint: sourceFingerprint}, nil
}

func (s *Service) resolveVerifyGateProfile(ctx context.Context, projectID string, scope gates.TestScope) ([]string, string, error) {
	if projectID != "" {
		return s.resolveProjectGateProfile(ctx, projectID)
	}
	names := []string{"format", "check", "test"}
	var configuration *model.ProjectConfiguration
	var policy *model.ProjectWorkflowPolicy
	identity, err := json.Marshal(struct {
		Scope         gates.TestScope
		GateNames     []string
		Configuration *model.ProjectConfiguration
		Policy        *model.ProjectWorkflowPolicy
		DefaultGates  bool
	}{scope, names, configuration, policy, projectID == ""})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(identity)
	return names, hex.EncodeToString(digest[:]), nil
}

func (s *Service) resolveProjectGateProfile(ctx context.Context, projectID string) ([]string, string, error) {
	if projectID == "" {
		return nil, "", fmt.Errorf("project-defined gate profile requires project")
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	policy, err := s.ProjectWorkflowPolicyRead(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	effective, err := model.WorkflowPolicyForOperation(policy, "implementation")
	if err != nil {
		return nil, "", err
	}
	names := append([]string{}, effective.Gates...)
	identity, err := json.Marshal(struct {
		GateNames     []string
		Configuration model.ProjectConfiguration
		Policy        model.ProjectWorkflowPolicy
	}{names, configuration, policy})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(identity)
	return names, hex.EncodeToString(digest[:]), nil
}

func (s *Service) runVerifyUnderLock(ctx context.Context, lock *lockfile.Lock, path, operationID string, plan verifyPlan) (VerifyReceipt, error) {
	defer lock.Release()
	now := time.Now().UTC()
	in := plan.Input
	receipt := VerifyReceipt{OperationID: operationID, Status: "running", ProjectID: in.ProjectID, Scope: in.Scope, Packages: append([]string{}, in.Packages...), SourceFingerprint: plan.SourceFingerprint, GateIdentity: plan.GateIdentity, GateNames: append([]string{}, plan.GateNames...), CreatedAt: now, UpdatedAt: now}
	if existing, err := readVerifyReceipt(path); err == nil && !existing.CreatedAt.IsZero() {
		receipt.CreatedAt = existing.CreatedAt
	}
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		return VerifyReceipt{}, err
	}
	scope := plan.Scope
	names := plan.GateNames
	results, runErr := s.executeVerifyPlanGates(ctx, verifyPlan{Input: in, Scope: scope, GateNames: names, GateIdentity: plan.GateIdentity, SourceFingerprint: plan.SourceFingerprint})
	receipt.Gates = results
	receipt.UpdatedAt = time.Now().UTC()
	if runErr != nil {
		receipt.Status = "failed"
		receipt.Error = boundedVerifyError(runErr.Error())
	} else {
		receipt.Status = "completed"
	}
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		return VerifyReceipt{}, err
	}
	if runErr != nil {
		return receipt, runErr
	}
	return receipt, nil
}

func readVerifyReceipt(path string) (VerifyReceipt, error) {
	var receipt VerifyReceipt
	if err := fsutil.ReadJSONBounded(path, 1<<20, &receipt); err != nil {
		return VerifyReceipt{}, err
	}
	if receipt.OperationID == "" || (receipt.Status != "running" && receipt.Status != "completed" && receipt.Status != "failed") {
		return VerifyReceipt{}, fmt.Errorf("invalid verify receipt")
	}
	return receipt, nil
}

func boundedVerifyError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 32<<10 {
		return value[:32<<10] + "\n[verify diagnostics truncated]"
	}
	return value
}

func (s *Service) VerifyStatus(ctx context.Context, operationID string) (VerifyReceipt, error) {
	if ctx == nil {
		return VerifyReceipt{}, fmt.Errorf("context is required")
	}
	if err := model.ValidateObjectIdentifier(operationID); err != nil || !strings.HasPrefix(operationID, "verify-") {
		return VerifyReceipt{}, fmt.Errorf("invalid verify operation ID")
	}
	receipt, err := readVerifyReceipt(verifyReceiptPath(s.Config.StateDir, operationID))
	if err != nil {
		return VerifyReceipt{}, err
	}
	if receipt.OperationID != operationID {
		return VerifyReceipt{}, fmt.Errorf("verify receipt operation ID mismatch")
	}
	return receipt, nil
}
