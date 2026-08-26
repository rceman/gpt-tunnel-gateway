package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/persistence"
)

func TestAttemptReportRetryReusesImmutablePreCASEvidence(t *testing.T) {
	store := persistence.NewLocalEvidenceStore(t.TempDir())
	first := model.TrainV2AttemptReport{
		SchemaVersion: 1, TrainID: "GTW-TRN1", TaskID: "GTW-TSK1", ItemPosition: 0, AttemptNumber: 1,
		ProjectID: "example", Status: "succeeded", Summary: "first result", FinishedAt: time.Unix(10, 0).UTC(),
	}
	if _, err := store.WriteAttemptReport(first); err != nil {
		t.Fatal(err)
	}
	candidate := first
	candidate.Summary = "regenerated result"
	candidate.FinishedAt = time.Unix(20, 0).UTC()
	got, err := loadOrReuseAttemptReport(store, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("retry did not reuse immutable report: got=%#v want=%#v", got, first)
	}
}

func TestAttemptReviewRetryReusesImmutablePreCASEvidence(t *testing.T) {
	store := persistence.NewLocalEvidenceStore(t.TempDir())
	first := model.TrainV2AttemptReview{
		SchemaVersion: model.TrainV2AttemptSchemaVersion, ID: "GTW-TRN1-ITEM0-ATTEMPT1-REVIEW",
		TrainID: "GTW-TRN1", TaskID: "GTW-TSK1", ItemPosition: 0, AttemptNumber: 1,
		Outcome: model.ReviewOutcomeAccepted, ReviewedHead: "0123456789abcdef0123456789abcdef01234567", ReviewedAt: time.Unix(10, 0).UTC(),
	}
	if _, err := store.WriteAttemptReview(first); err != nil {
		t.Fatal(err)
	}
	candidate := first
	candidate.Outcome = model.ReviewOutcomeRejected
	candidate.ReviewedAt = time.Unix(20, 0).UTC()
	got, err := loadOrReuseAttemptReview(store, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, first) {
		t.Fatalf("retry did not reuse immutable review: got=%#v want=%#v", got, first)
	}
}
