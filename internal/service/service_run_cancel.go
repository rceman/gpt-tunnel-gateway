package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) validateCanonicalReportProof(ctx context.Context, report model.Report, run model.Run, project config.ProjectConfig) error {
	proof, err := s.deriveMirrorRepositoryProof(ctx, run, project, report.Repository.Head)
	if err != nil {
		return err
	}
	if report.Repository.Branch != proof.Branch || report.Repository.BaseAncestor != proof.BaseAncestor || report.Repository.DiffScope != proof.DiffScope {
		return fmt.Errorf("report repository proof does not match Git")
	}
	if !sameStrings(proof.ChangedFiles, report.Repository.ChangedFiles) {
		return fmt.Errorf("report changed files do not match Git")
	}
	if !sameStrings(proof.Commits, report.Repository.Commits) {
		return fmt.Errorf("report commits do not match Git history")
	}
	branchHead, branchExists, err := s.Git.MirrorBranchHead(ctx, project, proof.Branch)
	if err != nil {
		return err
	}
	if branchExists {
		if branchHead != report.Repository.Head {
			return fmt.Errorf("report branch does not point at reported HEAD")
		}
		return nil
	}
	if report.Repository.Head == run.BaseRevision {
		return nil
	}
	defaultHead, defaultExists, err := s.Git.MirrorBranchHead(ctx, project, project.DefaultBranch)
	if err != nil {
		return err
	}
	if !defaultExists {
		return fmt.Errorf("default branch is unavailable for deleted task branch")
	}
	reachable, err := s.Git.MirrorAncestor(ctx, project, report.Repository.Head, defaultHead)
	if err != nil {
		return err
	}
	if !reachable {
		return fmt.Errorf("reported HEAD is not reachable from the default branch")
	}
	return nil
}

func (s *Service) RunCancel(ctx context.Context, id, expected string) (OperationResult, error) {
	if err := requireCanonicalRunID(id); err != nil {
		return OperationResult{}, err
	}
	run, err := s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	sessionLock, err := s.acquireSessionSendLock(run.SessionKey)
	if err != nil {
		return OperationResult{}, err
	}
	defer sessionLock.Release()
	run, err = s.findRun(ctx, id)
	if err != nil {
		return OperationResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return OperationResult{}, err
	}
	if err := requireCanonicalRun(run); err != nil {
		return OperationResult{}, err
	}
	if !operationalActiveRun(run) {
		return OperationResult{}, fmt.Errorf("run is terminal")
	}
	run.Status = "cancel_requested"
	published, err := s.updateRun(ctx, run, expected, "gateway: request cancellation "+run.ID)
	if err != nil {
		return OperationResult{}, err
	}
	message := "Cancel task execution. Run: gpt-tunnel run read " + run.ID
	dispatch, dispatchErr := s.Airelay.Prompt(ctx, run.SessionKey, message)
	code := dispatch.ExitCode
	run.DispatchExitCode = &code
	run.DispatchStdout = dispatch.Stdout
	run.DispatchStderr = dispatch.Stderr
	recorded, recordErr := s.updateRun(ctx, run, published.After, "gateway: record cancellation delivery "+run.ID)
	if recordErr != nil {
		return OperationResult{
			Hub:       published,
			ProjectID: run.ProjectID,
			TaskID:    run.TaskID,
			RunID:     run.ID,
			Status:    run.Status,
		}, fmt.Errorf("cancellation published but delivery evidence was not recorded: %w", recordErr)
	}
	result := OperationResult{
		Hub:       recorded,
		ProjectID: run.ProjectID,
		TaskID:    run.TaskID,
		RunID:     run.ID,
		Status:    run.Status,
	}
	if dispatchErr != nil {
		return result, dispatchErr
	}
	return result, nil
}

const cancelDeliveryOutputLimit = 8192

func validateCancelDelivery(run model.Run) error {
	if run.DispatchExitCode == nil || *run.DispatchExitCode != 0 {
		return fmt.Errorf("cancellation delivery was not successful")
	}
	if strings.TrimSpace(run.DispatchStdout) == "" {
		return fmt.Errorf("cancellation delivery produced no stdout")
	}
	if run.DispatchStderr != "" {
		return fmt.Errorf("cancellation delivery produced stderr")
	}
	if len([]byte(run.DispatchStdout)) > cancelDeliveryOutputLimit || len([]byte(run.DispatchStderr)) > cancelDeliveryOutputLimit {
		return fmt.Errorf("cancellation delivery evidence exceeds output limit")
	}
	return nil
}

func readCurrentRun(worktree, path string, maxReadBytes int64) (model.Run, error) {
	data, err := fsutil.ReadFileBounded(filepath.Join(worktree, filepath.FromSlash(path)), maxReadBytes)
	if err != nil {
		return model.Run{}, err
	}
	run, historical, err := model.DecodeRunRecord(data)
	if err != nil {
		return model.Run{}, err
	}
	if historical {
		return model.Run{}, fmt.Errorf("workflow-v1 run is history-only")
	}
	return run, nil
}

func (s *Service) validateCancelNoMutationWorktree(ctx context.Context, task model.Task, executionBase string) error {
	local, err := s.projectConfig(task.ProjectID)
	if err != nil {
		return err
	}
	status, err := s.Git.WorktreeStatus(ctx, local)
	if err != nil {
		return err
	}
	if status.Branch != task.Branch {
		return fmt.Errorf("repository branch does not match task branch")
	}
	if !status.Clean {
		return fmt.Errorf("repository worktree is dirty or conflicted")
	}
	if status.Head != executionBase {
		return fmt.Errorf("repository HEAD does not match run execution base")
	}
	if status.Upstream != "" && (status.Ahead != 0 || status.Behind != 0) {
		return fmt.Errorf("task branch differs from its upstream")
	}
	return nil
}

// RunCancelAcknowledgeNoMutation closes a successfully delivered cancellation
// only when the task branch proves that no source mutation occurred.
