package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
