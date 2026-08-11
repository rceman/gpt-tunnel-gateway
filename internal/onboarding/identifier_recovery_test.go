package onboarding

import (
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestProjectIdentifiersMatchRequiresExactIdentityAndMonotonicCounters(t *testing.T) {
	expected := model.ProjectIdentifiers{SchemaVersion: 1, ProjectID: "repodex", ProjectCode: "RDX", NextTaskNumber: 3, NextADRNumber: 2}
	if !projectIdentifiersMatch(model.ProjectIdentifiers{SchemaVersion: 1, ProjectID: "repodex", ProjectCode: "RDX", NextTaskNumber: 4, NextADRNumber: 2}, expected) {
		t.Fatal("monotonic identifier advancement was rejected")
	}
	for name, actual := range map[string]model.ProjectIdentifiers{
		"schema mismatch":  {SchemaVersion: 2, ProjectID: "repodex", ProjectCode: "RDX", NextTaskNumber: 3, NextADRNumber: 2},
		"project mismatch": {SchemaVersion: 1, ProjectID: "other", ProjectCode: "RDX", NextTaskNumber: 3, NextADRNumber: 2},
		"code mismatch":    {SchemaVersion: 1, ProjectID: "repodex", ProjectCode: "XYZ", NextTaskNumber: 3, NextADRNumber: 2},
		"task regression":  {SchemaVersion: 1, ProjectID: "repodex", ProjectCode: "RDX", NextTaskNumber: 2, NextADRNumber: 2},
		"adr regression":   {SchemaVersion: 1, ProjectID: "repodex", ProjectCode: "RDX", NextTaskNumber: 3, NextADRNumber: 1},
	} {
		if projectIdentifiersMatch(actual, expected) {
			t.Errorf("%s was accepted", name)
		}
	}
}
