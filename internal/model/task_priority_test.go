package model

import (
	"strings"
	"testing"
	"time"
)

func TestTaskPriorityEnumAndCanonicalRank(t *testing.T) {
	priorities := TaskPriorities()
	if len(priorities) != 5 || strings.Join(priorities, ",") != "P0,P1,P2,P3,P4" {
		t.Fatalf("priorities=%v", priorities)
	}
	for want, priority := range priorities {
		got, err := TaskPriorityRank(priority)
		if err != nil || got != want {
			t.Fatalf("rank(%q)=%d/%v, want %d", priority, got, err, want)
		}
	}
	for _, invalid := range []string{"P5", "p0", "high", "", " P1"} {
		if invalid == "" {
			if err := ValidateTaskPriority(invalid, true); err == nil {
				t.Fatalf("required empty priority accepted")
			}
			continue
		}
		if _, err := NormalizeTaskPriority(invalid); err == nil {
			t.Fatalf("invalid priority accepted: %q", invalid)
		}
	}
}

func TestTaskPriorityRequiredAtExecutableBoundaryAndHistoricalValuesRemainReadable(t *testing.T) {
	task := validTaskAuthoringForTest()
	task.Priority = "high"
	task.RevisionSHA256, _ = HashTaskAuthoring(task)
	if err := ValidateTaskAuthoring(task); err == nil {
		t.Fatal("new planned task accepted non-enum priority")
	}

	task = validTaskAuthoringForTest()
	task.Priority = ""
	task.RevisionSHA256, _ = HashTaskAuthoring(task)
	if err := ValidateTaskAuthoring(task); err != nil {
		t.Fatalf("planned task without priority rejected: %v", err)
	}
	if _, err := ReadyTask(task, "planner", time.Now().UTC()); err == nil {
		t.Fatal("planned task without priority became executable")
	}

	task = validTaskAuthoringForTest()
	task.Priority = "high"
	task.RevisionSHA256, _ = HashTaskAuthoring(task)
	if err := ValidateTaskAuthoringRevision(task, true); err != nil {
		t.Fatalf("historical free-form priority rejected: %v", err)
	}
}
