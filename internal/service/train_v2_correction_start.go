package service

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/lockfile"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// TrainV2CorrectionStart is the sole transition that can open a correction
// item after a rejected review. It changes only the exact Train start/current
// item and then delegates dispatch to the normal Train start path.
func (s *Service) TrainV2CorrectionStart(ctx context.Context, in TrainV2CorrectionStartInput) (trainv2.StartResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return trainv2.StartResult{}, err
	}
	if err := validateTrainV2CorrectionStartInput(in); err != nil {
		return trainv2.StartResult{}, err
	}
	lock, err := lockfile.Acquire(filepath.Join(s.Config.StateDir, "locks"), "train-"+in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	defer lock.Release()

	train, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if train.Status != model.TrainV2Running || in.RejectedItemPosition >= len(train.Items) || in.CorrectionItemPosition >= len(train.Items) || in.RejectedItemPosition == in.CorrectionItemPosition {
		return trainv2.StartResult{}, fmt.Errorf("Train correction target is not in the running Train")
	}
	rejected := train.Items[in.RejectedItemPosition]
	correction := train.Items[in.CorrectionItemPosition]
	if rejected.Status != model.TrainV2ItemReviewed || rejected.Review == nil || rejected.Review.Outcome != model.ReviewOutcomeRejectedCorrection || rejected.Review.ReportID != in.RejectedReviewID || rejected.SuccessfulAttemptNumber != in.RejectedAttemptNumber || in.RejectedAttemptNumber == 0 || in.RejectedAttemptNumber > uint64(len(rejected.Attempts)) {
		return trainv2.StartResult{}, fmt.Errorf("rejected review is not the exact correction source")
	}
	rejectedAttempt := rejected.Attempts[in.RejectedAttemptNumber-1]
	if rejectedAttempt.Status != model.TrainV2AttemptSucceeded || rejectedAttempt.ReviewID != in.RejectedReviewID {
		return trainv2.StartResult{}, fmt.Errorf("rejected Attempt is not the exact reviewed source")
	}
	reviewPath := trainV2AttemptReviewPath(in.ProjectID, in.TrainID, in.RejectedItemPosition, in.RejectedAttemptNumber)
	var review model.TrainV2AttemptReview
	if err := s.Hub.ReadJSON(ctx, reviewPath, &review); err != nil {
		return trainv2.StartResult{}, fmt.Errorf("read rejected review: %w", err)
	}
	if review.ID != in.RejectedReviewID || review.TrainID != in.TrainID || review.TaskID != rejected.TaskID || review.ItemPosition != in.RejectedItemPosition || review.AttemptNumber != in.RejectedAttemptNumber || review.Outcome != model.ReviewOutcomeRejectedCorrection {
		return trainv2.StartResult{}, fmt.Errorf("rejected review identity mismatch")
	}
	if correction.Status != model.TrainV2ItemQueued || len(correction.Attempts) != 0 || correction.TaskID != in.CorrectionTaskID || correction.TaskRevision != in.CorrectionTaskRevision || correction.TaskRevisionSHA256 != in.CorrectionTaskRevisionSHA256 {
		return trainv2.StartResult{}, fmt.Errorf("correction item is not the exact queued Task")
	}
	correctionTask, err := s.TaskAuthoringRead(ctx, in.ProjectID, correction.TaskID)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if err := trainv2.ValidateExecutionTask(correctionTask); err != nil || correctionTask.ID != correction.TaskID || correctionTask.ProjectID != in.ProjectID || correctionTask.Revision != correction.TaskRevision || correctionTask.RevisionSHA256 != correction.TaskRevisionSHA256 {
		return trainv2.StartResult{}, fmt.Errorf("correction Task identity changed")
	}
	for position, item := range train.Items {
		if position != in.RejectedItemPosition && (item.ActiveAttemptNumber != 0 || item.Status == model.TrainV2ItemRunning) {
			return trainv2.StartResult{}, fmt.Errorf("another Train Attempt is active")
		}
	}
	startPath := hub.ProtocolRoot + "/projects/" + in.ProjectID + "/train-v2-starts/" + in.TrainID + ".json"
	var start model.TrainV2StartRecord
	if err := s.Hub.ReadJSON(ctx, startPath, &start); err != nil {
		return trainv2.StartResult{}, fmt.Errorf("read Train start: %w", err)
	}
	if err := model.ValidateTrainV2StartRecord(start); err != nil || start.CurrentItemPosition != in.RejectedItemPosition || start.CurrentAttemptNumber != in.RejectedAttemptNumber || start.CurrentTaskID != rejected.TaskID {
		return trainv2.StartResult{}, fmt.Errorf("Train start is not bound to the rejected Attempt")
	}
	runtime, err := trainv2.ReadRuntime(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err != nil || runtime.ItemPosition != in.RejectedItemPosition || runtime.AttemptNumber != in.RejectedAttemptNumber || runtime.TaskID != rejected.TaskID {
		return trainv2.StartResult{}, fmt.Errorf("local runtime is not bound to the rejected Attempt")
	}
	lane := s.Config.Projects[in.ProjectID]
	lane.Root = runtime.WorktreePath
	head, branch, clean, err := s.Git.CurrentHead(ctx, lane)
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if !clean || branch != start.LaneBranch {
		return trainv2.StartResult{}, fmt.Errorf("Train correction lane is not clean and bound to its branch")
	}
	resolved, err := s.ResolveAgent(ctx, AgentResolveInput{
		ProjectID:            in.ProjectID,
		Role:                 model.AgentRoleCoding,
		AgentID:              in.AgentID,
		RecommendedReasoning: in.RecommendedReasoning,
		RequireUsable:        true,
	})
	if err != nil {
		return trainv2.StartResult{}, err
	}
	if err := s.checkSessionAvailableForTrainAttempt(ctx, resolved.SessionKey, train.ID); err != nil {
		return trainv2.StartResult{}, err
	}
	now := time.Now().UTC()
	attempt := model.TrainV2Attempt{Number: 1, Status: model.TrainV2AttemptRunning, AgentID: resolved.AgentID, AirelaySessionKey: resolved.SessionKey, GatewayID: s.Config.GatewayID, StartHead: head, StartedAt: now}
	updatedItem := correction
	updatedItem.Status = model.TrainV2ItemRunning
	updatedItem.ActiveAttemptNumber = 1
	updatedItem.Attempts = []model.TrainV2Attempt{attempt}
	updatedTrain := train
	updatedTrain.Items[in.CorrectionItemPosition] = updatedItem
	updatedTrain.Revision++
	updatedTrain.UpdatedAt = now
	updatedStart := start
	updatedStart.CurrentItemPosition = in.CorrectionItemPosition
	updatedStart.CurrentAttemptNumber = 1
	updatedStart.CurrentTaskID = correction.TaskID
	updatedStart.CurrentTaskRevision = correction.TaskRevision
	updatedStart.CurrentTaskRevisionSHA256 = correction.TaskRevisionSHA256
	if err := model.ValidateTrainV2(updatedTrain); err != nil {
		return trainv2.StartResult{}, err
	}
	if err := model.ValidateTrainV2StartRecord(updatedStart); err != nil {
		return trainv2.StartResult{}, err
	}
	newRuntime := runtime
	newRuntime.ItemPosition = in.CorrectionItemPosition
	newRuntime.TaskID = correction.TaskID
	newRuntime.AttemptNumber = 1
	newRuntime.AgentID = attempt.AgentID
	newRuntime.SessionKey = attempt.AirelaySessionKey
	newRuntime.StartedAt = now
	if err := trainv2.ValidateRuntimeBinding(newRuntime, s.Config.StateDir); err != nil {
		return trainv2.StartResult{}, err
	}
	runtimePath := trainv2.RuntimePath(s.Config.StateDir, in.ProjectID, in.TrainID)
	if err := fsutil.WriteJSONAtomic(runtimePath, newRuntime, 0o600); err != nil {
		return trainv2.StartResult{}, err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			_ = fsutil.WriteJSONAtomic(runtimePath, runtime, 0o600)
		}
	}()
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return trainv2.StartResult{}, err
		}
	}
	if _, err := s.Hub.Transact(ctx, expected, "gateway: start Train-v2 correction Attempt", func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != train.Revision || latest.Items[in.RejectedItemPosition].Review == nil || latest.Items[in.RejectedItemPosition].Review.ReportID != in.RejectedReviewID || latest.Items[in.CorrectionItemPosition].Status != model.TrainV2ItemQueued || len(latest.Items[in.CorrectionItemPosition].Attempts) != 0 {
			return nil, fmt.Errorf("Train correction state changed before start")
		}
		latest.Items[in.CorrectionItemPosition] = updatedItem
		latest.Revision++
		latest.UpdatedAt = now
		var latestStart model.TrainV2StartRecord
		if err := readWorktreeJSON(worktree, startPath, &latestStart); err != nil {
			return nil, err
		}
		if latestStart.CurrentItemPosition != start.CurrentItemPosition || latestStart.CurrentAttemptNumber != start.CurrentAttemptNumber || latestStart.CurrentTaskID != start.CurrentTaskID {
			return nil, fmt.Errorf("Train start changed before correction start")
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, startPath, updatedStart); err != nil {
			return nil, err
		}
		return []string{s.trainV2Path(in.ProjectID, in.TrainID), startPath}, nil
	}); err != nil {
		return trainv2.StartResult{}, err
	}
	keepRuntime = true
	return s.TrainV2Start(ctx, TrainV2StartInput{
		ProjectID:            in.ProjectID,
		TrainID:              in.TrainID,
		StartedBy:            in.StartedBy,
		AgentID:              attempt.AgentID,
		RecommendedReasoning: in.RecommendedReasoning,
	})
}

func validateTrainV2CorrectionStartInput(in TrainV2CorrectionStartInput) error {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return err
	}
	if in.RejectedItemPosition < 0 || in.CorrectionItemPosition < 0 || in.RejectedAttemptNumber == 0 || in.RejectedReviewID == "" || model.ValidateCanonicalTaskID(in.CorrectionTaskID) != nil || in.CorrectionTaskRevision < 1 || len(in.CorrectionTaskRevisionSHA256) != 64 || in.StartedBy == "" {
		return fmt.Errorf("invalid Train-v2 correction start input")
	}
	return nil
}
