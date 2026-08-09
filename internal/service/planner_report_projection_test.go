package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestPlannerReportCorrectionSupersedesWithAtomicJournal(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-report-correction")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), withHandoffPlan(s, DeliveryHandoffCreateInput{
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
	_, acknowledged, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{
		HandoffID:      handoff.ID,
		AcknowledgedBy: "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, nextOp, err := s.DeliveryHandoffNext(authority.WithDelivery(ctx), DeliveryHandoffNextInput{
		HandoffID: handoff.ID,
		NextBy:    "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: acknowledged.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked, published, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{
		HandoffID: handoff.ID,
		Report:    model.PlannerReport{ReportType: model.PlannerReportBlocked, OwnerSummary: handoffSummaryStatus(model.PlannerReportBlocked), TechnicalEvidence: blockedReportEvidence(), PublishedBy: "delivery"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: nextOp.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	corrected, correctionOp, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{
		HandoffID: handoff.ID,
		Report:    model.PlannerReport{ReportType: model.PlannerReportDecisionRequired, OwnerSummary: handoffSummaryStatus(model.PlannerReportDecisionRequired), TechnicalEvidence: decisionReportEvidence(), SupersedesReportID: blocked.ID, PublishedBy: "delivery"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: published.Hub.After,
		},
	})
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
