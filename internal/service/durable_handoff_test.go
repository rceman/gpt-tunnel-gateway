package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func handoffSummary() model.OwnerSummary {
	action := "Review the handoff"
	return model.OwnerSummary{
		Status:              "working",
		Goal:                "Deliver the reviewed work",
		CurrentlyDoing:      "Routing proof to Delivery",
		WhyItMatters:        "The owner needs a bounded decision",
		CompletedSoFar:      []string{"The requested implementation is complete"},
		NextStep:            "Delivery verifies the evidence",
		OwnerActionRequired: &action,
	}
}

func handoffSummaryStatus(status string) model.OwnerSummary {
	summary := handoffSummary()
	summary.Status = status
	return summary
}

func handoffEvidence(terminal, reviewed bool) json.RawMessage {
	return json.RawMessage(`{"terminal":` + strings.ToLower(boolString(terminal)) + `,"reviewed":` + strings.ToLower(boolString(reviewed)) + `,"head":"` + strings.Repeat("a", 40) + `"}`)
}

func blockedReportEvidence() json.RawMessage {
	return json.RawMessage(`{"blocker_class":"external_dependency","severity":"medium","failed_precondition":"required evidence was not available","verified_facts":["repository state was preserved"],"preservation_resume":"resume from the durable handoff","same_run_correction_available":false}`)
}

func decisionReportEvidence() json.RawMessage {
	return json.RawMessage(`{"decision_question":"which bounded next action is authorized","options":["wait","review"],"tradeoffs":"waiting preserves state","recommendation":"review the evidence","deferral_consequence":"no mutation occurs","preserved_state":"hub and worktree remain unchanged","unauthorized_choice_implemented":false}`)
}

func withHandoffPlan(s *Service, in DeliveryHandoffCreateInput, revision string) DeliveryHandoffCreateInput {
	plan, err := s.PlanRead(context.Background(), in.ProjectID)
	if err == nil {
		in.PlanRevision = plan.Revision
	}
	in.HubRevision = revision
	task, err := s.findTask(context.Background(), in.TaskID)
	if err == nil {
		in.TaskRefs = []model.TaskRef{{TaskID: task.ID, TaskSHA256: task.SHA256}}
	}
	in.TrainRefs = []string{"train-1"}
	in.PlanSectionRefs = nil
	in.OperatorEventRefs = nil
	in.FirstAction = "Review the durable task"
	in.StopBoundary = "Stop before mutation without authorization"
	in.ProhibitedOperations = []string{"release"}
	in.InstructionBody = "Execute only the bounded handoff contract"
	in.RoleRefs = []string{"planner"}
	in.DelegationRefs = []string{"delivery"}
	return in
}

