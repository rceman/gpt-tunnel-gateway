package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestDeliveryHandoffCreateReferenceValidationIsZeroMutation(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-create-refs")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := withHandoffPlan(s, DeliveryHandoffCreateInput{
		ProjectID:         "example",
		TaskID:            "EXM-TSK1",
		RunID:             "EXM-TSK1-RUN1",
		OwnerSummary:      handoffSummary(),
		TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy:         "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}, revision)
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

func TestDeliveryHandoffCreateRejectsSupersedesAndDuplicateActive(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-create-lifecycle")
	ctx := context.Background()
	revision, err := s.Hub.RemoteRevision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := withHandoffPlan(s, DeliveryHandoffCreateInput{
		ProjectID:         "example",
		TaskID:            "EXM-TSK1",
		RunID:             "EXM-TSK1-RUN1",
		SupersedesID:      "handoff-old",
		OwnerSummary:      handoffSummary(),
		TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy:         "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: revision,
		},
	}, revision)
	if _, _, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), input); err == nil {
		t.Fatal("create accepted supersedes_handoff_id")
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != revision {
		t.Fatalf("supersedes rejection mutated hub: %s %v", got, err)
	}
	input.SupersedesID = ""
	handoff, created, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := withHandoffPlan(s, DeliveryHandoffCreateInput{
		ProjectID:         "example",
		TaskID:            handoff.TaskID,
		RunID:             handoff.RunID,
		OwnerSummary:      handoffSummary(),
		TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy:         "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	}, created.Hub.After)
	if _, _, err := s.DeliveryHandoffCreate(authority.WithPlanner(ctx), duplicate); err == nil {
		t.Fatal("duplicate active handoff was accepted")
	}
	if got, err := s.Hub.RemoteRevision(ctx); err != nil || got != created.Hub.After {
		t.Fatalf("duplicate rejection mutated hub: %s %v", got, err)
	}
}

func TestDeliveryHandoffSupersedeAndCancelAreAtomic(t *testing.T) {
	s, _, _, _ := dispatchedRun(t, "task/durable-handoff-supersede")
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
	replacement, superseded, err := s.DeliveryHandoffSupersede(authority.WithPlanner(ctx), withSupersedePlan(s, DeliveryHandoffSupersedeInput{
		HandoffID:         handoff.ID,
		OwnerSummary:      handoffSummary(),
		TechnicalEvidence: handoffEvidence(false, false),
		CreatedBy:         "planner",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: created.Hub.After,
		},
	}, created.Hub.After))
	if err != nil {
		t.Fatal(err)
	}
	if replacement.SupersedesHandoffID != handoff.ID || len(superseded.Hub.Paths) != 4 {
		t.Fatalf("supersession was not atomic: %#v %#v", replacement, superseded)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, superseded), 2, "planner", replacement, handoff.ID)
	assertJournalCounter(t, s, 3)
	cancelled, cancelledOp, err := s.DeliveryHandoffCancel(authority.WithPlanner(ctx), DeliveryHandoffCancelInput{
		HandoffID:   replacement.ID,
		CancelledBy: "planner",
		Reason:      "owner withdrew the handoff",
		WriteOptions: WriteOptions{
			ExpectedHubRevision: superseded.Hub.After,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != model.DeliveryHandoffCancelled || len(cancelledOp.Hub.Paths) != 3 {
		t.Fatalf("cancellation was not persisted: %#v %#v", cancelled, cancelledOp)
	}
	assertHandoffJournalEvent(t, journalEventForOperation(t, s, cancelledOp), 3, "planner", replacement)
	assertJournalCounter(t, s, 4)
}
