package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type TrainV2AttemptMigrationInput struct {
	ProjectID           string
	TrainID             string
	ExpectedHubRevision string
	Apply               bool
}

type TrainV2AttemptMigrationResult struct {
	DryRun          bool
	Applied         bool
	AlreadyMigrated bool
	ProjectID       string
	TrainID         string
	HubBefore       string
	HubAfter        string
	ReceiptPath     string
	ChangedPaths    []string
	Items           []model.TrainV2AttemptMigrationItem
}

func trainV2AttemptMigrationPath(projectID, trainID string) string {
	return hub.ProtocolRoot + "/projects/" + projectID + "/migrations/train-v2-attempts/" + trainID + ".json"
}

func (s *Service) TrainV2MigrateAttempts(ctx context.Context, in TrainV2AttemptMigrationInput) (TrainV2AttemptMigrationResult, error) {
	if err := model.ValidateProjectIdentifier(in.ProjectID); err != nil {
		return TrainV2AttemptMigrationResult{}, err
	}
	if _, _, err := model.ParseTrainV2ID(in.TrainID); err != nil {
		return TrainV2AttemptMigrationResult{}, err
	}
	if in.Apply && in.ExpectedHubRevision == "" {
		return TrainV2AttemptMigrationResult{}, fmt.Errorf("Train-v2 migration apply requires expected_hub_revision")
	}
	checkRevision, err := s.hubRevision(ctx)
	if err != nil {
		return TrainV2AttemptMigrationResult{}, err
	}
	result := TrainV2AttemptMigrationResult{
		DryRun:       !in.Apply,
		ProjectID:    in.ProjectID,
		TrainID:      in.TrainID,
		HubBefore:    checkRevision,
		ChangedPaths: []string{},
	}
	if in.ExpectedHubRevision != "" && in.ExpectedHubRevision != checkRevision {
		return result, fmt.Errorf("HUB_REVISION_CONFLICT: expected %s, got %s", in.ExpectedHubRevision, checkRevision)
	}
	rawTrain, err := s.Hub.ReadFile(ctx, s.trainV2Path(in.ProjectID, in.TrainID))
	if err != nil {
		return result, err
	}
	train, err := decodeLegacyTrainV2MigrationSource(rawTrain)
	if err != nil {
		return result, err
	}
	if train.ProjectID != in.ProjectID || train.ID != in.TrainID || train.SchemaVersion != model.TrainV2SchemaVersion {
		return result, fmt.Errorf("invalid Train v2 migration source identity")
	}
	receiptPath := trainV2AttemptMigrationPath(in.ProjectID, in.TrainID)
	result.ReceiptPath = receiptPath
	if raw, readErr := s.Hub.ReadFile(ctx, receiptPath); readErr == nil {
		var receipt model.TrainV2AttemptMigrationReceipt
		if err := decodeStrict(raw, &receipt); err != nil {
			return result, fmt.Errorf("invalid Train v2 attempt migration receipt: %w", err)
		}
		if receipt.ProjectID != in.ProjectID || receipt.TrainID != in.TrainID || (receipt.State != "completed" && receipt.State != "pending") {
			return result, fmt.Errorf("invalid Train v2 attempt migration receipt identity")
		}
		if err := validateMigratedTrainItems(train, receipt.Items); err != nil {
			return result, fmt.Errorf("completed Train v2 attempt migration no longer matches: %w", err)
		}
		if receipt.State == "pending" {
			receipt.State = "completed"
			receipt.HubAfter = checkRevision
			if _, txErr := s.Hub.Transact(ctx, checkRevision, "gateway: complete Train v2 attempt migration "+in.TrainID, func(worktree string) ([]string, error) {
				if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
					return nil, err
				}
				return []string{receiptPath}, nil
			}); txErr != nil {
				return result, txErr
			}
		}
		result.AlreadyMigrated = true
		result.Applied = true
		result.HubAfter = checkRevision
		result.Items = append([]model.TrainV2AttemptMigrationItem{}, receipt.Items...)
		return result, nil
	} else if !IsNotFound(readErr) {
		return result, readErr
	}

	items, err := s.buildTrainV2AttemptMigration(ctx, in.ProjectID, train)
	if err != nil {
		return result, err
	}
	result.Items = items
	if !in.Apply {
		return result, nil
	}
	backup, err := s.Hub.Backup(ctx, "train-v2-attempt-migration")
	if err != nil {
		return result, err
	}
	_ = backup
	now := nowUTC()
	receipt := model.TrainV2AttemptMigrationReceipt{SchemaVersion: model.TrainV2AttemptSchemaVersion, ProjectID: in.ProjectID, TrainID: in.TrainID, State: "pending", HubBefore: checkRevision, Items: append([]model.TrainV2AttemptMigrationItem{}, items...), Reason: "migrate Train v2 execution from global Run identity to item-local attempts", CreatedAt: now, UpdatedAt: now}
	tx, err := s.Hub.Transact(ctx, checkRevision, "gateway: migrate Train v2 attempts "+in.TrainID, func(worktree string) ([]string, error) {
		latestRaw, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(s.trainV2Path(in.ProjectID, in.TrainID))))
		if err != nil {
			return nil, err
		}
		latest, err := decodeLegacyTrainV2MigrationSource(latestRaw)
		if err != nil {
			return nil, err
		}
		if err := s.applyTrainV2AttemptMigration(worktree, &latest, items); err != nil {
			return nil, err
		}
		if err := hub.WriteJSON(worktree, s.trainV2Path(in.ProjectID, in.TrainID), latest); err != nil {
			return nil, err
		}
		receipt.HubAfter = "pending"
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		paths := []string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}
		for _, item := range items {
			path := filepath.Join(worktree, filepath.FromSlash(item.LegacyRunRef.Path))
			if !strings.Contains(item.LegacyRunRef.Path, "/runs/") {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, err
			} else if err == nil {
				paths = append(paths, item.LegacyRunRef.Path)
			}
		}
		return paths, nil
	})
	if err != nil {
		return result, err
	}
	// The receipt is intentionally completed in the same durable transaction
	// only after the optimistic commit identity is known; a pending receipt is
	// resumable and never permits a second migration.
	receipt.HubAfter = tx.After
	_, err = s.Hub.Transact(ctx, tx.After, "gateway: complete Train v2 attempt migration "+in.TrainID, func(worktree string) ([]string, error) {
		if err := hub.WriteJSON(worktree, receiptPath, receipt); err != nil {
			return nil, err
		}
		return []string{receiptPath}, nil
	})
	if err != nil {
		return result, fmt.Errorf("Train v2 attempt migration committed with pending receipt: %w", err)
	}
	result.Applied = true
	result.HubAfter = tx.After
	result.ChangedPaths = []string{s.trainV2Path(in.ProjectID, in.TrainID), receiptPath}
	return result, nil
}

