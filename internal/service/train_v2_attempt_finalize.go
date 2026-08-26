package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

type TrainV2AttemptFinalizeInput struct {
	ProjectID          string   `json:"project_id"`
	TrainID            string   `json:"train_id"`
	ItemPosition       int      `json:"item_position"`
	AttemptNumber      uint64   `json:"attempt_number"`
	CompletionFile     string   `json:"completion_file,omitempty"`
	Summary            string   `json:"summary,omitempty"`
	AcceptanceCoverage []string `json:"acceptance_coverage,omitempty"`
	Deviations         []string `json:"deviations,omitempty"`
	RemainingRisks     []string `json:"remaining_risks,omitempty"`
	WriteOptions
}

type TrainV2AttemptFinalizeResult struct {
	Report      model.TrainV2AttemptReport `json:"report"`
	NextTaskID  string                     `json:"next_task_id,omitempty"`
	TrainStatus string                     `json:"train_status,omitempty"`
}

func (s *Service) trainV2AttemptCompletionPath(ctx context.Context, projectID, trainID, taskID string, position int, attempt uint64) (string, error) {
	return s.attemptCompletionID(trainID, taskID, attempt)
}

func (s *Service) TrainV2AttemptFinalize(ctx context.Context, in TrainV2AttemptFinalizeInput) (TrainV2AttemptFinalizeResult, error) {
	if s.Durability == nil {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Shared Train authority is unavailable")
	}
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if in.ItemPosition < 0 || in.AttemptNumber == 0 {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("invalid Train-v2 Attempt identity")
	}
	train, err := s.trainV2ReadShared(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if in.ItemPosition >= len(train.Items) {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Train item position is out of range")
	}
	item := train.Items[in.ItemPosition]
	if item.ActiveAttemptNumber != in.AttemptNumber || in.AttemptNumber > uint64(len(item.Attempts)) {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Attempt is not the exact active Train item Attempt")
	}
	attempt := item.Attempts[in.AttemptNumber-1]
	if attempt.Status != model.TrainV2AttemptRunning {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Attempt is not running")
	}
	task, err := s.TaskAuthoringRead(ctx, in.ProjectID, item.TaskID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if err := model.ValidateTaskAuthoring(task); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	var start model.TrainV2StartRecord
	policy, policyErr := s.ProjectWorkflowPolicyRead(ctx, in.ProjectID)
	if policyErr != nil {
		return TrainV2AttemptFinalizeResult{}, policyErr
	}
	projectConfig, ok := s.Config.Projects[in.ProjectID]
	if !ok {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("project %q has no local runtime configuration", in.ProjectID)
	}
	start = trainv2.DeriveStartRecord(train, item, attempt, policy, model.Project{ID: in.ProjectID, DefaultBranch: projectConfig.DefaultBranch}, attempt.StartedAt)
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if runtime.ItemPosition != in.ItemPosition || runtime.TaskID != item.TaskID || runtime.AttemptNumber != in.AttemptNumber || start.CurrentAttemptNumber != in.AttemptNumber || start.CurrentItemPosition != in.ItemPosition {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Attempt runtime ownership mismatch")
	}
	local, err := s.projectConfig(in.ProjectID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	local.Root = runtime.WorktreePath
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+in.TrainID)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	defer lock.Release()
	head, branch, clean, err := s.Git.CurrentHead(ctx, local)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if !clean || branch != start.LaneBranch {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Train Attempt finalization requires clean lane")
	}
	changed, err := s.Git.ChangedFiles(ctx, local.Root, attempt.StartHead, head)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	testScope := resolveFinalizationTestScope(ctx, "implementation", local.Root, changed)
	gates, err := s.ResolveProjectGates(ctx, in.ProjectID, "implementation")
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	serverGates, err := s.executeTaskFinalizeGatesWithSnapshot(ctx, in.ProjectID, local, gates, changed, testScope)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if err := validateProjectGateEvidence(serverGates, gates); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	finalHead, finalBranch, finalClean, err := s.Git.CurrentHead(ctx, local)
	if err != nil || finalHead != head || finalBranch != branch || !finalClean {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Train lane changed during Attempt gates")
	}
	completion, err := s.readTrainV2AttemptCompletion(ctx, in, task, item, attempt, serverGates)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if completion.Status == "succeeded" {
		completion.FinishedAt = time.Now().UTC()
	}
	proof := model.RepositoryProof{Branch: finalBranch, Head: finalHead, WorktreeClean: finalClean, BaseAncestor: true, Commits: []string{}, ChangedFiles: changed, DiffScope: "attempt-start..attempt-head"}
	report := model.TrainV2AttemptReport{SchemaVersion: 1, TrainID: in.TrainID, TaskID: item.TaskID, ItemPosition: in.ItemPosition, AttemptNumber: in.AttemptNumber, ProjectID: in.ProjectID, Status: completion.Status, Summary: completion.Summary, GateResults: completion.GateResults, ServerGateResults: serverGates, AcceptanceCoverage: completion.AcceptanceCoverage, Deviations: completion.Deviations, RemainingRisks: completion.RemainingRisks, Repository: proof, FinishedAt: completion.FinishedAt}
	if err := model.ValidateTrainV2AttemptReport(report); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	evidence, evidenceErr := s.sharedTrainEvidence()
	if evidenceErr != nil {
		return TrainV2AttemptFinalizeResult{}, evidenceErr
	}
	current := train
	currentItem := current.Items[in.ItemPosition]
	if currentItem.TaskID != item.TaskID || currentItem.ActiveAttemptNumber != in.AttemptNumber || len(currentItem.Attempts) < int(in.AttemptNumber) || currentItem.Attempts[in.AttemptNumber-1].Status != model.TrainV2AttemptRunning {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Attempt changed before finalization")
	}
	finished := completion.FinishedAt
	currentItem.Attempts[in.AttemptNumber-1].FinishedAt = &finished
	if completion.Status == "succeeded" {
		currentItem.Attempts[in.AttemptNumber-1].Status = model.TrainV2AttemptSucceeded
		currentItem.Status = model.TrainV2ItemFinalized
		currentItem.SuccessfulAttemptNumber = in.AttemptNumber
		currentItem.ActiveAttemptNumber = 0
	} else {
		currentItem.Attempts[in.AttemptNumber-1].Status = model.TrainV2AttemptFailed
		currentItem.Status = model.TrainV2ItemBlocked
		currentItem.ActiveAttemptNumber = 0
	}
	current.Items[in.ItemPosition] = currentItem
	current.Revision++
	current.UpdatedAt = finished
	if err := model.ValidateTrainV2(current); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	reportPath, err := evidence.WriteAttemptReport(report)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if filepath.IsAbs(reportPath) {
		return TrainV2AttemptFinalizeResult{}, fmt.Errorf("Attempt report identity must be portable")
	}
	if completion.Status == "succeeded" {
		if err := applyTrainV2AttemptProof(&current.Items[in.ItemPosition], model.TrainV2ImplementationProof{CheckpointHead: finalHead, ImplementationSHA: finalHead, ReportID: reportPath, GateResults: append([]model.CompletionGateResult{}, serverGates...), RecordedAt: finished}); err != nil {
			return TrainV2AttemptFinalizeResult{}, err
		}
		current.Items[in.ItemPosition].Attempts[in.AttemptNumber-1].ReportID = reportPath
	}
	if err := model.ValidateTrainV2(current); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	reportPayload, err := json.Marshal(report)
	if err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	if err := s.commitSharedTrain(ctx, durableMutationOperationID(ctx), current, "train-attempt-finalize", sqlitestore.SharedReplicaIntent{Kind: "attempt_report", EntityID: reportPath, ProjectID: in.ProjectID, Payload: reportPayload}); err != nil {
		return TrainV2AttemptFinalizeResult{}, err
	}
	return TrainV2AttemptFinalizeResult{
		Report: report,
	}, nil
}

func (s *Service) readTrainV2AttemptCompletion(ctx context.Context, in TrainV2AttemptFinalizeInput, task model.TaskAuthoring, item model.TrainV2Item, attempt model.TrainV2Attempt, serverGates []model.CompletionGateResult) (model.TrainV2AttemptCompletion, error) {
	path := in.CompletionFile
	if path == "" {
		summary := in.Summary
		if summary == "" {
			summary = "Gateway finalized the exact TrainItem Attempt from server-owned evidence."
		}
		return model.TrainV2AttemptCompletion{
			SchemaVersion: 1, TrainID: in.TrainID, TaskID: item.TaskID, ItemPosition: item.Position,
			AttemptNumber: attempt.Number, TaskSHA256: task.RevisionSHA256, Status: "succeeded", Summary: summary,
			GateResults: append([]model.CompletionGateResult{}, serverGates...), AcceptanceCoverage: append([]string{}, in.AcceptanceCoverage...),
			Deviations: append([]string{}, in.Deviations...), RemainingRisks: append([]string{}, in.RemainingRisks...),
		}, nil
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return model.TrainV2AttemptCompletion{}, fmt.Errorf("Attempt completion input must be a regular file")
	}
	raw, err := fsutil.ReadFileBounded(path, s.Config.MaxReadBytes)
	if err != nil {
		return model.TrainV2AttemptCompletion{}, err
	}
	var completion model.TrainV2AttemptCompletion
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&completion); err != nil {
		return completion, err
	}
	if err := model.ValidateTrainV2AttemptCompletion(completion, task, in.TrainID, item.Position, attempt.Number); err != nil {
		return completion, err
	}
	return completion, nil
}
