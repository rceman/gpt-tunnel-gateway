package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTSK531TaskSummaryAndHistoricalHashContract(t *testing.T) {
	task := validTaskAuthoringForTest()
	task.Summary = "A bounded summary."
	var err error
	task.RevisionSHA256, err = HashTaskAuthoring(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskAuthoring(task); err != nil {
		t.Fatal(err)
	}
	for _, summary := range []string{"", strings.Repeat("界", 256), strings.Repeat("x", 257)} {
		candidate := task
		candidate.Summary = summary
		candidate.RevisionSHA256, _ = HashTaskAuthoring(candidate)
		if summary == "" {
			if err := ValidateTaskAuthoring(candidate); err == nil {
				t.Fatal("current Task without summary was accepted")
			}
			continue
		}
		err := ValidateTaskAuthoring(candidate)
		if len([]rune(summary)) == 256 && err != nil {
			t.Fatalf("256-rune summary rejected: %v", err)
		}
		if len([]rune(summary)) == 257 && err == nil {
			t.Fatal("overlong summary was accepted")
		}
	}
	if err := ValidateTaskAuthoringRevision(validTaskAuthoringForTest(), true); err != nil {
		t.Fatalf("pre-summary historical payload rejected: %v", err)
	}
	for _, title := range []string{strings.Repeat("界", 129), strings.Repeat("界", 128)} {
		candidate := validTaskAuthoringForTest()
		candidate.Summary = "bounded"
		candidate.Title = title
		candidate.RevisionSHA256, _ = HashTaskAuthoring(candidate)
		err := ValidateTaskAuthoring(candidate)
		if len([]rune(title)) == 128 && err != nil {
			t.Fatalf("128-rune title rejected: %v", err)
		}
		if len([]rune(title)) == 129 && err == nil {
			t.Fatal("129-rune title accepted")
		}
	}
	encoded, err := json.Marshal(validTaskAuthoringForTest())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"summary"`) {
		t.Fatalf("empty historical summary changed persisted shape: %s", encoded)
	}
}

func TestTSK531ADRRelationReferenceRules(t *testing.T) {
	for _, relation := range []string{TaskADRRequiresNew, TaskADRImplementsExisting, TaskADRSupersedesExisting} {
		task := validTaskAuthoringForTest()
		task.Summary = "bounded"
		task.ADRRelation = relation
		task.ADRReferences = nil
		task.RevisionSHA256, _ = HashTaskAuthoring(task)
		err := ValidateTaskAuthoring(task)
		if relation == TaskADRRequiresNew && err != nil {
			t.Fatalf("requires_new_adr without refs rejected: %v", err)
		}
		if relation != TaskADRRequiresNew && err == nil {
			t.Fatalf("%s without refs accepted", relation)
		}
	}
	task := validTaskAuthoringForTest()
	task.Summary = "bounded"
	task.ADRRelation = TaskADRRequiresNew
	task.ADRReferences = []string{"not-an-adr"}
	task.RevisionSHA256, _ = HashTaskAuthoring(task)
	if err := ValidateTaskAuthoring(task); err == nil {
		t.Fatal("invalid requires_new_adr reference accepted")
	}
}
