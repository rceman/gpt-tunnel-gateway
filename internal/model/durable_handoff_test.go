package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testOwnerSummary() OwnerSummary {
	action := "Review and decide"
	return OwnerSummary{
		Status:              "working",
		Goal:                "Verify the requested change",
		CurrentlyDoing:      "Preparing bounded delivery evidence",
		WhyItMatters:        "The owner needs a clear handoff",
		CompletedSoFar:      []string{"The implementation is prepared"},
		NextStep:            "Delivery reviews the proof",
		OwnerActionRequired: &action,
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
	handoff := DeliveryHandoff{
		SchemaVersion:        DurableHandoffSchemaVersion,
		ID:                   "handoff-1",
		ProjectID:            "example",
		TaskID:               "EXM-TSK1",
		RunID:                "EXM-TSK1-RUN1",
		TaskSHA256:           strings.Repeat("a", 64),
		Status:               DeliveryHandoffPending,
		OwnerSummary:         testOwnerSummary(),
		TechnicalEvidence:    testTechnicalEvidence(false, false),
		PlanRevision:         1,
		HubRevision:          strings.Repeat("b", 40),
		TaskRefs:             []TaskRef{{TaskID: "EXM-TSK1", TaskSHA256: strings.Repeat("a", 64)}},
		TrainRefs:            []string{"train-1"},
		PlanSectionRefs:      []string{"section-1"},
		OperatorEventRefs:    []string{"event-1"},
		ExpectedRepoBase:     strings.Repeat("c", 40),
		ExpectedRepoHead:     strings.Repeat("d", 40),
		FirstAction:          "Review the durable task",
		StopBoundary:         "Stop before mutation without authorization",
		ProhibitedOperations: []string{"release"},
		InstructionBody:      "Execute only the bounded handoff contract",
		RoleRefs:             []string{"planner"},
		DelegationRefs:       []string{"delivery"},
		AuthorRole:           "planner",
		ConsumerRole:         "delivery",
		CreatedBy:            "planner",
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	handoff.CanonicalDigest, _ = CanonicalDeliveryHandoffDigest(handoff)
	return handoff
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
	handoff = testHandoff()
	handoff.OwnerSummary.Status = "ready"
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("unknown owner summary status was accepted")
	}
	handoff = testHandoff()
	handoff.OwnerSummary.CompletedSoFar = []string{"one", "two", "three", "four"}
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("more than three completed items were accepted")
	}
	handoff = testHandoff()
	handoff.CanonicalDigest = strings.Repeat("e", 64)
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("tampered immutable handoff digest was accepted")
	}
	handoff = testHandoff()
	handoff.Status = DeliveryHandoffCompleted
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("completed handoff without prior acknowledgement/start metadata was accepted")
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
	handoff.CanonicalDigest, _ = CanonicalDeliveryHandoffDigest(handoff)
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
	report.OwnerSummary.Status = PlannerReportCompleted
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

func TestPlannerReportTypeMatchesOwnerSummaryStatus(t *testing.T) {
	for _, reportType := range []string{PlannerReportCompleted, PlannerReportBlocked, PlannerReportDecisionRequired} {
		report := PlannerReport{SchemaVersion: DurableHandoffSchemaVersion, ID: "report-" + reportType, ProjectID: "example", HandoffID: "handoff-1", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", TaskSHA256: strings.Repeat("a", 64), ReportType: reportType, OwnerSummary: testOwnerSummary(), TechnicalEvidence: testTechnicalEvidence(false, false), PublishedBy: "delivery", PublishedAt: time.Now().UTC()}
		report.OwnerSummary.Status = reportType
		if err := ValidatePlannerReport(report); err != nil {
			t.Fatalf("valid %s report rejected: %v", reportType, err)
		}
		report.OwnerSummary.Status = "working"
		if err := ValidatePlannerReport(report); err == nil {
			t.Fatalf("mismatched %s report was accepted", reportType)
		}
	}
}
