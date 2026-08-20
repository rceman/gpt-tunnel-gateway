package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

const (
	trainV2ClassTerminal      = "terminal"
	trainV2ClassPlanned       = "planned_idle"
	trainV2ClassLiveAttempt   = "live_attempt"
	trainV2ClassLiveOperation = "live_operation"
	trainV2ClassIntegration   = "integration_pending"
	trainV2ClassCorrection    = "correction_pending"
	trainV2ClassStale         = "stale"
	trainV2ClassAmbiguous     = "ambiguous"
	trainV2ClassRetired       = "retired"
)

type trainV2LifecycleClassification struct {
	Class        string
	SafeToRetire bool
	Blocker      string
	Detail       string
	Recommended  string
}

func (s *Service) classifyTrainV2Lifecycle(projectID string, train model.TrainV2) (trainV2LifecycleClassification, error) {
	return s.classifyTrainV2LifecycleWithContext(context.Background(), projectID, train)
}

func (s *Service) classifyTrainV2LifecycleWithContext(ctx context.Context, projectID string, train model.TrainV2) (trainV2LifecycleClassification, error) {
	if train.Status == model.TrainV2Retired {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassRetired,
			Recommended: "retain retired Train history",
		}, nil
	}
	if train.Status == model.TrainV2Completed {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassTerminal,
			Recommended: "retain completed Train history",
		}, nil
	}
	if train.Status == model.TrainV2ReadyForIntegration {
		stale, err := s.trainV2StaleIntegrationHistory(ctx, projectID, train.ID)
		if err != nil {
			return trainV2LifecycleClassification{}, err
		}
		if stale {
			return trainV2LifecycleClassification{
				Class:       trainV2ClassStale,
				Blocker:     "TRAIN_INTEGRATION_RECONCILIATION_REQUIRED",
				Detail:      "failed durable integration mutation left a pre_pending prefix requiring reconciliation",
				Recommended: "reconcile the stale integration prefix before retrying integration",
			}, nil
		}
		return trainV2LifecycleClassification{
			Class:       trainV2ClassIntegration,
			Blocker:     "TRAIN_INTEGRATION_PENDING",
			Detail:      "Train has completed execution and still requires integration",
			Recommended: "integrate or explicitly recover the Train",
		}, nil
	}
	if train.Status == model.TrainV2Planned {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassPlanned,
			Recommended: "start the planned Train",
		}, nil
	}

	for _, item := range train.Items {
		for _, attempt := range item.Attempts {
			if attempt.Status == model.TrainV2AttemptRunning {
				return trainV2LifecycleClassification{
					Class:       trainV2ClassLiveAttempt,
					Blocker:     "TRAIN_ATTEMPT_LIVE",
					Detail:      fmt.Sprintf("item %d attempt %d is still running", item.Position, attempt.Number),
					Recommended: "reconcile the live Attempt before retirement",
				}, nil
			}
		}
	}
	if live, err := s.trainV2HasLiveOperationWithContext(ctx, projectID, train.ID); err != nil {
		return trainV2LifecycleClassification{}, err
	} else if live {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassLiveOperation,
			Blocker:     "TRAIN_OPERATION_LIVE",
			Detail:      "a durable Train mutation or integration operation is still active",
			Recommended: "let the operation reach a terminal state before retirement",
		}, nil
	}
	if rejected, ok := correctionPendingTrain(train); ok {
		return trainV2LifecycleClassification{
			Class:       trainV2ClassCorrection,
			Blocker:     "TRAIN_CORRECTION_PENDING",
			Detail:      fmt.Sprintf("item %d has an immutable rejected review and a queued correction tail", rejected),
			Recommended: "start the exact queued correction with train/correction-start",
		}, nil
	}

	allTerminal := true
	for _, item := range train.Items {
		if item.Status == model.TrainV2ItemQueued && len(item.Attempts) == 0 {
			allTerminal = false
			break
		}
		for _, attempt := range item.Attempts {
			if attempt.Status == model.TrainV2AttemptRunning {
				allTerminal = false
				break
			}
		}
	}
	if allTerminal && (train.Status == model.TrainV2Blocked || train.Status == model.TrainV2Paused || train.Status == model.TrainV2RecoveryQuarantined || train.Status == model.TrainV2Running) {
		return trainV2LifecycleClassification{
			Class:        trainV2ClassStale,
			SafeToRetire: true,
			Blocker:      "TRAIN_STALE",
			Detail:       "no live Attempt or durable operation remains for this non-terminal Train",
			Recommended:  "retire the stale Train with server-owned evidence",
		}, nil
	}
	return trainV2LifecycleClassification{
		Class:       trainV2ClassAmbiguous,
		Blocker:     "TRAIN_LIFECYCLE_AMBIGUOUS",
		Detail:      "durable state is non-terminal but cannot be proven inactive",
		Recommended: "inspect and reconcile the Train before retirement",
	}, nil
}

