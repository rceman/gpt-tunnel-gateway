package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

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
		ProjectID:         "example",
		TaskID:            "EXM-TSK1",
		RunID:             "EXM-TSK1-RUN1",
		OwnerSummary:      handoffSummary(),
		TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy:         "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}, revision))
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Status != model.DeliveryHandoffPending || len(created.Hub.Paths) != 3 {
		t.Fatalf("unexpected create result: %#v %#v", handoff, created)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, created), 1, "planner", handoff)
	assertJournalCounter(t, s, 2)
	acknowledged, acknowledgedOp, err := s.DeliveryHandoffAcknowledge(delivery, DeliveryHandoffAcknowledgeInput{
		HandoffID:      handoff.ID,
		AcknowledgedBy: "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Status != model.DeliveryHandoffAcknowledged {
		t.Fatalf("unexpected acknowledgement: %#v", acknowledged)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, acknowledgedOp), 2, "delivery", handoff)
	assertJournalCounter(t, s, 3)
	next, nextOp, err := s.DeliveryHandoffNext(delivery, DeliveryHandoffNextInput{
		HandoffID: handoff.ID,
		NextBy:    "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: acknowledgedOp.Hub.After,
		},
	})
	if err != nil || next.Status != model.DeliveryHandoffInProgress {
		t.Fatalf("handoff did not enter in_progress: %#v %v", next, err)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, nextOp), 3, "delivery", handoff)
	assertJournalCounter(t, s, 4)
	report, published, err := s.PlannerReportPublish(delivery, PlannerReportPublishInput{
		HandoffID: handoff.ID,
		Report:    model.PlannerReport{ReportType: model.PlannerReportBlocked, OwnerSummary: handoffSummaryStatus(model.PlannerReportBlocked), TechnicalEvidence: blockedReportEvidence(), PublishedBy: "delivery"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: nextOp.Hub.After,
		},
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
	status, err := s.DeliveryHandoffStatus(ctx, handoff.ID)
	if err != nil || status.OwnerSummary.Status != model.PlannerReportBlocked {
		t.Fatalf("blocked report summary was not projected: %#v %v", status, err)
	}
	readReport, err := s.PlannerReportRead(ctx, report.ID)
	if err != nil || readReport.ID != report.ID {
		t.Fatalf("published report was not readable: %#v %v", readReport, err)
	}
	reportStatus, err := s.PlannerReportStatus(ctx, report.ID)
	if err != nil || reportStatus.Status != model.PlannerReportPublished {
		t.Fatalf("report status projection was not bound to immutable report state: %#v %v", reportStatus, err)
	}
	ackState, ackReportOp, err := s.PlannerReportAcknowledge(authority.WithPlanner(ctx), PlannerReportAcknowledgeInput{
		ReportID:       report.ID,
		AcknowledgedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: published.Hub.After,
		},
	})
	if err != nil || ackState.Status != model.PlannerReportAcknowledged {
		t.Fatalf("planner report acknowledgement failed: %#v %v", ackState, err)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, ackReportOp), 5, "planner", next, report.ID)
	assertJournalCounter(t, s, 6)
	resolvedState, resolvedReportOp, err := s.PlannerReportNext(authority.WithPlanner(ctx), PlannerReportNextInput{
		ReportID:   report.ID,
		ResolvedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: ackReportOp.Hub.After,
		},
	})
	if err != nil || resolvedState.Status != model.PlannerReportResolved {
		t.Fatalf("planner report resolution failed: %#v %v", resolvedState, err)
	}
	resumed, err := s.DeliveryHandoffRead(ctx, handoff.ID)
	if err != nil || resumed.Status != model.DeliveryHandoffInProgress {
		t.Fatalf("blocked handoff did not resume after report resolution: %#v %v", resumed, err)
	}
	resumedStatus, err := s.DeliveryHandoffStatus(ctx, handoff.ID)
	if err != nil || resumedStatus.OwnerSummary.Status != handoffSummary().Status || resumedStatus.OwnerSummary.Goal != handoffSummary().Goal {
		t.Fatalf("resumed handoff did not restore original working summary: %#v %v", resumedStatus, err)
	}
	later, laterOp, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{
		HandoffID: handoff.ID,
		Report: model.PlannerReport{
			ReportType:        model.PlannerReportBlocked,
			OwnerSummary:      handoffSummaryStatus(model.PlannerReportBlocked),
			TechnicalEvidence: blockedReportEvidence(),
			PublishedBy:       "delivery",
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: resolvedReportOp.Hub.After,
		},
	})
	if err != nil || later.SupersedesReportID != "" {
		t.Fatalf("resumed handoff did not accept a fresh report: %#v %v", later, err)
	}
	current, err = s.DeliveryHandoffRead(ctx, handoff.ID)
	if err != nil || current.Status != model.DeliveryHandoffBlocked || current.CurrentReportID != later.ID {
		t.Fatalf("fresh report was not made current after resume: %#v %v", current, err)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, laterOp), 7, "delivery", next, later.ID)
	assertJournalCounter(t, s, 8)
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, resolvedReportOp), 6, "planner", next, report.ID)
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
	if len(reportStatuses) != 2 {
		t.Fatalf("report lifecycle status was not projected: %#v", reportStatuses)
	}
	seenResolved, seenPublished := false, false
	for _, item := range reportStatuses {
		if item.ID == report.ID && item.Status == model.PlannerReportResolved {
			seenResolved = true
		}
		if item.ID == later.ID && item.Status == model.PlannerReportPublished {
			seenPublished = true
		}
	}
	if !seenResolved || !seenPublished {
		t.Fatalf("report lifecycle statuses were not projected: %#v", reportStatuses)
	}
	if _, err := s.DeliveryHandoffList(ctx, DeliveryHandoffListInput{
		ProjectID: "example",
		Limit:     s.Config.MaxListItems + 1,
	}); err == nil {
		t.Fatal("over-limit handoff list was accepted")
	}
	if _, err := s.PlannerReportList(ctx, PlannerReportListInput{
		ProjectID: "example",
		Limit:     s.Config.MaxListItems + 1,
	}); err == nil {
		t.Fatal("over-limit report list was accepted")
	}
}
