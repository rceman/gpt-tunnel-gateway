package train

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

// EvidenceStore is the typed persistence seam used by Shared-authoritative
// lifecycle code. The service owns domain transitions; this adapter owns the
// local evidence representation and its bounded atomic I/O.
type EvidenceStore interface {
	AttemptReportID(trainID, taskID string, attempt uint64) (string, error)
	AttemptReviewID(trainID, taskID string, attempt uint64) (string, error)
	AttemptPacketID(trainID, taskID string, attempt uint64) (string, error)
	AttemptCompletionID(trainID, taskID string, attempt uint64) (string, error)
	WriteAttemptReport(model.TrainV2AttemptReport) (string, error)
	WriteAttemptReview(model.TrainV2AttemptReview) (string, error)
	WriteAttemptPacket(trainID, taskID string, attempt uint64, contents []byte) (string, error)
	ReadAttemptReport(trainID, taskID string, attempt uint64) (model.TrainV2AttemptReport, error)
	ReadAttemptReportBytes(trainID, taskID string, attempt uint64) ([]byte, error)
	ReadAttemptReview(trainID, taskID string, attempt uint64) (model.TrainV2AttemptReview, error)
	AttemptReviewExists(trainID, taskID string, attempt uint64) (bool, error)
}
