package model

import "testing"

func TestTaskTypeDefaultsAndValidatesClosedEnum(t *testing.T) {
	if got := DefaultTaskType(""); got != TaskTypeTask {
		t.Fatalf("default task type = %q, want %q", got, TaskTypeTask)
	}
	for _, value := range []TaskType{TaskTypeTask, TaskTypeBug, TaskTypePerf, TaskTypeChore} {
		if got, err := NormalizeTaskType(value); err != nil || got != value {
			t.Fatalf("NormalizeTaskType(%q) = %q, %v", value, got, err)
		}
	}
	if _, err := NormalizeTaskType("incident"); err == nil {
		t.Fatal("invalid task type was accepted")
	}
}

func TestTaskAuthoringDefaultTypePreservesLegacyRevisionHash(t *testing.T) {
	legacy := validTaskAuthoringForTest()
	withDefault := legacy
	withDefault.Type = TaskTypeTask
	legacyHash, err := HashTaskAuthoring(legacy)
	if err != nil {
		t.Fatal(err)
	}
	defaultHash, err := HashTaskAuthoring(withDefault)
	if err != nil {
		t.Fatal(err)
	}
	if legacyHash != defaultHash {
		t.Fatalf("default type changed legacy hash: %s != %s", legacyHash, defaultHash)
	}
}