func withSupersedePlan(s *Service, in DeliveryHandoffSupersedeInput, revision string) DeliveryHandoffSupersedeInput {
	handoff, err := s.findDeliveryHandoff(context.Background(), in.HandoffID)
	if err == nil {
		plan, planErr := s.PlanRead(context.Background(), handoff.ProjectID)
		if planErr == nil {
			in.PlanRevision = plan.Revision
		}
	}
	in.HubRevision = revision
	handoff, err = s.findDeliveryHandoff(context.Background(), in.HandoffID)
	if err == nil {
		in.TaskRefs = []model.TaskRef{{TaskID: handoff.TaskID, TaskSHA256: handoff.TaskSHA256}}
	}
	in.TrainRefs = []string{"train-1"}
	in.PlanSectionRefs = nil
	in.OperatorEventRefs = nil
	in.FirstAction = "Review the durable task"
	in.StopBoundary = "Stop before mutation without authorization"
	in.ProhibitedOperations = []string{"release"}
	in.InstructionBody = "Execute only the bounded handoff contract"
	in.RoleRefs = []string{"planner"}
	in.DelegationRefs = []string{"delivery"}
	return in
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func journalEventForOperation(t *testing.T, s *Service, operation OperationResult) model.OperatorJournalEvent {
	t.Helper()
	for _, path := range operation.Hub.Paths {
		if strings.Contains(path, "/operator-journal/events/") {
			var event model.OperatorJournalEvent
			if err := s.Hub.ReadJSON(context.Background(), path, &event); err != nil {
				t.Fatal(err)
			}
			return event
		}
	}
	t.Fatalf("operation did not append an operator event: %#v", operation.Hub.Paths)
	return model.OperatorJournalEvent{}
}

func assertHandoffJournalEvent(t *testing.T, event model.OperatorJournalEvent, number uint64, actor string, handoff model.DeliveryHandoff, identities ...string) {
	t.Helper()
	if event.Kind != model.OperatorOperation || event.Actor != actor || event.ProjectID != handoff.ProjectID {
		t.Fatalf("unexpected handoff journal event: %#v", event)
	}
	_, gotNumber, err := model.ParseOperatorEventID(event.ID)
	if err != nil || gotNumber != number {
		t.Fatalf("unexpected operator event ID %q: %v", event.ID, err)
	}
	if len(event.References.Tasks) != 1 || event.References.Tasks[0] != handoff.TaskID || len(event.References.Runs) != 1 || event.References.Runs[0] != handoff.RunID {
		t.Fatalf("journal task/run references are not exact: %#v", event.References)
	}
	want := append([]string{handoff.ID}, identities...)
	if len(event.References.Identities) != len(want) {
		t.Fatalf("journal identity references are not exact: got %#v want %#v", event.References.Identities, want)
	}
	for i := range want {
		if event.References.Identities[i] != want[i] {
			t.Fatalf("journal identity references are not exact: got %#v want %#v", event.References.Identities, want)
		}
	}
}

func assertJournalCounter(t *testing.T, s *Service, want uint64) {
	t.Helper()
	var counter model.OperatorJournalCounter
	if err := s.Hub.ReadJSON(context.Background(), s.operatorCounterPath("example"), &counter); err != nil {
		t.Fatal(err)
	}
	if counter.NextEventNumber != want {
		t.Fatalf("unexpected operator counter: got %d want %d", counter.NextEventNumber, want)
	}
}

func TestDeliveryHandoffLifecycleAndAtomicReportPublication(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff")
	ctx := context.Background()
	planner := authority.WithPlanner(ctx)
	delivery := authority.WithDelivery(ctx)
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handoff, created, err := s.DeliveryHandoffCreate(planner, withHandoffPlan(s, DeliveryHandoffCreateInput{
		ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1",
		OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	}, revision))
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Status != model.DeliveryHandoffPending || len(created.Hub.Paths) != 3 {
		t.Fatalf("unexpected create result: %#v %#v", handoff, created)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, created), 1, "planner", handoff)
	assertJournalCounter(t, s, 2)
	acknowledged, acknowledgedOp, err := s.DeliveryHandoffAcknowledge(delivery, DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Status != model.DeliveryHandoffAcknowledged {
		t.Fatalf("unexpected acknowledgement: %#v", acknowledged)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, acknowledgedOp), 2, "delivery", handoff)
	assertJournalCounter(t, s, 3)
	next, nextOp, err := s.DeliveryHandoffNext(delivery, DeliveryHandoffNextInput{HandoffID: handoff.ID, NextBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: acknowledgedOp.Hub.After}})
	if err != nil || next.Status != model.DeliveryHandoffInProgress {
		t.Fatalf("handoff did not enter in_progress: %#v %v", next, err)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, nextOp), 3, "delivery", handoff)
	assertJournalCounter(t, s, 4)
	report, published, err := s.PlannerReportPublish(delivery, PlannerReportPublishInput{
		HandoffID:    handoff.ID,
		Report:       model.PlannerReport{ReportType: model.PlannerReportBlocked, OwnerSummary: handoffSummaryStatus(model.PlannerReportBlocked), TechnicalEvidence: blockedReportEvidence(), PublishedBy: "delivery"},
		WriteOptions: WriteOptions{ExpectedHubRevision: nextOp.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(published.Hub.Paths) != 5 || report.ReportType != model.PlannerReportBlocked {
		t.Fatalf("report publication was not atomic: %#v %#v", report, published)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, published), 4, "delivery", next, report.ID)
	assertJournalCounter(t, s, 5)
	current, err := s.DeliveryHandoffRead(ctx, handoff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != model.DeliveryHandoffBlocked || current.CurrentReportID != report.ID {
		t.Fatalf("handoff transition missing: %#v", current)
	}
	readReport, err := s.PlannerReportRead(ctx, report.ID)
	if err != nil || readReport.ID != report.ID {
		t.Fatalf("published report was not readable: %#v %v", readReport, err)
	}
	ackState, ackReportOp, err := s.PlannerReportAcknowledge(authority.WithPlanner(ctx), PlannerReportAcknowledgeInput{ReportID: report.ID, AcknowledgedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: published.Hub.After}})
	if err != nil || ackState.Status != model.PlannerReportAcknowledged {
		t.Fatalf("planner report acknowledgement failed: %#v %v", ackState, err)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, ackReportOp), 5, "planner", next, report.ID)
	assertJournalCounter(t, s, 6)
	resolvedState, resolvedReportOp, err := s.PlannerReportNext(authority.WithPlanner(ctx), PlannerReportNextInput{ReportID: report.ID, ResolvedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: ackReportOp.Hub.After}})
	if err != nil || resolvedState.Status != model.PlannerReportResolved {
		t.Fatalf("planner report resolution failed: %#v %v", resolvedState, err)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, resolvedReportOp), 6, "planner", next, report.ID)
	assertJournalCounter(t, s, 7)
	_ = resolvedReportOp
	handoffStatuses, err := s.DeliveryHandoffList(ctx, DeliveryHandoffListInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(handoffStatuses) != 1 {
		t.Fatalf("unexpected handoff status count: %#v", handoffStatuses)
	}
	statusJSON, err := json.Marshal(handoffStatuses[0])
	if err != nil || strings.Contains(string(statusJSON), "technical_evidence") {
		t.Fatalf("handoff list exposed technical evidence: %s %v", statusJSON, err)
	}
	reportStatuses, err := s.PlannerReportList(ctx, PlannerReportListInput{ProjectID: "example"})
	if err != nil {
		t.Fatal(err)
	}
	reportStatusJSON, err := json.Marshal(reportStatuses)
	if err != nil || strings.Contains(string(reportStatusJSON), "technical_evidence") {
		t.Fatalf("report list exposed technical evidence: %s %v", reportStatusJSON, err)
	}
	if len(reportStatuses) != 1 || reportStatuses[0].Status != model.PlannerReportResolved {
		t.Fatalf("report lifecycle status was not projected: %#v", reportStatuses)
	}
	if _, err := s.DeliveryHandoffList(ctx, DeliveryHandoffListInput{ProjectID: "example", Limit: s.Config.MaxListItems + 1}); err == nil {
		t.Fatal("over-limit handoff list was accepted")
	}
	if _, err := s.PlannerReportList(ctx, PlannerReportListInput{ProjectID: "example", Limit: s.Config.MaxListItems + 1}); err == nil {
		t.Fatal("over-limit report list was accepted")
	}
}

