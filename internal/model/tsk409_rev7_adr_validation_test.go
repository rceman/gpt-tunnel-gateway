package model

import (
	"strings"
	"testing"
)

func rev7ADRFixture() ADR {
	return ADR{
		SchemaVersion: SchemaVersion,
		ID:            "EXM-ADR1",
		ProjectID:     "example",
		Revision:      1,
		RevisionCount: 1,
		Title:         "Rev7 ADR",
		Summary:       "Bounded rev7 summary",
		Status:        ADRStatusProposed,
		Context:       "context",
		Decision:      "decision",
		Consequences:  "consequences",
	}
}

func TestTSK409Rev7ADRSummaryValidationIsCurrentStrictAndHistoricalTolerant(t *testing.T) {
	valid := rev7ADRFixture()
	if err := ValidateADR(valid); err != nil {
		t.Fatalf("valid current ADR rejected: %v", err)
	}
	historical := valid
	historical.Summary = ""
	if err := ValidateADRRevision(historical, true); err != nil {
		t.Fatalf("pre-summary historical ADR rejected: %v", err)
	}
	if err := ValidateADR(historical); err == nil {
		t.Fatal("current ADR without summary was accepted")
	}
	if err := ValidateADRRevision(historical, false); err == nil {
		t.Fatal("new ADR revision without summary was accepted")
	}
}

func TestTSK409Rev7ADRTitleAndSummaryBoundsAreRunes(t *testing.T) {
	boundary := rev7ADRFixture()
	boundary.Title = strings.Repeat("t", 128)
	boundary.Summary = strings.Repeat("s", 256)
	if err := ValidateADR(boundary); err != nil {
		t.Fatalf("boundary ADR rejected: %v", err)
	}
	overTitle := rev7ADRFixture()
	overTitle.Title = strings.Repeat("t", 129)
	if err := ValidateADR(overTitle); err == nil {
		t.Fatal("129 rune ADR title was accepted")
	}
	multibyteTitle := rev7ADRFixture()
	multibyteTitle.Title = strings.Repeat("é", 128)
	if err := ValidateADR(multibyteTitle); err != nil {
		t.Fatalf("128 rune multibyte ADR title rejected: %v", err)
	}
	overSummary := rev7ADRFixture()
	overSummary.Summary = strings.Repeat("s", 257)
	if err := ValidateADR(overSummary); err == nil {
		t.Fatal("257 rune ADR summary was accepted")
	}
	blankSummary := rev7ADRFixture()
	blankSummary.Summary = "   "
	if err := ValidateADR(blankSummary); err == nil {
		t.Fatal("blank ADR summary was accepted")
	}
}

func TestTSK409Rev7HistoricalADRStillRequiresValidContent(t *testing.T) {
	for _, length := range []int{129, 300} {
		legacy := rev7ADRFixture()
		legacy.Summary = ""
		legacy.Title = strings.Repeat("t", length)
		if err := ValidateADRRevision(legacy, true); err != nil {
			t.Fatalf("legacy %d rune title rejected historically: %v", length, err)
		}
		if err := ValidateADR(legacy); err == nil {
			t.Fatalf("legacy %d rune title accepted as current state", length)
		}
	}
	historical := rev7ADRFixture()
	historical.Summary = ""
	historical.Title = strings.Repeat("t", 301)
	if err := ValidateADRRevision(historical, true); err == nil {
		t.Fatal("historical ADR beyond the legacy title bound was accepted")
	}
}
