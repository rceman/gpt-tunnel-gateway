package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProjectWorkflowPolicyHasNoGenericGateSelection(t *testing.T) {
	policy := ProjectWorkflowPolicy{
		SchemaVersion:     SchemaVersion,
		ProjectID:         "example",
		Revision:          1,
		WorkflowStage:     WorkflowStageTransitionalMain,
		IntegrationBranch: "main",
		CI: WorkflowPolicyCI{
			Task:      WorkflowCIModeDisabled,
			TaskMerge: WorkflowCIModeObserve,
			Release:   WorkflowCIModeObserve,
		},
		UpdatedBy: "test",
		UpdatedAt: time.Now().UTC(),
	}
	if err := ValidateProjectWorkflowPolicy(policy); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["gates"]; ok {
		t.Fatalf("ProjectWorkflowPolicy exposes generic gates: %s", encoded)
	}
	if _, err := WorkflowPolicyForOperation(policy, "implementation"); err != nil {
		t.Fatal(err)
	}
}