func TestCompletedPlannerReportRequiresImmutableDeliveryProof(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	delivery := finalizeAcceptedDeliveryReview(t, s, task, run)
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	evidence := json.RawMessage(`{"terminal":true,"reviewed":true,"task_sha256":"` + task.SHA256 + `","run_id":"` + run.ID + `","delivery_report_id":"` + delivery.ID + `","reviewed_head":"` + delivery.ReviewedHead + `"}`)
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), withHandoffPlan(s, DeliveryHandoffCreateInput{ProjectID: task.ProjectID, TaskID: task.ID, RunID: run.ID, TaskSHA256: task.SHA256, OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}, revision))
	if err != nil {
		t.Fatal(err)
	}
	_, acknowledgedOp, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	next, nextOp, err := s.DeliveryHandoffNext(authority.WithDelivery(ctx), DeliveryHandoffNextInput{HandoffID: handoff.ID, NextBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: acknowledgedOp.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{HandoffID: next.ID, Report: model.PlannerReport{ReportType: model.PlannerReportCompleted, OwnerSummary: handoffSummaryStatus(model.PlannerReportCompleted), TechnicalEvidence: evidence, PublishedBy: "delivery"}, WriteOptions: WriteOptions{ExpectedHubRevision: nextOp.Hub.After}})
	if err != nil {
		t.Fatalf("completed report with exact immutable proof was rejected: %v", err)
	}
	if report.ReportType != model.PlannerReportCompleted {
		t.Fatalf("unexpected completed report: %#v", report)
	}
}