func decodeLegacyTrainV2MigrationSource(raw []byte) (model.TrainV2, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return model.TrainV2{}, err
	}
	items, ok := object["items"].([]any)
	if !ok {
		return model.TrainV2{}, fmt.Errorf("Train-v2 migration source has no items array")
	}
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return model.TrainV2{}, fmt.Errorf("Train-v2 migration source has malformed item")
		}
		delete(item, "run_id")
		delete(item, "agent_id")
		delete(item, "start_head")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return model.TrainV2{}, err
	}
	var train model.TrainV2
	if err := decodeStrict(canonical, &train); err != nil {
		return model.TrainV2{}, fmt.Errorf("invalid Train-v2 migration source: %w", err)
	}
	return train, nil
}

type legacyTrainV2ItemInput struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	RunID  string `json:"run_id"`
}

func (s *Service) buildTrainV2AttemptMigration(ctx context.Context, projectID string, train model.TrainV2) ([]model.TrainV2AttemptMigrationItem, error) {
	// This decoder is migration input only. Its legacy fields are never
	// decoded by canonical Train-v2 runtime code and are retained solely to
	// identify the exact source bytes being migrated.
	rawTrain, err := s.Hub.ReadFile(ctx, s.trainV2Path(projectID, train.ID))
	if err != nil {
		return nil, err
	}
	var legacy struct {
		Items []legacyTrainV2ItemInput `json:"items"`
	}
	if err := json.Unmarshal(rawTrain, &legacy); err != nil {
		return nil, err
	}
	items := make([]model.TrainV2AttemptMigrationItem, 0, len(train.Items))
	for _, item := range train.Items {
		if len(item.Attempts) > 0 {
			continue
		}
		var legacyItem legacyTrainV2ItemInput
		for _, candidate := range legacy.Items {
			if candidate.TaskID == item.TaskID {
				legacyItem = candidate
				break
			}
		}
		if legacyItem.RunID == "" {
			if item.Status == model.TrainV2ItemQueued {
				continue
			}
			return nil, fmt.Errorf("Train item %s has execution state without Run or attempts", item.TaskID)
		}
		path := s.projectPrefix(train.ProjectID) + "/runs/" + legacyItem.RunID + "/run.json"
		raw, err := s.Hub.ReadFile(ctx, path)
		status := ""
		if err == nil {
			candidate, historical, decodeErr := decodeMigrationRun(raw)
			if decodeErr != nil || historical || candidate.ProjectID != train.ProjectID || candidate.TaskID != item.TaskID || candidate.TrainID != train.ID {
				raw, path, status, err = s.findExactRecoveredTrainSource(ctx, train, item)
			}
		} else if IsNotFound(err) {
			raw, path, status, err = s.findExactTrainSourceHistory(ctx, train, item, path)
			if err != nil {
				raw, path, status, err = s.findExactRecoveredTrainSource(ctx, train, item)
			}
		} else {
			return nil, fmt.Errorf("read current Train v2 migration source %s: %w", item.TaskID, err)
		}
		if err != nil {
			return nil, err
		}
		run, historical, err := decodeMigrationRun(raw)
		if err != nil || historical || run.ID != legacyItem.RunID || run.TaskID != item.TaskID || run.ProjectID != train.ProjectID || run.TrainID != train.ID {
			return nil, fmt.Errorf("legacy source %s does not exactly bind Train item: decode=%v historical=%v run_id=%s task_id=%s project_id=%s train_id=%s", legacyItem.RunID, err, historical, run.ID, run.TaskID, run.ProjectID, run.TrainID)
		}
		if status == "" {
			status, err = migrationAttemptStatus(run.Status, run.FinishedAt)
			if err != nil {
				return nil, fmt.Errorf("legacy source %s cannot be migrated losslessly: %w", path, err)
			}
		}
		digest := digestBytes(raw)
		items = append(items, model.TrainV2AttemptMigrationItem{TaskID: item.TaskID, AttemptNumber: 1, AttemptStatus: status, LegacyRunRef: model.TrainV2LegacyRunRef{RunID: run.ID, RecordSHA256: digest, Path: path}, OriginalRunJSONBase64: base64.StdEncoding.EncodeToString(raw)})
	}
	return items, nil
}

