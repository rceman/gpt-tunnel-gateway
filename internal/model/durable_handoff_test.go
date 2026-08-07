package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testOwnerSummary() OwnerSummary {
	return OwnerSummary{
		Status:              "ready",
		Goal:                "Verify the requested change",
		CurrentlyDoing:      "Preparing bounded delivery evidence",
		WhyItMatters:        "The owner needs a clear handoff",
		CompletedSoFar:      "The implementation is prepared",
		NextStep:            "Delivery reviews the proof",
		OwnerActionRequired: "Review and decide",
	}
}

func testTechnicalEvidence(terminal, reviewed bool) json.RawMessage {
	return json.RawMessage(`{"terminal":` + boolText(terminal) + `,"reviewed":` + boolText(reviewed) + `,"proof":"bounded"}`)
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func testHandoff() DeliveryHandoff {
	now := time.Now().UTC()
	return DeliveryHandoff{
		SchemaVersion:     DurableHandoffSchemaVersion,
		ID:                "handoff-1",
		ProjectID:         "example",
		TaskID:            "EXM-TSK1",
		RunID:             "EXM-TSK1-RUN1",
		TaskSHA256:        strings.Repeat("a", 64),
		Status:            DeliveryHandoffPending,
		OwnerSummary:      testOwnerSummary(),
		TechnicalEvidence: testTechnicalEvidence(false, false),
		CreatedBy:         "planner",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func TestDurableHandoffRequiresCompleteOwnerSummary(t *testing.T) {
	handoff := testHandoff()
	handoff.OwnerSummary.NextStep = ""
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("missing owner summary field was accepted")
	}
	handoff = testHandoff()
	handoff.OwnerSummary.Goal = strings.Repeat("x", MaxOwnerSummaryFieldBytes+1)
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("oversized owner summary field was accepted")
	}
}

func TestDurableHandoffSeparatesObjectTechnicalEvidence(t *testing.T) {
	handoff := testHandoff()
	handoff.TechnicalEvidence = json.RawMessage(`"not-an-object"`)
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("scalar technical evidence was accepted")
	}
	handoff = testHandoff()
	handoff.TechnicalEvidence = json.RawMessage(`{"terminal":true}`)
	if err := ValidateDeliveryHandoff(handoff); err != nil {
		t.Fatal(err)
	}
}

func TestPlannerReportTypeAndTerminalEvidenceAreClosed(t *testing.T) {
	report := PlannerReport{
		SchemaVersion:     DurableHandoffSchemaVersion,
		ID:                "report-1",
		ProjectID:         "example",
		HandoffID:         "handoff-1",
		TaskID:            "EXM-TSK1",
		RunID:             "EXM-TSK1-RUN1",
		TaskSHA256:        strings.Repeat("a", 64),
		ReportType:        PlannerReportCompleted,
		OwnerSummary:      testOwnerSummary(),
		TechnicalEvidence: testTechnicalEvidence(false, true),
		PublishedBy:       "delivery",
		PublishedAt:       time.Now().UTC(),
	}
	if err := ValidatePlannerReport(report); err != nil {
		t.Fatal(err)
	}
	if err := PlannerReportRequiresTerminalEvidence(report); err == nil {
		t.Fatal("unreviewed terminal report evidence was accepted")
	}
	report.ReportType = "unknown"
	if err := ValidatePlannerReport(report); err == nil {
		t.Fatal("unknown report type was accepted")
	}
}
