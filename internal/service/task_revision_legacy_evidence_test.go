package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const legacyTaskRevisionFixtureHash = "81205258b251f032b6398f70346b0d176acdb6c2a82b15cd2004d325533b758b"

func legacyTaskRevisionFixture() LegacyTaskRevisionEvidence {
	return LegacyTaskRevisionEvidence{
		SchemaVersion:          1,
		ID:                     "EXM-TSK999.REV2",
		TaskID:                 "EXM-TSK999",
		TaskRevision:           2,
		RevisionSHA256:         legacyTaskRevisionFixtureHash,
		ParentTaskRevision:     1,
		ParentTaskSHA256:       strings.Repeat("a", 64),
		ProjectID:              "example",
		Title:                  "Legacy second revision",
		Objective:              "Preserve historical evidence.",
		Branch:                 "task/example",
		BaseRevision:           "base",
		AcceptanceCriteria:     []string{"one"},
		Constraints:            []string{"none"},
		RequiredGates:          []string{"check"},
		WorkflowPolicyRevision: 1,
		OperationClass:         "implementation",
		EffectiveCIField:       "task",
		EffectiveCIMode:        "disabled",
		Status:                 "created",
		SourceRunID:            "EXM-TSK999-RUN1",
		SourceReportID:         "EXM-TSK999-RUN1-REPORT",
		CreatedBy:              "planner",
		CreatedAt:              time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestTSK531LegacyEvidenceHistoricalWireHashAndStrictFields(t *testing.T) {
	fixture := legacyTaskRevisionFixture()
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var decoded LegacyTaskRevisionEvidence
	if err := readLegacyTaskRevisionJSON(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.validate("example", "EXM-TSK999"); err != nil {
		t.Fatalf("historical wire fixture rejected: %v", err)
	}
	if decoded.SourceRunID != "EXM-TSK999-RUN1" || decoded.SourceReportID != "EXM-TSK999-RUN1-REPORT" {
		t.Fatalf("historical source fields lost: %#v", decoded)
	}

	for name, mutate := range map[string]func(*LegacyTaskRevisionEvidence){
		"missing hash": func(value *LegacyTaskRevisionEvidence) { value.RevisionSHA256 = "" },
		"wrong hash":   func(value *LegacyTaskRevisionEvidence) { value.RevisionSHA256 = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			value := fixture
			mutate(&value)
			if err := value.validate("example", "EXM-TSK999"); err == nil {
				t.Fatal("invalid historical hash unexpectedly validated")
			}
		})
	}

	unknown := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"source_future_id":"future"}`)...)
	if err := readLegacyTaskRevisionJSON(unknown, &decoded); err == nil {
		t.Fatal("unknown historical field unexpectedly accepted")
	}
}

func TestTSK531LegacyEvidenceRejectsOwnershipAndParentMismatches(t *testing.T) {
	for name, mutate := range map[string]func(*LegacyTaskRevisionEvidence){
		"project":         func(value *LegacyTaskRevisionEvidence) { value.ProjectID = "other" },
		"task":            func(value *LegacyTaskRevisionEvidence) { value.TaskID = "EXM-TSK998" },
		"parent revision": func(value *LegacyTaskRevisionEvidence) { value.ParentTaskRevision = 0 },
		"parent hash":     func(value *LegacyTaskRevisionEvidence) { value.ParentTaskSHA256 = strings.Repeat("c", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			value := legacyTaskRevisionFixture()
			mutate(&value)
			if err := value.validate("example", "EXM-TSK999"); err == nil {
				t.Fatal("invalid legacy identity or parent unexpectedly validated")
			}
		})
	}
}