func correctionPendingTrain(train model.TrainV2) (int, bool) {
	rejected := -1
	for position, item := range train.Items {
		if item.Status == model.TrainV2ItemReviewed && item.Review != nil && item.Review.Outcome == model.ReviewOutcomeRejectedCorrection && item.SuccessfulAttemptNumber > 0 && item.SuccessfulAttemptNumber <= uint64(len(item.Attempts)) {
			attempt := item.Attempts[item.SuccessfulAttemptNumber-1]
			if attempt.Status == model.TrainV2AttemptSucceeded && attempt.ReviewID == item.Review.ReportID {
				if rejected != -1 {
					return -1, false
				}
				rejected = position
			}
		}
	}
	if rejected < 0 || rejected == len(train.Items)-1 {
		return -1, false
	}
	for position := rejected + 1; position < len(train.Items); position++ {
		item := train.Items[position]
		if item.Status != model.TrainV2ItemQueued || len(item.Attempts) != 0 || item.Review != nil || item.Proof != nil {
			return -1, false
		}
	}
	return rejected, true
}

func (s *Service) trainV2HasLiveOperation(projectID, trainID string) (bool, error) {
	return s.trainV2HasLiveOperationWithContext(context.Background(), projectID, trainID)
}

func (s *Service) trainV2HasLiveOperationInWorktree(projectID, trainID, worktree string) (bool, error) {
	return s.trainV2HasLiveOperationInWorktreeContext(context.Background(), projectID, trainID, worktree)
}

func (s *Service) trainV2HasLiveOperationWithContext(ctx context.Context, projectID, trainID string) (bool, error) {
	return s.trainV2HasLiveOperationInWorktreeContext(ctx, projectID, trainID, "")
}

func (s *Service) trainV2HasLiveOperationInWorktreeContext(ctx context.Context, projectID, trainID, worktree string) (bool, error) {
	var integration trainv2.IntegrationOperation
	var integrationErr error
	if worktree == "" {
		integration, integrationErr = s.readIntegrationOperation(ctx, projectID, trainID)
	} else {
		integrationErr = readWorktreeJSON(worktree, trainV2IntegrationOperationPath(projectID, trainID), &integration)
	}
	if integrationErr == nil {
		if integration.Phase != "completed" && integration.Phase != "failed" {
			status, found, err := s.trainV2IntegrationMutationStatus(projectID, trainID)
			if err != nil {
				return false, err
			}
			if !found || status != "failed" {
				return true, nil
			}
			// A failed durable integration mutation can leave the Hub lifecycle
			// at pre_pending. Preserve both records, but do not treat that stale
			// prefix as a live owner of the project integration lane.
		}
	} else if !IsNotFound(integrationErr) && !os.IsNotExist(integrationErr) {
		return false, integrationErr
	}
	root := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, err := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return false, err
		}
		if operation.ProjectID != projectID || operation.Status == "completed" || operation.Status == "failed" {
			continue
		}
		switch operation.Kind {
		case "train-v2-create", "train-v2-retire", "train-v2-reconcile":
			// These are project-level lifecycle mutations or the current
			// retirement operation itself. Hub revision checks serialize their
			// writes; they do not represent an execution Attempt for this Train.
			continue
		case "train-v2-start", "train-v2-advance", "train-attempt-finalize", "train-attempt-review", "train-attempt-proof-recovery", "train-v2-review-backfill", "train-v2-full-proof", "train-v2-integrate", "train-v2-cutover", "train-v2-add":
			var identity struct {
				TrainID string `json:"train_id"`
			}
			if err := json.Unmarshal(operation.Input, &identity); err != nil || identity.TrainID == "" {
				return false, fmt.Errorf("active Train operation %q has invalid train identity", operation.OperationID)
			}
			if identity.TrainID == trainID {
				return true, nil
			}
		default:
			if strings.HasPrefix(operation.Kind, "train-") || strings.HasPrefix(operation.Kind, "train-v2-") {
				return false, fmt.Errorf("active Train operation %q has unknown kind %q", operation.OperationID, operation.Kind)
			}
		}
	}
	return false, nil
}

