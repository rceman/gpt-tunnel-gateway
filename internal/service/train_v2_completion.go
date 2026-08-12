package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// trainV2CompletionAuthority is the server-owned completion input. It keeps
// the authoring Task and execution Train separate from the legacy Task model
// used by the workflow-v1 parser and report validator.
type trainV2CompletionAuthority struct {
	run         model.Run
	task        model.TaskAuthoring
	train       model.TrainV2
	item        model.TrainV2Item
	runtime     trainv2.RuntimeBinding
	start       model.TrainV2StartRecord
	completion  model.Task
	destination string
	gates       []string
}

func (s *Service) loadTrainV2Authority(ctx context.Context, run model.Run, requireCurrentRuntime bool) (trainV2CompletionAuthority, error) {
	if run.TrainID == "" {
		return trainV2CompletionAuthority{}, fmt.Errorf("run is not a Train v2 run")
	}
	task, err := s.TaskAuthoringRead(ctx, run.ProjectID, run.TaskID)
	if err != nil {
		return trainV2CompletionAuthority{}, err
	}
	train, err := s.TrainV2Read(ctx, run.ProjectID, run.TrainID)
	if err != nil {
		return trainV2CompletionAuthority{}, err
	}
	var item model.TrainV2Item
	found := false
	for _, candidate := range train.Items {
		if candidate.TaskID == run.TaskID && candidate.RunID == run.ID {
			item, found = candidate, true
			break
		}
	}
	if !found || item.AgentID != run.AgentID {
		return trainV2CompletionAuthority{}, fmt.Errorf("run is not bound to an exact Train v2 item")
	}
	runtime, runtimeErr := trainv2.ReadRuntime(s.Config.StateDir, run.ProjectID, run.TrainID)
	if requireCurrentRuntime && (runtimeErr != nil || runtime.RunID != run.ID) {
		return trainV2CompletionAuthority{}, fmt.Errorf("Train v2 Run has no exact local runtime binding")
	}
	var start model.TrainV2StartRecord
	startPath := hub.ProtocolRoot + "/projects/" + run.ProjectID + "/train-v2-starts/" + run.TrainID + ".json"
	if startErr := s.Hub.ReadJSON(ctx, startPath, &start); startErr != nil && requireCurrentRuntime {
		return trainV2CompletionAuthority{}, fmt.Errorf("read Train v2 start record: %w", startErr)
	}
	if requireCurrentRuntime && (start.RunID != run.ID || start.CurrentTaskID != run.TaskID) {
		return trainV2CompletionAuthority{}, fmt.Errorf("Train v2 start record is not bound to the current Run")
	}
	gates, err := s.ResolveProjectGates(ctx, run.ProjectID, "implementation")
	if err != nil {
		return trainV2CompletionAuthority{}, err
	}
	destination, err := gatewayCompletionDestination(s.Config.StateDir, run)
	if err != nil {
		return trainV2CompletionAuthority{}, err
	}
	return trainV2CompletionAuthority{
		run: run, task: task, train: train, item: item, runtime: runtime, start: start,
		completion:  trainV2CompletionTask(task, run, gates),
		destination: destination, gates: gates,
	}, nil
}

func (s *Service) loadTrainV2CompletionAuthority(ctx context.Context, run model.Run) (trainV2CompletionAuthority, error) {
	return s.loadTrainV2Authority(ctx, run, true)
}

func (s *Service) loadTrainV2HistoricalAuthority(ctx context.Context, run model.Run) (trainV2CompletionAuthority, error) {
	return s.loadTrainV2Authority(ctx, run, false)
}

func trainV2CompletionTask(task model.TaskAuthoring, run model.Run, gates []string) model.Task {
	return model.Task{
		SchemaVersion:      model.SchemaVersion,
		ID:                 task.ID,
		SHA256:             run.TaskSHA256,
		ProjectID:          task.ProjectID,
		Title:              task.Title,
		Objective:          task.Objective,
		Branch:             run.Branch,
		BaseRevision:       run.BaseRevision,
		AcceptanceCriteria: append([]string{}, task.AcceptanceCriteria...),
		Constraints:        append([]string{}, task.Constraints...),
		RequiredGates:      append([]string{}, gates...),
		Status:             "ready",
		CreatedBy:          task.CreatedBy,
		CreatedAt:          task.CreatedAt,
	}
}

func (s *Service) trainV2WriteCompletion(ctx context.Context, in CompletionWriteInput, run model.Run) (CompletionWriteResult, error) {
	if err := requireCanonicalRun(run); err != nil {
		return CompletionWriteResult{}, err
	}
	if err := s.ensureRunOwned(run); err != nil {
		return CompletionWriteResult{}, err
	}
	if !operationalActiveRun(run) {
		return CompletionWriteResult{}, fmt.Errorf("run is not active: %s", run.Status)
	}
	inputInfo, err := os.Lstat(in.CompletionFile)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	if inputInfo.Mode()&os.ModeSymlink != 0 || !inputInfo.Mode().IsRegular() {
		return CompletionWriteResult{}, fmt.Errorf("completion input must be a regular non-symlink file")
	}
	authority, err := s.loadTrainV2CompletionAuthority(ctx, run)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "project-"+run.ProjectID)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	defer lock.Release()
	data, err := fsutil.ReadFileBounded(in.CompletionFile, s.Config.MaxReadBytes)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	completion, err := model.ParseCompletion(data, authority.completion)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	if completion.RunID != run.ID || completion.TaskSHA256 != run.TaskSHA256 {
		return CompletionWriteResult{}, fmt.Errorf("completion identity does not match Train v2 Run")
	}
	canonical, err := model.CompletionJSON(completion)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	alreadyPresent, err := writeCompletionExclusive(authority.destination, append(canonical, '\n'), authority.completion, s.Config.MaxReadBytes)
	if err != nil {
		return CompletionWriteResult{}, err
	}
	status := "WRITTEN"
	if alreadyPresent {
		status = "ALREADY_PRESENT"
	}
	return CompletionWriteResult{Status: status, Path: authority.destination, ProjectID: run.ProjectID, TaskID: run.TaskID, RunID: run.ID}, nil
}
