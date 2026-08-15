package model

import (
	"strings"
	"testing"
)

func validTrainV2AttemptCompletion(gates []CompletionGateResult) (TrainV2AttemptCompletion, TaskAuthoring) {
	task := TaskAuthoring{
		SchemaVersion:      1,
		ID:                 "GTW-TSK274",
		RevisionSHA256:     strings.Repeat("a", 64),
		AcceptanceCriteria: []string{"proof"},
	}
	completion := TrainV2AttemptCompletion{
		SchemaVersion:      1,
		TrainID:            "GTW-TRN11",
		TaskID:             task.ID,
		ItemPosition:       17,
		AttemptNumber:      1,
		TaskSHA256:         task.RevisionSHA256,
		Status:             "succeeded",
		Summary:            "proof attached",
		GateResults:        gates,
		AcceptanceCoverage: []string{"AC1"},
	}
	return completion, task
}

func TestValidateTrainV2AttemptCompletionMatchesGateIDsRegardlessOfOrder(t *testing.T) {
	completion, task := validTrainV2AttemptCompletion([]CompletionGateResult{
		{ID: WorkflowGateTest},
		{ID: WorkflowGateFormat},
		{ID: WorkflowGateCheck},
	})
	if err := ValidateTrainV2AttemptCompletion(completion, task, completion.TrainID, completion.ItemPosition, completion.AttemptNumber); err != nil {
		t.Fatalf("reordered gate results rejected: %v", err)
	}
}

func TestValidateTrainV2AttemptCompletionRejectsInvalidGateSets(t *testing.T) {
	tests := []struct {
		name  string
		gates []CompletionGateResult
		want  string
	}{
		{
			name:  "missing",
			gates: []CompletionGateResult{{ID: WorkflowGateFormat}, {ID: WorkflowGateCheck}},
			want:  "do not match required gates",
		},
		{
			name:  "duplicate",
			gates: []CompletionGateResult{{ID: WorkflowGateFormat}, {ID: WorkflowGateFormat}, {ID: WorkflowGateTest}},
			want:  "duplicate Attempt gate result",
		},
		{
			name:  "unexpected",
			gates: []CompletionGateResult{{ID: WorkflowGateFormat}, {ID: WorkflowGateCheck}, {ID: "release"}},
			want:  "unexpected Attempt gate result",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			completion, task := validTrainV2AttemptCompletion(test.gates)
			err := ValidateTrainV2AttemptCompletion(completion, task, completion.TrainID, completion.ItemPosition, completion.AttemptNumber)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}
