package model

import (
	"testing"
	"time"
)

func TestProjectFormatSelectionRetainsMandatoryCheck(t *testing.T) {
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
		Gates:     []string{WorkflowGateFormat},
	}
	effective, err := WorkflowPolicyForOperation(policy, "implementation")
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Gates) != 2 || effective.Gates[0] != WorkflowGateFormat || effective.Gates[1] != WorkflowGateCheck {
		t.Fatalf("effective gates=%v", effective.Gates)
	}
}