func (s *Service) trainV2IntegrationMutationStatus(projectID, trainID string) (string, bool, error) {
	root := filepath.Join(s.Config.StateDir, "operations", "mutations")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	status := ""
	updated := time.Time{}
	found := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		operation, err := s.readDurableMutation(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return "", false, err
		}
		if operation.Kind != "train-v2-integrate" || operation.ProjectID != projectID {
			continue
		}
		var identity struct {
			TrainID string `json:"train_id"`
		}
		if err := json.Unmarshal(operation.Input, &identity); err != nil || identity.TrainID == "" {
			return "", false, fmt.Errorf("integration operation %q has invalid train identity", operation.OperationID)
		}
		if identity.TrainID != trainID || (found && !operation.UpdatedAt.After(updated)) {
			continue
		}
		status, updated, found = operation.Status, operation.UpdatedAt, true
	}
	return status, found, nil
}

func (s *Service) trainV2StaleIntegrationHistory(ctx context.Context, projectID, trainID string) (bool, error) {
	integration, err := s.readIntegrationOperation(ctx, projectID, trainID)
	if err != nil {
		if IsNotFound(err) || os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if integration.Phase != trainv2.IntegrationPhasePrePending {
		return false, nil
	}
	status, found, err := s.trainV2IntegrationMutationStatus(projectID, trainID)
	return found && status == "failed", err
}

func (s *Service) TrainV2Retire(ctx context.Context, in TrainV2RetireInput) (TrainV2RetireResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2RetireResult{}, err
	}
	if model.ValidateProjectIdentifier(in.ProjectID) != nil {
		return TrainV2RetireResult{}, fmt.Errorf("invalid project identifier")
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2RetireResult{}, err
	}
	if strings.TrimSpace(in.Reason) == "" || strings.ContainsAny(in.Reason, "\x00\r\n") || len(in.Reason) > 512 {
		return TrainV2RetireResult{}, fmt.Errorf("bounded retirement reason is required")
	}
	actor := AgentSessionID(ctx)
	if actor == "" {
		return TrainV2RetireResult{}, fmt.Errorf("retirement requires a bound Agent session")
	}
	current, err := s.TrainV2Read(ctx, in.ProjectID, in.TrainID)
	if err != nil {
		return TrainV2RetireResult{}, err
	}
	if current.Status == model.TrainV2Retired {
		return TrainV2RetireResult{
			Train:          current,
			PreviousStatus: current.Retirement.PreviousStatus,
			Classification: current.Retirement.Classification,
			Status:         current.Status,
		}, nil
	}
	classification, err := s.classifyTrainV2Lifecycle(in.ProjectID, current)
	if err != nil {
		return TrainV2RetireResult{}, err
	}
	if !classification.SafeToRetire {
		return TrainV2RetireResult{}, fmt.Errorf("Train cannot be retired: %s", classification.Blocker)
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2RetireResult{}, err
		}
	}
	now := s.durableNow()
	var retired model.TrainV2
	_, err = s.Hub.Transact(ctx, expected, "gateway: retire stale Train "+in.TrainID, func(worktree string) ([]string, error) {
		var latest model.TrainV2
		if err := readWorktreeJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), &latest); err != nil {
			return nil, err
		}
		if latest.Revision != current.Revision || latest.Status != current.Status {
			return nil, fmt.Errorf("Train changed before retirement")
		}
		if latest.Status == model.TrainV2Retired {
			retired = latest
			return nil, fmt.Errorf("Train is already retired")
		}
		if !staticTrainV2SafeToRetire(latest) {
			return nil, fmt.Errorf("Train became active before retirement")
		}
		if live, liveErr := s.trainV2HasLiveOperationInWorktree(in.ProjectID, in.TrainID, worktree); liveErr != nil {
			return nil, liveErr
		} else if live {
			return nil, fmt.Errorf("Train became active before retirement")
		}
		latest.Status = model.TrainV2Retired
		latest.Revision++
		latest.UpdatedAt = now
		latest.Retirement = &model.TrainV2Retirement{PreviousStatus: current.Status, Classification: classification.Class, Reason: in.Reason, ActorSessionID: actor, RetiredAt: now}
		if err := model.ValidateTrainV2(latest); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		retired = latest
		return []string{s.trainV2Path(in.ProjectID, in.TrainID)}, nil
	})
	if err != nil {
		return TrainV2RetireResult{}, err
	}
	return TrainV2RetireResult{
		Train:          retired,
		PreviousStatus: current.Status,
		Classification: classification.Class,
		Status:         retired.Status,
		OperationID:    "",
	}, nil
}

