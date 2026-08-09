package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunReviewReportIdentityIsDeterministic(t *testing.T) {
	if got, want := NewRunReviewReportID("run-123"), "run-123-REPORT"; got != want {
		t.Fatalf("report identity = %q, want %q", got, want)
	}
	if got, want := NewRunReviewReportID("run-123"), NewRunReviewReportID("run-123"); got != want {
		t.Fatalf("report identity is not deterministic: %q != %q", got, want)
	}
}

func TestRunReviewReportParserRejectsUnknownOutcomeAndFields(t *testing.T) {
	if _, err := ParseRunReviewReport([]byte(`{"schema_version":1,"id":"run-123-REPORT","outcome":"not-a-valid-outcome"}`)); err == nil {
		t.Fatal("unknown review outcome accepted")
	}
	if _, err := ParseRunReviewReport([]byte(`{"schema_version":1,"id":"run-123-REPORT","unknown":true}`)); err == nil {
		t.Fatal("unknown review-report field accepted")
	}
}

func TestRunReviewReportParsersAcceptRevisionAwareFields(t *testing.T) {
	revisionID, err := FormatTaskRevisionID("EXM-TSK1", 2)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := FormatTaskRevisionRunID(revisionID, 3)
	if err != nil {
		t.Fatal(err)
	}

	final := reviewReportParityFixture()
	final.RunID = runID
	final.ID = NewRunReviewReportID(runID)
	final.TaskRevision = 2
	final.TaskRevisionSHA256 = strings.Repeat("d", 64)
	final.TaskRunNumber = 3
	encodedFinal, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRunReviewReport(encodedFinal); err != nil {
		t.Fatalf("revision-aware final report rejected: %v", err)
	}

	draft := RunReviewReportDraft{
		SchemaVersion:      RunReviewReportSchemaVersion,
		ID:                 NewRunReviewReportID(runID),
		TaskID:             "EXM-TSK1",
		RunID:              runID,
		ProjectID:          "example",
		TaskSHA256:         strings.Repeat("a", 64),
		TaskRevision:       2,
		TaskRevisionSHA256: strings.Repeat("d", 64),
		TaskRunNumber:      3,
		Branch:             "task/EXM-TSK1-review",
		BaseRevision:       strings.Repeat("b", 40),
		ReviewedHead:       strings.Repeat("c", 40),
		RepositoryState:    final.RepositoryState,
		Gates:              []CompletionGateResult{},
		ChangedFiles:       []string{},
		CompletedSections:  []string{"repository_state", "gates", "changed_files"},
		DraftRevision:      1,
		UpdatedAt:          time.Now().UTC(),
	}
	encodedDraft, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRunReviewReportDraft(encodedDraft); err != nil {
		t.Fatalf("revision-aware draft rejected: %v", err)
	}
}

func TestRunReviewReportParsersRejectUnrelatedUnknownFieldWithValidRevisionFields(t *testing.T) {
	revisionID, err := FormatTaskRevisionID("EXM-TSK1", 2)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := FormatTaskRevisionRunID(revisionID, 3)
	if err != nil {
		t.Fatal(err)
	}
	final := reviewReportParityFixture()
	final.RunID = runID
	final.ID = NewRunReviewReportID(runID)
	final.TaskRevision = 2
	final.TaskRevisionSHA256 = strings.Repeat("d", 64)
	final.TaskRunNumber = 3
	encoded, err := json.Marshal(final)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	object["unrelated_field"] = true
	encoded, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRunReviewReport(encoded); err == nil {
		t.Fatal("unrelated revision-aware field accepted")
	}
}
