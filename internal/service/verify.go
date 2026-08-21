package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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
	OperationID string                       `json:"operation_id"`
	Status      string                       `json:"status"`
	ProjectID   string                       `json:"project_id,omitempty"`
	Scope       string                       `json:"scope"`
	Packages    []string                     `json:"packages,omitempty"`
	Gates       []model.CompletionGateResult `json:"gates,omitempty"`
	Error       string                       `json:"error,omitempty"`
	Reused      bool                         `json:"reused,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"`
	UpdatedAt   time.Time                    `json:"updated_at"`
}

func verifyReceiptPath(stateDir, operationID string) string {
	return filepath.Join(stateDir, "operations", "verify", operationID+".json")
}

func (s *Service) Verify(ctx context.Context, in VerifyInput) (VerifyReceipt, error) {
	if in.Root == "" {
		return VerifyReceipt{}, fmt.Errorf("verify root is required")
	}
	if in.Scope == "" {
		in.Scope = "full"
	}
	if in.Scope != "full" && in.Scope != "changed" && in.Scope != "packages" {
		return VerifyReceipt{}, fmt.Errorf("invalid verify scope %q", in.Scope)
	}
	if in.Scope == "packages" && len(in.Packages) == 0 {
		return VerifyReceipt{}, fmt.Errorf("package scope requires packages")
	}
	if in.Scope != "packages" {
		in.Packages = nil
	}
	if in.Scope == "changed" {
		changed, err := s.Git.ChangedWorkingFiles(ctx, in.Root)
		if err != nil {
			return VerifyReceipt{}, err
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
	identity, err := json.Marshal(struct {
		Root, ProjectID, Scope string
		Packages               []string
	}{in.Root, in.ProjectID, in.Scope, in.Packages})
	if err != nil {
		return VerifyReceipt{}, err
	}
	digest := sha256.Sum256(identity)
	digestText := hex.EncodeToString(digest[:])
	operationID := "verify-" + digestText
	path := verifyReceiptPath(s.Config.StateDir, operationID)
	if existing, err := readVerifyReceipt(path); err == nil && existing.Status == "completed" {
		existing.Reused = true
		return existing, nil
	}
	lockDir := filepath.Join(s.Config.StateDir, "locks")
	for {
		lock, lockErr := lockfile.Acquire(lockDir, operationID)
		if lockErr == nil {
			receipt, runErr := s.runVerifyUnderLock(ctx, lock, path, operationID, in)
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

func (s *Service) runVerifyUnderLock(ctx context.Context, lock *lockfile.Lock, path, operationID string, in VerifyInput) (VerifyReceipt, error) {
	defer lock.Release()
	now := time.Now().UTC()
	receipt := VerifyReceipt{OperationID: operationID, Status: "running", ProjectID: in.ProjectID, Scope: in.Scope, Packages: append([]string{}, in.Packages...), CreatedAt: now, UpdatedAt: now}
	if existing, err := readVerifyReceipt(path); err == nil && !existing.CreatedAt.IsZero() {
		receipt.CreatedAt = existing.CreatedAt
	}
	if err := fsutil.WriteJSONAtomic(path, receipt, 0o600); err != nil {
		return VerifyReceipt{}, err
	}
	scope := gates.FullTestScope()
	if in.Scope == "packages" {
		scope = gates.TestScope{Mode: gates.TestScopePackages, Packages: in.Packages}
	}
	names := []string{"format", "check", "test"}
	var results []model.CompletionGateResult
	var runErr error
	if in.ProjectID != "" {
		names, runErr = s.ResolveProjectGates(ctx, in.ProjectID, "implementation")
		if runErr == nil {
			results, runErr = s.executeProjectGatesWithProjectCommandsAndScope(ctx, in.ProjectID, in.Root, names, "task", scope)
		}
	} else {
		results, runErr = s.executeGateNamesWithScope(ctx, in.Root, names, scope)
	}
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

func VerifyStatus(ctx context.Context, path string) (VerifyReceipt, error) {
	if ctx == nil {
		return VerifyReceipt{}, fmt.Errorf("context is required")
	}
	if _, err := os.Stat(path); err != nil {
		return VerifyReceipt{}, err
	}
	return readVerifyReceipt(path)
}