func (s *Service) findExactTrainSourceHistory(ctx context.Context, train model.TrainV2, item model.TrainV2Item, path string) ([]byte, string, string, error) {
	history, err := s.Hub.History(ctx, path, s.Config.MaxListItems)
	if err != nil {
		return nil, "", "", err
	}
	for _, entry := range history {
		commit := entry["sha"]
		data, readErr := s.Hub.ReadFileAtCommit(ctx, commit, path)
		if readErr != nil {
			continue
		}
		run, historical, decodeErr := decodeMigrationRun(data)
		if decodeErr == nil && !historical && run.ProjectID == train.ProjectID && run.TaskID == item.TaskID && run.TrainID == train.ID {
			return data, path, model.TrainV2AttemptSucceeded, nil
		}
	}
	return nil, "", "", fmt.Errorf("missing historical Train v2 migration source for item %s", item.TaskID)
}

func (s *Service) findExactRecoveredTrainSource(ctx context.Context, train model.TrainV2, item model.TrainV2Item) ([]byte, string, string, error) {
	paths, err := s.Hub.List(ctx, s.projectPrefix(train.ProjectID)+"/recovery/orphan-runs", ".json")
	if err != nil {
		return nil, "", "", err
	}
	var found []struct {
		raw  []byte
		path string
	}
	for _, path := range paths {
		if filepath.Ext(path) != ".json" || filepath.Base(path) == "" || filepath.Base(path) == "" {
			continue
		}
		data, readErr := s.Hub.ReadFile(ctx, path)
		if readErr != nil {
			continue
		}
		var recovery model.OrphanRunRecovery
		if decodeStrict(data, &recovery) != nil || model.ValidateOrphanRunRecovery(recovery) != nil {
			continue
		}
		original, decodeErr := base64.StdEncoding.DecodeString(recovery.OriginalRunJSONB64)
		if decodeErr != nil {
			continue
		}
		run, historical, decodeErr := decodeMigrationRun(original)
		if decodeErr == nil && !historical && run.ProjectID == train.ProjectID && run.TaskID == item.TaskID && run.TrainID == train.ID {
			found = append(found, struct {
				raw  []byte
				path string
			}{
				raw:  original,
				path: recovery.SourcePath,
			})
		}
	}
	if len(found) != 1 {
		return nil, "", "", fmt.Errorf("missing or ambiguous exact Train v2 migration source for item %s", item.TaskID)
	}
	return found[0].raw, found[0].path, model.TrainV2AttemptRecovered, nil
}

