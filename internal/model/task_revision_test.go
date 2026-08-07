package model

import (
	"strings"
	"testing"
	"time"
)

func TestTaskRevisionIdentityAndRunIdentityBoundaries(t *testing.T) {
	taskID := "EXM-TSK7"
	revisionID, err := FormatTaskRevisionID(taskID, 3)
	if err != nil || revisionID != "EXM-TSK7.REV3" {
		t.Fatalf("revision id=%q err=%v", revisionID, err)
	}
	if gotTask, gotRevision, err := ParseTaskRevisionID(revisionID); err != nil || gotTask != taskID || gotRevision != 3 {
		t.Fatalf("parsed revision=(%q,%d) err=%v", gotTask, gotRevision, err)
	}
	runID, err := FormatTaskRevisionRunID(revisionID, MaxSafeInteger)
	if err != nil || !strings.HasSuffix(runID, "-RUN9007199254740991") {
		t.Fatalf("run id=%q err=%v", runID, err)
	}
	if _, err := FormatTaskRevisionRunID(revisionID, MaxSafeInteger+1); err == nil {
		t.Fatal("run number above safe integer was accepted")
	}
	if _, _, err := ParseTaskRevisionRunID(revisionID + "-RUN9007199254740992"); err == nil {
		t.Fatal("parsed run number above safe integer")
	}
}

func TestTaskRevisionHashAndParentValidation(t *testing.T) {
	task := Task{
		SchemaVersion: SchemaVersion, ID: "EXM-TSK8", ProjectID: "example", Title: "Revision task",
		Objective: "Preserve immutable task revisions.", Branch: "task/EXM-TSK8-revision",
		BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"read"},
		Constraints: []string{"bounded"}, Status: "created", CreatedBy: "test",
		CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	}
	task.SHA256, _ = HashTask(task)
	revision := TaskRevisionFromTask(task)
	if err := ValidateTaskRevision(revision); err != nil {
		t.Fatal(err)
	}
	hash, err := HashTaskRevision(revision)
	if err != nil {
		t.Fatal(err)
	}
	if hash == revision.RevisionSHA256 {
		t.Fatal("revision hash unexpectedly matched empty hash")
	}
	revision.RevisionSHA256 = hash
	if err := ValidateTaskRevision(revision); err != nil {
		t.Fatal(err)
	}
	child := revision
	child.TaskRevision = 2
	child.ID = FormatTaskRevisionIDUnchecked(task.ID, 2)
	child.ParentTaskRevision = 1
	child.ParentTaskSHA256 = revision.RevisionSHA256
	child.RevisionSHA256, err = HashTaskRevision(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskRevision(child); err != nil {
		t.Fatal(err)
	}
	child.ParentTaskRevision = 3
	child.RevisionSHA256, err = HashTaskRevision(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskRevision(child); err == nil {
		t.Fatal("invalid parent revision was accepted")
	}
}

func TestRevisionAwareCompletionBindsRunRevisionAndDigest(t *testing.T) {
	task := Task{SchemaVersion: SchemaVersion, ID: "EXM-TSK9", ProjectID: "example", Title: "Revision completion", Objective: "Validate revision-aware completion.", Branch: "task/EXM-TSK9-completion", BaseRevision: strings.Repeat("a", 40), AcceptanceCriteria: []string{"read"}, Constraints: []string{}, Status: "created", CreatedBy: "test", CreatedAt: time.Now().UTC()}
	task.SHA256, _ = HashTask(task)
	revisionID := task.ID + ".REV2"
	runID := revisionID + "-RUN1"
	completion := Completion{SchemaVersion: SchemaVersion, RunID: runID, TaskSHA256: task.SHA256, TaskRevision: 2, TaskRevisionSHA256: strings.Repeat("b", 64), TaskRunNumber: 1, Status: "needs_gpt_revision", Summary: "requires a bounded correction", GateResults: []CompletionGateResult{}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}}
	if err := ValidateCompletion(completion, task); err != nil {
		t.Fatal(err)
	}
	completion.TaskRunNumber = 2
	if err := ValidateCompletion(completion, task); err == nil {
		t.Fatal("completion with an incorrect revision run number was accepted")
	}
}
