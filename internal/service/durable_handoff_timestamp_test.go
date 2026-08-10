package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestDurableHandoffTransitionsClampBackwardClock(t *testing.T) {
	s, task, run := makeReviewableRun(t)
	delivery := finalizeAcceptedDeliveryReview(t, s, task, run)
	future := time.Now().UTC().Add(time.Hour)
	past := future.Add(-time.Hour)
	calls := 0
	s.clock = func() time.Time {
		calls++
		if calls == 1 {
			return future
		}
		return past
	}
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
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
	evidence := json.RawMessage(`{"terminal":true,"reviewed":true,"task_sha256":"` + task.SHA256 + `","run_id":"` + run.ID + `","delivery_report_id":"` + delivery.ID + `","reviewed_head":"` + delivery.ReviewedHead + `"}`)
	if _, _, err := s.PlannerReportPublish(authority.WithDelivery(ctx), PlannerReportPublishInput{
		HandoffID: next.ID,
		Report: model.PlannerReport{
			ReportType:        model.PlannerReportCompleted,
			OwnerSummary:      handoffSummaryStatus(model.PlannerReportCompleted),
			TechnicalEvidence: evidence,
			PublishedBy:       "delivery",
		},
		WriteOptions: WriteOptions{
			ExpectedHubRevision: nextOp.Hub.After,
		},
	}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.DeliveryHandoffRead(ctx, handoff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.UpdatedAt.Before(stored.CreatedAt) {
		t.Fatalf("handoff updated_at=%s precedes created_at=%s", stored.UpdatedAt, stored.CreatedAt)
	}
	if stored.Status != model.DeliveryHandoffCompleted {
		t.Fatalf("handoff status = %q, want completed", stored.Status)
	}
	if strings.TrimSpace(stored.CurrentReportID) == "" {
		t.Fatal("completed handoff lost current report")
	}
}
