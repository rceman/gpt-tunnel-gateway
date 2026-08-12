package train

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func projectRoot(projectID string) string { return hub.ProtocolRoot + "/projects/" + projectID }

func trainPath(projectID, trainID string) string {
	return projectRoot(projectID) + "/trains-v2/" + trainID + ".json"
}

func runPath(projectID, runID string) string {
	return projectRoot(projectID) + "/runs/" + runID + "/run.json"
}

func readStartRecord(ctx context.Context, store hub.Store, projectID, trainID string) (model.TrainV2StartRecord, error) {
	var record model.TrainV2StartRecord
	if err := store.ReadJSON(ctx, projectRoot(projectID)+"/train-v2-starts/"+trainID+".json", &record); err != nil {
		return record, err
	}
	if err := model.ValidateTrainV2StartRecord(record); err != nil {
		return record, err
	}
	return record, nil
}

func readRun(ctx context.Context, store hub.Store, projectID, runID string) (model.Run, error) {
	var run model.Run
	if err := store.ReadJSON(ctx, runPath(projectID, runID), &run); err != nil {
		return run, err
	}
	if err := model.ValidateRun(run); err != nil {
		return run, err
	}
	return run, nil
}

func nextRunID(worktree, root, taskID string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(worktree, filepath.FromSlash(root)))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	var next uint64 = 1
	for _, entry := range entries {
		if entry.IsDir() {
			if parent, n, e := model.ParseRunID(entry.Name()); e == nil && parent == taskID && n >= next {
				next = n + 1
			}
		}
	}
	return model.FormatRunID(taskID, next)
}

// NextRunID allocates the next canonical Run ID from the durable Hub tree.
// Service adapters use it when a finalized Train item advances in place.
func NextRunID(worktree, root, taskID string) (string, error) {
	return nextRunID(worktree, root, taskID)
}

func readWorktreeJSON(worktree, path string, out any) error {
	data, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("worktree JSON has trailing content")
	}
	return nil
}
