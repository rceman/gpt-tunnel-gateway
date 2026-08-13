package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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

func (s *Service) findExactTrainSourceHistory(ctx context.Context, train model.TrainV2, item model.TrainV2Item, sourcePath string) ([]byte, string, string, error) {
	history, err := s.Hub.History(ctx, sourcePath, s.Config.MaxListItems)
	if err != nil {
		return nil, "", "", err
	}
	for _, entry := range history {
		commit := entry["sha"]
		data, readErr := s.Hub.ReadFileAtCommit(ctx, commit, sourcePath)
		if readErr != nil {
			continue
		}
		run, historical, decodeErr := decodeMigrationRun(data)
		if decodeErr == nil && !historical && run.ProjectID == train.ProjectID && run.TaskID == item.TaskID && run.TrainID == train.ID {
			return data, sourcePath, model.TrainV2AttemptSucceeded, nil
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
	for _, sourcePath := range paths {
		if filepath.Ext(sourcePath) != ".json" || filepath.Base(sourcePath) == "" {
			continue
		}
		data, readErr := s.Hub.ReadFile(ctx, sourcePath)
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
