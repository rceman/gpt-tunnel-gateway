package train

import (
	"fmt"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

// CompactTrainShort and CompactTaskShort are projections of canonical IDs;
// callers cannot provide alternate path components.
func CompactTrainShort(projectCode, trainID string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	code, _, err := model.ParseTrainV2ID(trainID)
	if err != nil {
		return "", err
	}
	if code != projectCode {
		return "", fmt.Errorf("train ID project code %q does not match %q", code, projectCode)
	}
	return trainID[len(projectCode)+1:], nil
}

func CompactTaskShort(projectCode, taskID string) (string, error) {
	if err := model.ValidateProjectCode(projectCode); err != nil {
		return "", err
	}
	if _, err := model.ParseTaskIDForProject(taskID, projectCode); err != nil {
		return "", err
	}
	return taskID[len(projectCode)+1:], nil
}

func CompactWorktreePath(stateDir, projectCode, trainID string) (string, error) {
	short, err := CompactTrainShort(projectCode, trainID)
	if err != nil {
		return "", err
	}
	if stateDir == "" {
		return "", fmt.Errorf("invalid train runtime state directory")
	}
	return filepath.Join(stateDir, "work", projectCode, short), nil
}

func LegacyAttemptPath(stateDir, projectID, trainID string, position int, attempt uint64) string {
	return filepath.Join(stateDir, "train-attempts", projectID, trainID, fmt.Sprintf("item-%d", position), fmt.Sprintf("attempt-%d", attempt))
}

func CompactAttemptPath(stateDir, projectCode, trainID, taskID string, attempt uint64) (string, error) {
	trainShort, err := CompactTrainShort(projectCode, trainID)
	if err != nil {
		return "", err
	}
	taskShort, err := CompactTaskShort(projectCode, taskID)
	if err != nil {
		return "", err
	}
	if attempt < 1 {
		return "", fmt.Errorf("attempt number must be positive")
	}
	return filepath.Join(stateDir, "attempts", projectCode, trainShort, taskShort, fmt.Sprintf("A%d", attempt)), nil
}
