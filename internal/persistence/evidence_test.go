package persistence

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestLocalEvidenceStoreOwnsAttemptIDsAndRoundTripsTypedEvidence(t *testing.T) {
	store := NewLocalEvidenceStore(t.TempDir())
	trainID, taskID := "GTW-TRN1", "GTW-TSK1"

	reportID, err := store.AttemptReportID(trainID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	reviewID, err := store.AttemptReviewID(trainID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	packetID, err := store.AttemptPacketID(trainID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	completionID, err := store.AttemptCompletionID(trainID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{reportID, reviewID, packetID, completionID} {
		if filepath.IsAbs(path) == false {
			t.Fatalf("evidence identity is not a resolved local path: %q", path)
		}
	}
	if reportID == reviewID || reportID == packetID || reviewID == packetID {
		t.Fatal("evidence identities are not distinct")
	}

	report := model.TrainV2AttemptReport{TrainID: trainID, TaskID: taskID, AttemptNumber: 1, Status: "succeeded"}
	if _, err := store.WriteAttemptReport(report); err != nil {
		t.Fatal(err)
	}
	gotReport, err := store.ReadAttemptReport(trainID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gotReport.TrainID != report.TrainID || gotReport.TaskID != report.TaskID || gotReport.Status != report.Status {
		t.Fatalf("report round-trip mismatch: %#v", gotReport)
	}

	review := model.TrainV2AttemptReview{TrainID: trainID, TaskID: taskID, ItemPosition: 0, AttemptNumber: 1, Outcome: model.ReviewOutcomeAccepted}
	if _, err := store.WriteAttemptReview(review); err != nil {
		t.Fatal(err)
	}
	gotReview, err := store.ReadAttemptReview(trainID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if gotReview.TrainID != review.TrainID || gotReview.TaskID != review.TaskID || gotReview.Outcome != review.Outcome {
		t.Fatalf("review round-trip mismatch: %#v", gotReview)
	}

	if _, err := store.WriteAttemptPacket(trainID, taskID, 1, []byte("packet\n")); err != nil {
		t.Fatal(err)
	}
	packet, err := os.ReadFile(packetID)
	if err != nil {
		t.Fatal(err)
	}
	if string(packet) != "packet\n" {
		t.Fatalf("packet mismatch: %q", packet)
	}
}
