package service

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
