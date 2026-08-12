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
	TrainID         string    `json:"train_id"`
	WorktreePath    string    `json:"worktree_path"`
	AgentID         string    `json:"agent_id"`
	SessionKey      string    `json:"session_key"`
	RunID           string    `json:"run_id"`
	RestartRequired bool      `json:"restart_required,omitempty"`
	StartedAt       time.Time `json:"started_at"`
}

const runtimeSchemaVersion = 1

func ExpectedWorktreePath(stateDir, projectID, trainID string) string {
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
	if v.SchemaVersion != runtimeSchemaVersion || model.ValidateProjectIdentifier(v.ProjectID) != nil || v.WorktreePath == "" || model.ValidateObjectIdentifier(v.AgentID) != nil || v.SessionKey == "" || strings.ContainsAny(v.SessionKey, "\x00\r\n") || model.ValidateCanonicalRunID(v.RunID) != nil || v.StartedAt.IsZero() {
		return fmt.Errorf("invalid local train runtime binding")
	}
	if _, _, err := model.ParseTrainV2ID(v.TrainID); err != nil {
		return fmt.Errorf("invalid local train runtime train ID")
	}
	return nil
}

func ValidateRuntimeBinding(v RuntimeBinding, stateDir string) error {
	if err := ValidateRuntimeBindingShape(v); err != nil {
		return err
	}
	if v.WorktreePath != ExpectedWorktreePath(stateDir, v.ProjectID, v.TrainID) {
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
func RetireRuntimeForRestart(stateDir, projectID, trainID, expectedRunID string) (RuntimeBinding, error) {
	binding, err := ReadRuntime(stateDir, projectID, trainID)
	if err != nil {
		return RuntimeBinding{}, err
	}
	if expectedRunID != "" && binding.RunID != expectedRunID {
		return RuntimeBinding{}, fmt.Errorf("Train runtime generation does not match the reconciled Run")
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
