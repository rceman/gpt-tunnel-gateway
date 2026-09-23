package model

import "testing"

func TestTaskStatusSymbolUsesADR107Vocabulary(t *testing.T) {
	for _, test := range []struct {
		status string
		want   string
	}{
		{TaskAuthoringPlanned, "○"},
		{TaskAuthoringReady, "○"},
		{TaskExecutionDispatched, "▶"},
		{TaskExecutionInProgress, "▶"},
		{TaskExecutionAwaitingReview, "◐"},
		{TaskExecutionReadyForVerification, "◇"},
		{TaskExecutionIntegrated, "◇"},
		{TaskExecutionBlocked, "⊘"},
		{TaskAuthoringDone, "●"},
		{TaskExecutionFailed, "×"},
		{"deferred", "◌"},
		{"paused", "◌"},
	} {
		t.Run(test.status, func(t *testing.T) {
			got, err := TaskStatusSymbol(test.status)
			if err != nil || got != test.want {
				t.Fatalf("TaskStatusSymbol(%q)=%q, %v; want %q", test.status, got, err, test.want)
			}
		})
	}
	if _, err := TaskStatusSymbol(TaskAuthoringArchived); err == nil {
		t.Fatal("archived Task status must be handled as an omitted historical member")
	}
}
