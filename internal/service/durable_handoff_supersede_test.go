package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestCompletedPlannerReportRequiresImmutableDeliveryProof(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	delivery := finalizeAcceptedDeliveryReview(t, s, task, run)
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	evidence := json.RawMessage(`{"terminal":true,"reviewed":true,"task_sha256":"` + task.SHA256 + `","run_id":"` + run.ID + `","delivery_report_id":"` + delivery.ID + `","reviewed_head":"` + delivery.ReviewedHead + `"}`)
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), withHandoffPlan(s, DeliveryHandoffCreateInput{
		ProjectID:         task.ProjectID,
		TaskID:            task.ID,
		RunID:             run.ID,
		TaskSHA256:        task.SHA256,
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
	_, acknowledgedOp, err := s.DeliveryHandoffAcknowledge(authority.WithDelivery(ctx), DeliveryHandoffAcknowledgeInput{
		HandoffID:      handoff.ID,
		AcknowledgedBy: "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	next, nextOp, err := s.DeliveryHandoffNext(authority.WithDelivery(ctx), DeliveryHandoffNextInput{
		HandoffID: handoff.ID,
		NextBy:    "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: acknowledgedOp.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, published, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{
		HandoffID: next.ID,
		Report:    model.PlannerReport{ReportType: model.PlannerReportCompleted, OwnerSummary: handoffSummaryStatus(model.PlannerReportCompleted), TechnicalEvidence: evidence, PublishedBy: "delivery"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: nextOp.Hub.After,
		},
	})
	if err != nil {
		t.Fatalf("completed report with exact immutable proof was rejected: %v", err)
	}
	if report.ReportType != model.PlannerReportCompleted {
		t.Fatalf("unexpected completed report: %#v", report)
	}
	status, err := s.DeliveryHandoffStatus(ctx, handoff.ID)
	if err != nil || status.Status != model.DeliveryHandoffCompleted || status.OwnerSummary.Status != model.PlannerReportCompleted {
		t.Fatalf("completed report summary was not projected: %#v %v", status, err)
	}
	_, acknowledged, err := s.PlannerReportAcknowledge(authority.WithPlanner(ctx), PlannerReportAcknowledgeInput{
		ReportID:       report.ID,
		AcknowledgedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: published.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PlannerReportNext(authority.WithPlanner(ctx), PlannerReportNextInput{
		ReportID:   report.ID,
		ResolvedBy: "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: acknowledged.Hub.After,
		},
	}); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.DeliveryHandoffRead(ctx, handoff.ID)
	if err != nil || terminal.Status != model.DeliveryHandoffCompleted || terminal.CurrentReportID != report.ID {
		t.Fatalf("completed handoff was resumed unexpectedly: %#v %v", terminal, err)
	}
}

func TestDeliveryHandoffAuthorityAndTerminalReportFailClosedWithoutMutation(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-authority")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := DeliveryHandoffCreateInput{
		ProjectID:         "example",
		TaskID:            "EXM-TSK1",
		RunID:             "EXM-TSK1-RUN1",
		OwnerSummary:      handoffSummary(),
		TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy:         "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}
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
	next, nextOp, err := s.DeliveryHandoffNext(authority.WithDelivery(ctx), DeliveryHandoffNextInput{
		HandoffID: handoff.ID,
		NextBy:    "delivery",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: acknowledged.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invalidCompletedEvidence := json.RawMessage(`{"terminal":false,"reviewed":true,"task_sha256":"` + strings.Repeat("a", 64) + `","run_id":"EXM-TSK1-RUN1","delivery_report_id":"report-proof","reviewed_head":"` + strings.Repeat("b", 40) + `"}`)
	_, _, err = s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{
		HandoffID: next.ID,
		Report:    model.PlannerReport{ReportType: model.PlannerReportCompleted, OwnerSummary: handoffSummaryStatus(model.PlannerReportCompleted), TechnicalEvidence: invalidCompletedEvidence, PublishedBy: "delivery"},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: nextOp.Hub.After,
		},
	})
	if err == nil {
		t.Fatal("invalid completed report was accepted")
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != before {
		t.Fatalf("invalid report mutated hub: %s %v", got, err)
	}
}
