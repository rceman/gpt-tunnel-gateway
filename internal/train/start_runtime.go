package train

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type RuntimeBinding struct {
	SchemaVersion   int       `json:"schema_version"`
	ProjectID       string    `json:"project_id"`
	ProjectCode     string    `json:"project_code,omitempty"`
	TrainID         string    `json:"train_id"`
	WorktreePath    string    `json:"worktree_path"`
	AgentID         string    `json:"agent_id"`
	SessionKey      string    `json:"session_key"`
	ItemPosition    int       `json:"item_position"`
	TaskID          string    `json:"task_id"`
	AttemptNumber   uint64    `json:"attempt_number"`
	RestartRequired bool      `json:"restart_required,omitempty"`
	StartedAt       time.Time `json:"started_at"`
}

const runtimeSchemaVersion = 1

func ExpectedWorktreePath(stateDir, projectID, trainID string) string {
	if model.ValidateProjectCode(projectID) == nil {
		if path, err := CompactWorktreePath(stateDir, projectID, trainID); err == nil {
			return path
		}
	}
	return filepath.Join(stateDir, "train-worktrees", projectID, trainID)
}

func runtimePath(stateDir, projectID, trainID string) string {
	return filepath.Join(stateDir, "train-runtime", projectID, trainID+".json")
}

// RuntimePath exposes the Gateway-local binding location to service adapters.
func RuntimePath(stateDir, projectID, trainID string) string {
	return runtimePath(stateDir, projectID, trainID)
}

func ValidateRuntimeBindingShape(v RuntimeBinding) error {
	if v.SchemaVersion != runtimeSchemaVersion || model.ValidateProjectIdentifier(v.ProjectID) != nil || v.WorktreePath == "" || model.ValidateObjectIdentifier(v.AgentID) != nil || v.SessionKey == "" || strings.ContainsAny(v.SessionKey, "\x00\r\n") || v.ItemPosition < 0 || model.ValidateCanonicalTaskID(v.TaskID) != nil || v.AttemptNumber < 1 || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid local train runtime binding")
	}
	projectCode, _, err := model.ParseTrainV2ID(v.TrainID)
	if err != nil {
		return fmt.Errorf("invalid local train runtime train ID")
	}
	if v.ProjectCode != "" {
		if err := model.ValidateProjectCode(v.ProjectCode); err != nil || projectCode != v.ProjectCode {
			return fmt.Errorf("invalid local train runtime project code")
		}
	}
	return nil
}

func ValidateRuntimeBinding(v RuntimeBinding, stateDir string) error {
	if err := ValidateRuntimeBindingShape(v); err != nil {
		return err
	}
	expected := ExpectedWorktreePath(stateDir, v.ProjectID, v.TrainID)
	if v.ProjectCode != "" {
		var err error
		expected, err = CompactWorktreePath(stateDir, v.ProjectCode, v.TrainID)
		if err != nil {
			return err
		}
	}
	if v.WorktreePath != expected {
		return fmt.Errorf("invalid local train runtime worktree path")
	}
	return nil
}

func ReadRuntime(stateDir, projectID, trainID string) (RuntimeBinding, error) {
	var binding RuntimeBinding
	if err := fsutil.ReadJSONBounded(runtimePath(stateDir, projectID, trainID), 1<<20, &binding); err != nil {
		return RuntimeBinding{}, err
	}
	if err := ValidateRuntimeBinding(binding, stateDir); err != nil {
		return RuntimeBinding{}, err
	}
	return binding, nil
}

// RetireRuntimeForRestart preserves the server-owned lane binding while
// retiring the current execution generation.  The next Start must create a
// new Run from the retained refreshed-target checkout; it must not resume the
// old Run or its dispatch receipt.
func RetireRuntimeForRestart(stateDir, projectID, trainID string, expectedAttempt uint64) (RuntimeBinding, error) {
	binding, err := ReadRuntime(stateDir, projectID, trainID)
	if err != nil {
		return RuntimeBinding{}, err
	}
	if expectedAttempt != 0 && binding.AttemptNumber != expectedAttempt {
		return RuntimeBinding{}, fmt.Errorf("Train runtime generation does not match the reconciled Attempt")
	}
	binding.RestartRequired = true
	data, err := json.Marshal(binding)
	if err != nil {
		return RuntimeBinding{}, err
	}
	if err := fsutil.WriteFileAtomic(runtimePath(stateDir, projectID, trainID), data, 0o600); err != nil {
		return RuntimeBinding{}, err
	}
	if err := os.Remove(dispatchReceiptPath(stateDir, projectID, trainID)); err != nil && !os.IsNotExist(err) {
		return RuntimeBinding{}, fmt.Errorf("retire stale Train dispatch receipt: %w", err)
	}
	return binding, nil
}
