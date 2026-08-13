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
