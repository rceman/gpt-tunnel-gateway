package model

import (
	"strings"
	"testing"
	"time"
)

func validPlan() Plan {
	return Plan{
		SchemaVersion:    PlanSchemaVersion,
		ProjectID:        "project",
		Revision:         1,
		Title:            "Project plan",
		Summary:          "A compact plan",
		CurrentObjective: "Deliver the next bounded change",
		Queue:            []string{"section-one"},
		Sections:         []PlanSectionIndex{{ID: "section-one", Title: "Section one", ShortDescription: "First section", Revision: 1}},
		UpdatedBy:        "gpt",
		UpdatedAt:        time.Now().UTC(),
	}
}

func TestPlanSchemaV2ValidationRejectsLegacyAndDuplicateSections(t *testing.T) {
	legacy := validPlan()
	legacy.SchemaVersion = SchemaVersion
	if err := ValidatePlan(legacy); err == nil {
		t.Fatal("schema-v1 plan accepted as canonical")
	}
	duplicate := validPlan()
	duplicate.Sections = append(duplicate.Sections, duplicate.Sections[0])
	if err := ValidatePlan(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate section accepted: %v", err)
	}
	section := PlanSection{SchemaVersion: PlanSchemaVersion, ProjectID: "project", ID: "section-one", Revision: 1, Title: "Section one", ShortDescription: "two\nlines", Description: "description", UpdatedBy: "gpt", UpdatedAt: time.Now().UTC()}
	if err := ValidatePlanSection(section); err == nil {
		t.Fatal("multiline section short description accepted")
	}
}

func TestPlanSectionValidationBoundsDescription(t *testing.T) {
	section := PlanSection{SchemaVersion: PlanSchemaVersion, ProjectID: "project", ID: "section", Revision: 1, Title: "Section", ShortDescription: "Short", Description: strings.Repeat("x", 200001), UpdatedBy: "gpt", UpdatedAt: time.Now().UTC()}
	if err := ValidatePlanSection(section); err == nil {
		t.Fatal("oversized section description accepted")
	}
}
