package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func (s *Service) RunWriteCompletion(ctx context.Context, in CompletionWriteInput) (CompletionWriteResult, error) {
	if err := requireCanonicalRunID(in.RunID); err != nil {
		return CompletionWriteResult{}, err
	}
	if in.CompletionFile == "" {
		return CompletionWriteResult{}, fmt.Errorf("completion file is required")
	}
	if run, runErr := s.findRun(ctx, in.RunID); runErr == nil && run.TrainID != "" {
		return CompletionWriteResult{}, fmt.Errorf("Train-v2 completion requires the exact Train item Attempt; Run completion is not a canonical action")
	}
	inputInfo, err := os.Lstat(in.CompletionFile)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	if inputInfo.Mode()&os.ModeSymlink != 0 || !inputInfo.Mode().IsRegular() {
		return CompletionWriteResult{}, fmt.Errorf("completion input must be a regular non-symlink file")
	}
	loadAuthority := func() (model.Run, model.Task, string, error) {
		run, err := s.findRun(ctx, in.RunID)
		if err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := requireCanonicalRun(run); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := s.ensureRunOwned(run); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if !operationalActiveRun(run) {
			return model.Run{}, model.Task{}, "", fmt.Errorf("run is not active: %s", run.Status)
		}
		task, err := s.findTask(ctx, run.TaskID)
		if err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := model.ValidateTask(task); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if err := requireCanonicalTaskID(task.ID); err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		if task.ID != run.TaskID || task.ProjectID != run.ProjectID || task.SHA256 != run.TaskSHA256 {
			return model.Run{}, model.Task{}, "", fmt.Errorf("canonical task/run identity mismatch")
		}
		if err := model.ValidateTaskHash(task); err != nil || run.TaskSHA256 != task.SHA256 {
			return model.Run{}, model.Task{}, "", fmt.Errorf("durable task hash mismatch")
		}
		destination, err := gatewayCompletionDestination(s.Config.StateDir, run)
		if err != nil {
			return model.Run{}, model.Task{}, "", err
		}
		return run, task, destination, nil
	}
	run, task, destination, err := loadAuthority()
	if err != nil {
		return CompletionWriteResult{}, err
	}
	projectLock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+run.ProjectID)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	defer projectLock.Release()
	run, task, destination, err = loadAuthority()
	if err != nil {
		return CompletionWriteResult{}, err
	}
	data, err := fsutil.ReadFileBounded(in.CompletionFile, s.Config.MaxReadBytes)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	completion, err := model.ParseCompletion(data, task)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	if completion.RunID != run.ID || completion.TaskSHA256 != run.TaskSHA256 || completion.TaskRevision != run.TaskRevision || completion.TaskRevisionSHA256 != run.TaskRevisionSHA256 || completion.TaskRunNumber != run.TaskRunNumber {
		return CompletionWriteResult{}, fmt.Errorf("completion identity does not match canonical run")
	}
	canonical, err := model.CompletionJSON(completion)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	canonical = append(canonical, '\n')
	alreadyPresent, err := writeCompletionExclusive(destination, canonical, task, s.Config.MaxReadBytes)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	status := "WRITTEN"
	if alreadyPresent {
		status = "ALREADY_PRESENT"
	}
	return CompletionWriteResult{
		Status:    status,
		Path:      destination,
		ProjectID: run.ProjectID,
		TaskID:    task.ID,
		RunID:     run.ID,
	}, nil
}