func (s *Service) TrainV2Reconcile(ctx context.Context, in TrainV2ReconcileInput) (TrainV2ReconcileResult, error) {
	if err := requireTrainV2Authoring(ctx, s, in.ProjectID); err != nil {
		return TrainV2ReconcileResult{}, err
	}
	if model.ValidateProjectIdentifier(in.ProjectID) != nil {
		return TrainV2ReconcileResult{}, fmt.Errorf("invalid project identifier")
	}
	if in.Apply && AgentSessionID(ctx) == "" {
		return TrainV2ReconcileResult{}, fmt.Errorf("reconciliation apply requires a bound Agent session")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "server-owned stale Train reconciliation"
	}
	if len(reason) > 512 || strings.ContainsAny(reason, "\x00\r\n") {
		return TrainV2ReconcileResult{}, fmt.Errorf("bounded reconciliation reason is invalid")
	}
	trains, err := s.readTrainV2Records(ctx, in.ProjectID)
	if err != nil {
		return TrainV2ReconcileResult{}, err
	}
	result := TrainV2ReconcileResult{
		ProjectID: in.ProjectID,
		DryRun:    !in.Apply,
		Records:   make([]TrainV2ReconcileRecord, 0, len(trains)),
	}
	for _, train := range trains {
		classification, err := s.classifyTrainV2Lifecycle(in.ProjectID, train)
		if err != nil {
			return TrainV2ReconcileResult{}, err
		}
		result.Records = append(result.Records, TrainV2ReconcileRecord{
			TrainID:               train.ID,
			Status:                train.Status,
			Classification:        classification.Class,
			SafeToRetire:          classification.SafeToRetire,
			Blocker:               classification.Blocker,
			RecommendedNextAction: classification.Recommended,
		})
	}
	if !in.Apply {
		return result, nil
	}
	safeCount := 0
	for _, record := range result.Records {
		if record.SafeToRetire {
			safeCount++
		}
	}
	if safeCount == 0 {
		result.Hub = OperationResult{
			ProjectID: in.ProjectID,
			Status:    "no_changes",
		}
		return result, nil
	}
	expected := in.ExpectedHubRevision
	if expected == "" {
		expected, err = s.hubRevision(ctx)
		if err != nil {
			return TrainV2ReconcileResult{}, err
		}
	}
	actor := AgentSessionID(ctx)
	now := s.durableNow()
	tx, err := s.Hub.Transact(ctx, expected, "gateway: reconcile stale Trains", func(worktree string) ([]string, error) {
		paths := make([]string, 0, len(trains))
		for i := range trains {
			path := s.trainV2Path(in.ProjectID, trains[i].ID)
			var latest model.TrainV2
			if err := readWorktreeJSON(worktree, path, &latest); err != nil {
				return nil, err
			}
			if latest.Revision != trains[i].Revision || latest.Status != trains[i].Status {
				return nil, fmt.Errorf("Train %s changed before reconciliation", latest.ID)
			}
			if !staticTrainV2SafeToRetire(latest) {
				continue
			}
			if live, liveErr := s.trainV2HasLiveOperationInWorktree(in.ProjectID, latest.ID, worktree); liveErr != nil {
				return nil, liveErr
			} else if live {
				return nil, fmt.Errorf("Train %s became active before reconciliation", latest.ID)
			}
			latest.Status = model.TrainV2Retired
			latest.Revision++
			latest.UpdatedAt = now
			latest.Retirement = &model.TrainV2Retirement{PreviousStatus: trains[i].Status, Classification: trainV2ClassStale, Reason: reason, ActorSessionID: actor, RetiredAt: now}
			if err := model.ValidateTrainV2(latest); err != nil {
				return nil, err
			}
			if err := hub.WriteJSON(worktree, path, latest); err != nil {
				return nil, err
			}
			paths = append(paths, path)
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("no stale Train records are safe to reconcile")
		}
		return paths, nil
	})
	if err != nil {
		return TrainV2ReconcileResult{}, err
	}
	for i := range result.Records {
		if result.Records[i].SafeToRetire {
			result.Records[i].Changed = true
			result.Records[i].Status = model.TrainV2Retired
		}
	}
	result.Hub = OperationResult{
		Hub:       tx,
		ProjectID: in.ProjectID,
		Status:    "reconciled",
	}
	return result, nil
}

