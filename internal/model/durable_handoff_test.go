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

func testCompletedEvidence(terminal, reviewed bool) json.RawMessage {
	return json.RawMessage(`{"terminal":` + boolText(terminal) + `,"reviewed":` + boolText(reviewed) + `,"task_sha256":"` + strings.Repeat("a", 64) + `","run_id":"EXM-TSK1-RUN1","delivery_report_id":"report-proof","reviewed_head":"` + strings.Repeat("b", 40) + `"}`)
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

func TestDurableHandoffRejectsDuplicateReferences(t *testing.T) {
	handoff := testHandoff()
	handoff.TaskRefs = append(handoff.TaskRefs, handoff.TaskRefs[0])
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("duplicate task reference was accepted")
	}
	handoff = testHandoff()
	handoff.TrainRefs = []string{"train-1", "train-1"}
	if err := ValidateDeliveryHandoff(handoff); err == nil {
		t.Fatal("duplicate bounded reference was accepted")
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
		TechnicalEvidence: testCompletedEvidence(true, true),
		PublishedBy:       "delivery",
		PublishedAt:       time.Now().UTC(),
	}
	report.OwnerSummary.Status = PlannerReportCompleted
	if err := ValidatePlannerReport(report); err != nil {
		t.Fatal(err)
	}
	report.TechnicalEvidence = testCompletedEvidence(false, true)
	if err := PlannerReportRequiresTerminalEvidence(report); err == nil {
		t.Fatal("unreviewed terminal report evidence was accepted")
	}
	if err := ValidatePlannerReport(report); err == nil {
		t.Fatal("invalid completed report evidence was accepted")
	}
	report.ReportType = "unknown"
	if err := ValidatePlannerReport(report); err == nil {
		t.Fatal("unknown report type was accepted")
	}
}

func TestPlannerReportTypeMatchesOwnerSummaryStatus(t *testing.T) {
	for _, reportType := range []string{PlannerReportCompleted, PlannerReportBlocked, PlannerReportDecisionRequired} {
		evidence := testCompletedEvidence(true, true)
		if reportType == PlannerReportBlocked {
			evidence = json.RawMessage(`{"blocker_class":"dependency","severity":"high","failed_precondition":"approval missing","verified_facts":["state preserved"],"preservation_resume":"resume from the durable receipt","same_run_correction_available":false}`)
		}
		if reportType == PlannerReportDecisionRequired {
			evidence = json.RawMessage(`{"decision_question":"choose next action","options":["wait","review"],"tradeoffs":"waiting preserves state","recommendation":"review","deferral_consequence":"work remains paused","preserved_state":"no mutation","unauthorized_choice_implemented":false}`)
		}
		report := PlannerReport{
			SchemaVersion:     DurableHandoffSchemaVersion,
			ID:                "report-" + reportType,
			ProjectID:         "example",
			HandoffID:         "handoff-1",
			TaskID:            "EXM-TSK1",
			RunID:             "EXM-TSK1-RUN1",
			TaskSHA256:        strings.Repeat("a", 64),
			ReportType:        reportType,
			OwnerSummary:      testOwnerSummary(),
			TechnicalEvidence: evidence,
			PublishedBy:       "delivery",
			PublishedAt:       time.Now().UTC(),
		}
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

func TestPlannerReportTypedBlockedAndDecisionEvidence(t *testing.T) {
	blockedEvidence := json.RawMessage(`{"blocker_class":"dependency","severity":"high","failed_precondition":"approval missing","verified_facts":["state preserved"],"preservation_resume":"resume from the durable receipt","same_run_correction_available":false}`)
	decisionEvidence := json.RawMessage(`{"decision_question":"choose next action","options":["wait","review"],"tradeoffs":"waiting preserves state","recommendation":"review","deferral_consequence":"work remains paused","preserved_state":"no mutation","unauthorized_choice_implemented":false}`)
	for _, tc := range []struct {
		name     string
		typeName string
		evidence json.RawMessage
	}{
		{name: "blocked", typeName: PlannerReportBlocked, evidence: blockedEvidence},
		{name: "decision", typeName: PlannerReportDecisionRequired, evidence: decisionEvidence},
	} {
		report := PlannerReport{
			SchemaVersion:     DurableHandoffSchemaVersion,
			ID:                "report-" + tc.name,
			ProjectID:         "example",
			HandoffID:         "handoff-1",
			TaskID:            "EXM-TSK1",
			RunID:             "EXM-TSK1-RUN1",
			TaskSHA256:        strings.Repeat("a", 64),
			ReportType:        tc.typeName,
			OwnerSummary:      testOwnerSummary(),
			TechnicalEvidence: tc.evidence,
			PublishedBy:       "delivery",
			PublishedAt:       time.Now().UTC(),
		}
		report.OwnerSummary.Status = tc.typeName
		if err := ValidatePlannerReport(report); err != nil {
			t.Fatalf("valid %s evidence rejected: %v", tc.name, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(tc.evidence, &fields); err != nil {
			t.Fatal(err)
		}
		if tc.typeName == PlannerReportBlocked {
			fields["same_run_correction_available"] = json.RawMessage(`true`)
		} else {
			fields["unauthorized_choice_implemented"] = json.RawMessage(`true`)
		}
		invalid, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		report.TechnicalEvidence = invalid
		if err := ValidatePlannerReport(report); err == nil {
			t.Fatalf("malformed %s evidence was accepted", tc.name)
		}
	}
}
