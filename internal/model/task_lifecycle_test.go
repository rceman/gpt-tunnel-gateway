package model

import (
	"strings"
	"testing"
	"time"
)

func lifecycleTask() Task {
	return Task{SchemaVersion: SchemaVersion, ID: "task", SHA256: strings.Repeat("a", 64), Title: "Lifecycle task", Objective: "Exercise lifecycle state validation.", Branch: "feature/lifecycle", BaseRevision: strings.Repeat("b", 40), Status: "created", CreatedBy: "test", CreatedAt: time.Now().UTC()}
}

func TestTaskLifecycleStatesValidateTheirConditionalFields(t *testing.T) {
	task := lifecycleTask()
	now := time.Now().UTC()
	reviewed := strings.Repeat("c", 40)
	integration := strings.Repeat("d", 40)
	tests := []TaskState{
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", UpdatedAt: now},
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", ReviewedHead: reviewed, UpdatedAt: now},
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "deferred", ReviewedHead: reviewed, DeferredReason: "outside integration scope", UpdatedAt: now},
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merged", ReviewedHead: reviewed, IntegrationBranch: "develop", IntegrationHead: integration, UpdatedAt: now},
	}
	for _, state := range tests {
		if err := ValidateTaskState(state, task); err != nil {
			t.Errorf("%s: %v", state.Status, err)
		}
	}
}

func TestTaskLifecycleStateRejectsInvalidConditionalFields(t *testing.T) {
	task := lifecycleTask()
	now := time.Now().UTC()
	validHead := strings.Repeat("c", 40)
	tests := []TaskState{
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merge_ready", UpdatedAt: now},
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "deferred", ReviewedHead: validHead, DeferredReason: "\x00", UpdatedAt: now},
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "merged", ReviewedHead: validHead, IntegrationBranch: "main", IntegrationHead: validHead, UpdatedAt: now},
		{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "completed", ReviewedHead: validHead, UpdatedAt: now},
	}
	for _, state := range tests {
		if err := ValidateTaskState(state, task); err == nil {
			t.Errorf("expected invalid %s state", state.Status)
		}
	}
	tooLong := TaskState{SchemaVersion: SchemaVersion, TaskID: task.ID, TaskSHA256: task.SHA256, Status: "deferred", ReviewedHead: validHead, DeferredReason: strings.Repeat("x", MaxDeferredReasonBytes+1), UpdatedAt: now}
	if err := ValidateTaskState(tooLong, task); err == nil {
		t.Fatal("accepted oversized deferred reason")
	}
}

func TestValidateCommitSHARemainsStrict(t *testing.T) {
	if err := ValidateCommitSHA(strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", strings.Repeat("A", 40), strings.Repeat("a", 39), strings.Repeat("a", 41)} {
		if err := ValidateCommitSHA(value); err == nil {
			t.Errorf("accepted invalid commit SHA %q", value)
		}
	}
}