func TestDeliveryHandoffAuthorityAndTerminalReportFailClosedWithoutMutation(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-authority")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := DeliveryHandoffCreateInput{ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}
	if _, _, err := s.DeliveryHandoffCreate(ctx, input); err == nil || !strings.Contains(err.Error(), "AUTHORITY_UNAVAILABLE") {
		t.Fatalf("unauthorized handoff mutation was accepted: %v", err)
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != revision {
		t.Fatalf("unauthorized call mutated hub: %s %v", got, err)
	}
	input = withHandoffPlan(s, input, revision)
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), input)
	if err != nil {
		t.Fatal(err)
	}
	_, acknowledged, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	next, nextOp, err := s.DeliveryHandoffNext(authority.WithDelivery(ctx), DeliveryHandoffNextInput{HandoffID: handoff.ID, NextBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: acknowledged.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalidCompletedEvidence := json.RawMessage(`{"terminal":false,"reviewed":true,"task_sha256":"` + strings.Repeat("a", 64) + `","run_id":"EXM-TSK1-RUN1","delivery_report_id":"report-proof","reviewed_head":"` + strings.Repeat("b", 40) + `"}`)
	_, _, err = s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{HandoffID: next.ID, Report: model.PlannerReport{ReportType: model.PlannerReportCompleted, OwnerSummary: handoffSummaryStatus(model.PlannerReportCompleted), TechnicalEvidence: invalidCompletedEvidence, PublishedBy: "delivery"}, WriteOptions: WriteOptions{ExpectedHubRevision: nextOp.Hub.After}})
	if err == nil {
		t.Fatal("invalid completed report was accepted")
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != before {
		t.Fatalf("invalid report mutated hub: %s %v", got, err)
	}
}

func TestDeliveryHandoffCreateReferenceValidationIsZeroMutation(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-create-refs")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := withHandoffPlan(s, DeliveryHandoffCreateInput{ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}, revision)
	cases := []struct {
		name   string
		mutate func(*DeliveryHandoffCreateInput)
	}{
		{name: "missing plan section", mutate: func(in *DeliveryHandoffCreateInput) { in.PlanSectionRefs = []string{"missing-section"} }},
		{name: "missing operator event", mutate: func(in *DeliveryHandoffCreateInput) { in.OperatorEventRefs = []string{"EXM-OPR1"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.mutate(&input)
			before, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), input); err == nil {
				t.Fatal("invalid reference was accepted")
			}
			after, err := s.Hub.RemoteRevision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("invalid reference mutated hub: %s -> %s", before, after)
			}
		})
	}
}

