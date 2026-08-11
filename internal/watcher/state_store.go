package watcher

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

const maxStateBytes = 128 << 10

func statePath(stateDir, projectID string) string {
	return filepath.Join(filepath.Clean(stateDir), "watchers", projectID, "observation.json")
}

func supervisorPath(stateDir, projectID string) string {
	return filepath.Join(filepath.Clean(stateDir), "watchers", projectID, "supervisor.json")
}

func LoadObservation(stateDir, projectID string) (model.WatcherObservationState, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.WatcherObservationState{}, err
	}
	path := statePath(stateDir, projectID)
	var value model.WatcherObservationState
	if err := fsutil.ReadJSONBounded(path, maxStateBytes, &value); err != nil {
		if os.IsNotExist(err) {
			return model.WatcherObservationState{
				SchemaVersion: model.WatcherObservationSchemaVersion,
				ProjectID:     projectID,
				SeenDigests:   []string{},
			}, nil
		}
		return model.WatcherObservationState{}, fmt.Errorf("read watcher observation: %w", err)
	}
	if err := model.ValidateWatcherObservationState(value); err != nil {
		return model.WatcherObservationState{}, err
	}
	if value.ProjectID != projectID {
		return model.WatcherObservationState{}, fmt.Errorf("watcher observation project mismatch")
	}
	return value, nil
}

func SaveObservation(stateDir string, value model.WatcherObservationState) error {
	if err := model.ValidateWatcherObservationState(value); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(statePath(stateDir, value.ProjectID), value, 0o600)
}

func ObservationPath(stateDir, projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return ""
	}
	return statePath(stateDir, projectID)
}

func LoadSupervisor(stateDir, projectID string) (model.WatcherSupervisorState, error) {
	if err := model.ValidateProjectIdentifier(projectID); err != nil {
		return model.WatcherSupervisorState{}, err
	}
	path := supervisorPath(stateDir, projectID)
	var value model.WatcherSupervisorState
	if err := fsutil.ReadJSONBounded(path, maxStateBytes, &value); err != nil {
		if os.IsNotExist(err) {
			return model.WatcherSupervisorState{
				SchemaVersion: model.WatcherStatusSchemaVersion,
				ProjectID:     projectID,
				Desired:       "stopped",
				Runtime:       "stopped",
			}, nil
		}
		return model.WatcherSupervisorState{}, fmt.Errorf("read watcher supervisor: %w", err)
	}
	if err := model.ValidateWatcherSupervisorState(value); err != nil {
		return model.WatcherSupervisorState{}, err
	}
	if value.ProjectID != projectID {
		return model.WatcherSupervisorState{}, fmt.Errorf("watcher supervisor project mismatch")
	}
	return value, nil
}

func SaveSupervisor(stateDir string, value model.WatcherSupervisorState) error {
	if err := model.ValidateWatcherSupervisorState(value); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(supervisorPath(stateDir, value.ProjectID), value, 0o600)
}

func SupervisorPath(stateDir, projectID string) string {
	if model.ValidateProjectIdentifier(projectID) != nil {
		return ""
	}
	return supervisorPath(stateDir, projectID)
}