func staticTrainV2SafeToRetire(train model.TrainV2) bool {
	if train.Status != model.TrainV2Running && train.Status != model.TrainV2Paused && train.Status != model.TrainV2Blocked && train.Status != model.TrainV2RecoveryQuarantined {
		return false
	}
	for _, item := range train.Items {
		if item.Status == model.TrainV2ItemQueued && len(item.Attempts) == 0 {
			return false
		}
		for _, attempt := range item.Attempts {
			if attempt.Status == model.TrainV2AttemptRunning {
				return false
			}
		}
	}
	return true
}

func staleTrainProjection(classification trainV2LifecycleClassification, train model.TrainV2) *TrainV2StaleTrain {
	if classification.Class != trainV2ClassStale && classification.Class != trainV2ClassAmbiguous {
		return nil
	}
	return &TrainV2StaleTrain{
		TrainID:               train.ID,
		Status:                train.Status,
		Classification:        classification.Class,
		Blocker:               classification.Blocker,
		Detail:                classification.Detail,
		RecommendedNextAction: classification.Recommended,
	}
}

func correctionTrainProjection(classification trainV2LifecycleClassification, train model.TrainV2) *TrainV2StaleTrain {
	if classification.Class != trainV2ClassCorrection {
		return nil
	}
	return &TrainV2StaleTrain{
		TrainID:               train.ID,
		Status:                train.Status,
		Classification:        classification.Class,
		Blocker:               classification.Blocker,
		Detail:                classification.Detail,
		RecommendedNextAction: classification.Recommended,
	}
}

func sortTrainV2StaleProjection(values []*TrainV2StaleTrain) {
	sort.Slice(values, func(i, j int) bool { return values[i].TrainID < values[j].TrainID })
}