func TestDeliveryHandoffSupersedeAndCancelAreAtomic(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-supersede")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), withHandoffPlan(s, DeliveryHandoffCreateInput{ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}, revision))
	if err != nil {
		t.Fatal(err)
	}
	replacement, superseded, err := s.DeliveryHandoffSupersede(authority.WithPlanner(ctx), withSupersedePlan(s, DeliveryHandoffSupersedeInput{HandoffID: handoff.ID, OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}}, created.Hub.After))
	if err != nil {
		t.Fatal(err)
	}
	if replacement.SupersedesHandoffID != handoff.ID || len(superseded.Hub.Paths) != 4 {
		t.Fatalf("supersession was not atomic: %#v %#v", replacement, superseded)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, superseded), 2, "planner", replacement, handoff.ID)
	assertJournalCounter(t, s, 3)
	cancelled, cancelledOp, err := s.DeliveryHandoffCancel(authority.WithPlanner(ctx), DeliveryHandoffCancelInput{HandoffID: replacement.ID, CancelledBy: "planner", Reason: "owner withdrew the handoff", WriteOptions: WriteOptions{ExpectedHubRevision: superseded.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.DeliveryHandoffCancelled || len(cancelledOp.Hub.Paths) != 3 {
		t.Fatalf("cancellation was not persisted: %#v %#v", cancelled, cancelledOp)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, cancelledOp), 3, "planner", replacement)
	assertJournalCounter(t, s, 4)
}

func TestPlannerReportCorrectionSupersedesWithAtomicJournal(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-report-correction")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), withHandoffPlan(s, DeliveryHandoffCreateInput{ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}}, revision))
	if err != nil {
		t.Fatal(err)
	}
	_, acknowledged, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	_, nextOp, err := s.DeliveryHandoffNext(authority.WithDelivery(ctx), DeliveryHandoffNextInput{HandoffID: handoff.ID, NextBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: acknowledged.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	blocked, published, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{HandoffID: handoff.ID, Report: model.PlannerReport{ReportType: model.PlannerReportBlocked, OwnerSummary: handoffSummaryStatus(model.PlannerReportBlocked), TechnicalEvidence: blockedReportEvidence(), PublishedBy: "delivery"}, WriteOptions: WriteOptions{ExpectedHubRevision: nextOp.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	corrected, correctionOp, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{HandoffID: handoff.ID, Report: model.PlannerReport{ReportType: model.PlannerReportDecisionRequired, OwnerSummary: handoffSummaryStatus(model.PlannerReportDecisionRequired), TechnicalEvidence: decisionReportEvidence(), SupersedesReportID: blocked.ID, PublishedBy: "delivery"}, WriteOptions: WriteOptions{ExpectedHubRevision: published.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	oldState, err := s.plannerReportStateReadInProject(ctx, "example", blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	newState, err := s.plannerReportStateReadInProject(ctx, "example", corrected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldState.Status != model.PlannerReportSuperseded || newState.Status != model.PlannerReportPublished {
		t.Fatalf("report correction lifecycle was not atomic: old=%#v new=%#v", oldState, newState)
	}
	current, err := s.DeliveryHandoffRead(ctx, handoff.ID)
	if err != nil || current.CurrentReportID != corrected.ID || current.Status != model.DeliveryHandoffAwaitingDecision {
		t.Fatalf("corrected report is not current: %#v %v", current, err)
	}
	if len(correctionOp.Hub.Paths) != 6 {
		t.Fatalf("correction transaction did not include old state, new report/state, handoff and journal: %#v", correctionOp.Hub.Paths)
	}
	event := journalEventForOperation(t, s, correctionOp)
	if event.Kind != model.OperatorOperation || event.Actor != "delivery" || len(event.References.Identities) != 3 {
		t.Fatalf("correction journal event metadata is incomplete: %#v", event)
	}
	identitySet := map[string]bool{}
	for _, identity := range event.References.Identities {
		identitySet[identity] = true
	}
	for _, identity := range []string{handoff.ID, corrected.ID, blocked.ID} {
		if !identitySet[identity] {
			t.Fatalf("correction journal omitted identity %q: %#v", identity, event.References.Identities)
		}
	}
	assertJournalCounter(t, s, 6)
}