func validateMigratedTrainItems(train model.TrainV2, expected []model.TrainV2AttemptMigrationItem) error {
	byTask := map[string]model.TrainV2AttemptMigrationItem{}
	for _, item := range expected {
		byTask[item.TaskID] = item
	}
	for _, item := range train.Items {
		migration, ok := byTask[item.TaskID]
		if !ok {
			continue
		}
		if len(item.Attempts) != 1 || item.Attempts[0].Number != migration.AttemptNumber || item.Attempts[0].LegacyRunRef == nil || *item.Attempts[0].LegacyRunRef != migration.LegacyRunRef {
			return fmt.Errorf("item %s attempt evidence mismatch", item.TaskID)
		}
	}
	return nil
}

func (s *Service) applyTrainV2AttemptMigration(worktree string, train *model.TrainV2, items []model.TrainV2AttemptMigrationItem) error {
	byTask := map[string]model.TrainV2AttemptMigrationItem{}
	for _, item := range items {
		byTask[item.TaskID] = item
	}
	for i := range train.Items {
		item := &train.Items[i]
		if len(item.Attempts) > 0 {
			continue
		}
		migration, ok := byTask[item.TaskID]
		if !ok {
			continue
		}
		path := filepath.Join(worktree, filepath.FromSlash(migration.LegacyRunRef.Path))
		raw, err := base64.StdEncoding.DecodeString(migration.OriginalRunJSONBase64)
		if err != nil {
			return err
		}
		if migration.AttemptStatus != model.TrainV2AttemptRecovered {
			if current, readErr := os.ReadFile(path); readErr == nil {
				if digestBytes(current) != migration.LegacyRunRef.RecordSHA256 || string(current) != string(raw) {
					return fmt.Errorf("legacy Run %s changed before migration", migration.LegacyRunRef.RunID)
				}
			} else if !os.IsNotExist(readErr) {
				return readErr
			}
		}
		if digestBytes(raw) != migration.LegacyRunRef.RecordSHA256 {
			return fmt.Errorf("legacy Run %s changed before migration", migration.LegacyRunRef.RunID)
		}
		var run migrationRunRecord
		if err := decodeStrict(raw, &run); err != nil {
			return err
		}
		reportID := ""
		if item.Proof != nil {
			reportID = item.Proof.ReportID
		}
		attempt := model.TrainV2Attempt{Number: 1, Status: migration.AttemptStatus, AgentID: run.AgentID, AirelaySessionKey: run.SessionKey, GatewayID: run.GatewayID, StartHead: run.BaseRevision, StartedAt: run.CreatedAt, DispatchedAt: run.DispatchedAt, FinishedAt: run.FinishedAt, CompletionPath: run.CompletionPath, ReportID: reportID, LegacyRunRef: &migration.LegacyRunRef}
		if attempt.Status != model.TrainV2AttemptRunning && attempt.FinishedAt == nil {
			return fmt.Errorf("legacy Run %s has no terminal timestamp", migration.LegacyRunRef.RunID)
		}
		item.Attempts = []model.TrainV2Attempt{attempt}
		if attempt.Status == model.TrainV2AttemptRunning {
			item.Status = model.TrainV2ItemRunning
			item.ActiveAttemptNumber = 1
			item.SuccessfulAttemptNumber = 0
			train.Status = model.TrainV2Running
		} else if train.Status == model.TrainV2RecoveryQuarantined {
			item.Status = model.TrainV2ItemBlocked
			item.SuccessfulAttemptNumber = 0
			item.ActiveAttemptNumber = 0
		} else {
			item.SuccessfulAttemptNumber = 1
			item.ActiveAttemptNumber = 0
		}
	}
	return model.ValidateTrainV2(*train)
}
