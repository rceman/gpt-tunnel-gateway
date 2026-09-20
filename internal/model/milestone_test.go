package model

import (
	"testing"
	"time"
)

func TestMilestoneLifecycleAndMembershipValidation(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	milestone, err := NewMilestone("example", "EXM-MIL1", "Release", "First release", []string{"EXM-TSK1", "EXM-TSK2"}, "planner", now)
	if err != nil {
		t.Fatal(err)
	}
	if milestone.Status != MilestonePlanned || milestone.Tasks[0] != "EXM-TSK1" || milestone.Tasks[1] != "EXM-TSK2" {
		t.Fatalf("milestone=%#v", milestone)
	}
	for _, status := range MilestoneStatuses() {
		milestone.Status = status
		if status == MilestoneCompleted || status == MilestoneArchived {
			milestone.CompletionEvidence = "verified"
		} else {
			milestone.CompletionEvidence = ""
		}
		if err := ValidateMilestone(milestone); err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
	}
	milestone.Status = MilestoneActive
	milestone.Tasks = []string{"EXM-TRN1"}
	if err := ValidateMilestone(milestone); err == nil {
		t.Fatal("Train membership was accepted")
	}
	milestone.Tasks = []string{"EXM-TSK1", "EXM-TSK1"}
	if err := ValidateMilestone(milestone); err == nil {
		t.Fatal("duplicate membership was accepted")
	}
	if _, _, err := ParseMilestoneID("EXM-TSK1"); err == nil {
		t.Fatal("Task identifier parsed as Milestone")
	}
}

func TestMilestoneEvidenceRequiresCanonicalBoundedReferences(t *testing.T) {
	if err := ValidateMilestoneEvidence("example", "done", []string{"EXM-TSK1", "EXM-ADR2"}); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []struct{ name, value string }{{"empty", " "}, {"invalid reference", "not-an-entity"}} {
		t.Run(evidence.name, func(t *testing.T) {
			if evidence.name == "empty" {
				if err := ValidateMilestoneEvidence("example", evidence.value, nil); err == nil {
					t.Fatal("empty evidence accepted")
				}
				return
			}
			if err := ValidateMilestoneEvidence("example", "done", []string{evidence.value}); err == nil {
				t.Fatal("invalid evidence reference accepted")
			}
		})
	}
}
