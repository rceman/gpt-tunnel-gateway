package model

import "testing"

func TestOperationIdentifiersUseCanonicalProjectScopedFormat(t *testing.T) {
	operationID, err := FormatOperationID("GTW", 12)
	if err != nil || operationID != "GTW-OPR12" {
		t.Fatalf("operation=%q err=%v", operationID, err)
	}
	code, number, err := ParseOperationID(operationID)
	if err != nil || code != "GTW" || number != 12 {
		t.Fatalf("parsed operation=%q/%d err=%v", code, number, err)
	}
	for _, invalid := range []string{"mutation-" + "a", "GTW-OPR0", "EXM-OPR1"} {
		if invalid == "EXM-OPR1" {
			if err := ValidateOperationIDForProject(invalid, "GTW"); err == nil {
				t.Fatalf("accepted wrong-project operation %q", invalid)
			}
			continue
		}
		if err := ValidateOperationID(invalid); err == nil {
			t.Fatalf("accepted invalid operation %q", invalid)
		}
	}
}
