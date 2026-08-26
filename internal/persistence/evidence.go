package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	trainv2 "github.com/rceman/gpt-tunnel-gateway/internal/train"
)

// LocalEvidenceStore is the infrastructure implementation of the typed train
// evidence contract. Path layout and atomic file writes stay outside domain and
// service packages.
type LocalEvidenceStore struct {
	stateDir string
}

func NewLocalEvidenceStore(stateDir string) trainv2.EvidenceStore {
	if stateDir == "" {
		return nil
	}
	return LocalEvidenceStore{stateDir: stateDir}
}

func (s LocalEvidenceStore) attemptRoot(trainID, taskID string, attempt uint64) (string, error) {
	projectCode, _, err := model.ParseTrainV2ID(trainID)
	if err != nil {
		return "", err
	}
	return trainv2.CompactAttemptPath(s.stateDir, projectCode, trainID, taskID, attempt)
}

func (s LocalEvidenceStore) AttemptReportID(trainID, taskID string, attempt uint64) (string, error) {
	root, err := s.attemptRoot(trainID, taskID, attempt)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "report.json"), nil
}

func (s LocalEvidenceStore) AttemptReviewID(trainID, taskID string, attempt uint64) (string, error) {
	root, err := s.attemptRoot(trainID, taskID, attempt)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "review.json"), nil
}

func (s LocalEvidenceStore) AttemptPacketID(trainID, taskID string, attempt uint64) (string, error) {
	root, err := s.attemptRoot(trainID, taskID, attempt)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "task-packet.md"), nil
}

func (s LocalEvidenceStore) AttemptCompletionID(trainID, taskID string, attempt uint64) (string, error) {
	root, err := s.attemptRoot(trainID, taskID, attempt)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "completion.json"), nil
}

func (s LocalEvidenceStore) WriteAttemptReport(report model.TrainV2AttemptReport) (string, error) {
	path, err := s.AttemptReportID(report.TrainID, report.TaskID, report.AttemptNumber)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return "", err
	}
	return hub.TrainAttemptReportPath(report.ProjectID, report.TrainID, report.ItemPosition, report.AttemptNumber), nil
}

func (s LocalEvidenceStore) WriteAttemptReview(review model.TrainV2AttemptReview) (string, error) {
	path, err := s.AttemptReviewID(review.TrainID, review.TaskID, review.AttemptNumber)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(review)
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(path, data, 0o600); err != nil {
		return "", err
	}
	return review.ID, nil
}

func (s LocalEvidenceStore) WriteAttemptPacket(trainID, taskID string, attempt uint64, contents []byte) (string, error) {
	path, err := s.AttemptPacketID(trainID, taskID, attempt)
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(path, contents, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s LocalEvidenceStore) ReadAttemptReport(trainID, taskID string, attempt uint64) (model.TrainV2AttemptReport, error) {
	path, err := s.AttemptReportID(trainID, taskID, attempt)
	if err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	var report model.TrainV2AttemptReport
	if err := fsutil.ReadJSONBounded(path, 1<<20, &report); err != nil {
		return model.TrainV2AttemptReport{}, err
	}
	return report, nil
}

func (s LocalEvidenceStore) ReadAttemptReportBytes(trainID, taskID string, attempt uint64) ([]byte, error) {
	path, err := s.AttemptReportID(trainID, taskID, attempt)
	if err != nil {
		return nil, err
	}
	return fsutil.ReadFileBounded(path, 1<<20)
}

func (s LocalEvidenceStore) ReadAttemptReview(trainID, taskID string, attempt uint64) (model.TrainV2AttemptReview, error) {
	path, err := s.AttemptReviewID(trainID, taskID, attempt)
	if err != nil {
		return model.TrainV2AttemptReview{}, err
	}
	var review model.TrainV2AttemptReview
	if err := fsutil.ReadJSONBounded(path, 1<<20, &review); err != nil {
		return model.TrainV2AttemptReview{}, err
	}
	return review, nil
}

func (s LocalEvidenceStore) AttemptReviewExists(trainID, taskID string, attempt uint64) (bool, error) {
	path, err := s.AttemptReviewID(trainID, taskID, attempt)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
