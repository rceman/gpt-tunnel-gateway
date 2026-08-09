package model

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRunAcceptsOnlyCanonicalStatuses(t *testing.T) {
	valid := []string{"created", "dispatching", "dispatched", "awaiting_result", "cancel_requested", "succeeded", "failed", "needs_gpt_revision"}
	for _, status := range valid {
		run := Run{
			SchemaVersion:  SchemaVersion,
			ID:             "run",
			TaskID:         "task",
			TaskSHA256:     strings.Repeat("a", 64),
			ProjectID:      "project",
			Status:         status,
			CompletionPath: "/tmp/completion.json",
			CreatedAt:      time.Now().UTC(),
		}
		if err := ValidateRun(run); err != nil {
			t.Errorf("status %q rejected: %v", status, err)
		}
	}
}

func TestValidateRunRejectsUnknownStatus(t *testing.T) {
	run := Run{
		SchemaVersion:  SchemaVersion,
		ID:             "run",
		TaskID:         "task",
		TaskSHA256:     strings.Repeat("a", 64),
		ProjectID:      "project",
		Status:         "unknown",
		CompletionPath: "/tmp/completion.json",
		CreatedAt:      time.Now().UTC(),
	}
	if err := ValidateRun(run); err == nil {
		t.Fatal("unknown run status was accepted")
	}
}
