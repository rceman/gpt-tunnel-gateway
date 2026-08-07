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
	return model.OwnerSummary{
		Status:              "ready",
		Goal:                "Deliver the reviewed work",
		CurrentlyDoing:      "Routing proof to Delivery",
		WhyItMatters:        "The owner needs a bounded decision",
		CompletedSoFar:      "The requested implementation is complete",
		NextStep:            "Delivery verifies the evidence",
		OwnerActionRequired: "Review the handoff",
	}
}

func handoffEvidence(terminal, reviewed bool) json.RawMessage {
	return json.RawMessage(`{"terminal":` + strings.ToLower(boolString(terminal)) + `,"reviewed":` + strings.ToLower(boolString(reviewed)) + `,"head":"` + strings.Repeat("a", 40) + `"}`)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
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
	handoff, created, err := s.DeliveryHandoffCreate(planner, DeliveryHandoffCreateInput{
		ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1",
		OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Status != model.DeliveryHandoffPending || len(created.Hub.Paths) != 1 {
		t.Fatalf("unexpected create result: %#v %#v", handoff, created)
	}
	acknowledged, acknowledgedOp, err := s.DeliveryHandoffAcknowledge(delivery, DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Status != model.DeliveryHandoffAcknowledged {
		t.Fatalf("unexpected acknowledgement: %#v", acknowledged)
	}
	report, published, err := s.PlannerReportPublish(delivery, PlannerReportPublishInput{
		HandoffID:    handoff.ID,
		Report:       model.PlannerReport{ReportType: model.PlannerReportBlocked, OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, true), PublishedBy: "delivery"},
		WriteOptions: WriteOptions{ExpectedHubRevision: acknowledgedOp.Hub.After},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(published.Hub.Paths) != 2 || report.ReportType != model.PlannerReportBlocked {
		t.Fatalf("report publication was not atomic: %#v %#v", report, published)
	}
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
	handoffStatuses, err := s.DeliveryHandoffList(ctx, "example")
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
	reportStatuses, err := s.PlannerReportList(ctx, "example")
	if err != nil {
		t.Fatal(err)
	}
	reportStatusJSON, err := json.Marshal(reportStatuses)
	if err != nil || strings.Contains(string(reportStatusJSON), "technical_evidence") {
		t.Fatalf("report list exposed technical evidence: %s %v", reportStatusJSON, err)
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
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), DeliveryHandoffCreateInput{ProjectID: task.ProjectID, TaskID: task.ID, RunID: run.ID, TaskSHA256: task.SHA256, OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	acknowledged, acknowledgedOp, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{HandoffID: acknowledged.ID, Report: model.PlannerReport{ReportType: model.PlannerReportCompleted, OwnerSummary: handoffSummary(), TechnicalEvidence: evidence, PublishedBy: "delivery"}, WriteOptions: WriteOptions{ExpectedHubRevision: acknowledgedOp.Hub.After}})
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
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), input)
	if err != nil {
		t.Fatal(err)
	}
	ack, acknowledged, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{HandoffID: handoff.ID, AcknowledgedBy: "delivery", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{HandoffID: ack.ID, Report: model.PlannerReport{ReportType: model.PlannerReportCompleted, OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), PublishedBy: "delivery"}, WriteOptions: WriteOptions{ExpectedHubRevision: acknowledged.Hub.After}})
	if err == nil || !strings.Contains(err.Error(), "terminal technical evidence") {
		t.Fatalf("invalid completed report was accepted: %v", err)
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != before {
		t.Fatalf("invalid report mutated hub: %s %v", got, err)
	}
}

func TestDeliveryHandoffSupersedeAndCancelAreAtomic(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-supersede")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), DeliveryHandoffCreateInput{ProjectID: "example", TaskID: "EXM-TSK1", RunID: "EXM-TSK1-RUN1", OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: revision}})
	if err != nil {
		t.Fatal(err)
	}
	replacement, superseded, err := s.DeliveryHandoffSupersede(authority.WithPlanner(ctx), DeliveryHandoffSupersedeInput{HandoffID: handoff.ID, OwnerSummary: handoffSummary(), TechnicalEvidence: handoffEvidence(false, false), CreatedBy: "planner", WriteOptions: WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.SupersedesHandoffID != handoff.ID || len(superseded.Hub.Paths) != 2 {
		t.Fatalf("supersession was not atomic: %#v %#v", replacement, superseded)
	}
	cancelled, cancelledOp, err := s.DeliveryHandoffCancel(authority.WithPlanner(ctx), DeliveryHandoffCancelInput{HandoffID: replacement.ID, CancelledBy: "planner", Reason: "owner withdrew the handoff", WriteOptions: WriteOptions{ExpectedHubRevision: superseded.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.DeliveryHandoffCancelled || len(cancelledOp.Hub.Paths) != 1 {
		t.Fatalf("cancellation was not persisted: %#v %#v", cancelled, cancelledOp)
	}
}
