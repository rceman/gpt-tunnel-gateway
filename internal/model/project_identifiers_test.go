package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectIdentifiersValidationAndCompactIDs(t *testing.T) {
	identifiers := ProjectIdentifiers{
		SchemaVersion:  SchemaVersion,
		ProjectID:      "gpt-tunnel-gateway",
		ProjectCode:    "GTW",
		NextTaskNumber: 1,
		NextADRNumber:  MaxSafeInteger,
	}
	if err := ValidateProjectIdentifiers(identifiers); err != nil {
		t.Fatal(err)
	}
	taskID, err := FormatTaskID("GTW", MaxSafeInteger)
	if err != nil || taskID != "GTW-TSK9007199254740991" {
		t.Fatalf("format task ID: %q %v", taskID, err)
	}
	projectCode, taskNumber, err := ParseTaskID(taskID)
	if err != nil || projectCode != "GTW" || taskNumber != MaxSafeInteger {
		t.Fatalf("parse task ID: %q %d %v", projectCode, taskNumber, err)
	}
	runID := "GTW-TSK1-RUN9007199254740991"
	parsedTaskID, runNumber, err := ParseRunID(runID)
	if err != nil || parsedTaskID != "GTW-TSK1" || runNumber != MaxSafeInteger {
		t.Fatalf("parse run ID: %q %d %v", parsedTaskID, runNumber, err)
	}
	adrID, err := FormatADRID("GTW", 1)
	if err != nil || adrID != "GTW-ADR1" {
		t.Fatalf("format ADR ID: %q %v", adrID, err)
	}
	adrCode, adrNumber, err := ParseADRID(adrID)
	if err != nil || adrCode != "GTW" || adrNumber != 1 {
		t.Fatalf("parse ADR ID: %q %d %v", adrCode, adrNumber, err)
	}
}

func TestProjectIdentifiersRejectInvalidValues(t *testing.T) {
	cases := []ProjectIdentifiers{
		{SchemaVersion: SchemaVersion, ProjectID: "Example", ProjectCode: "EXM", NextTaskNumber: 1, NextADRNumber: 1},
		{SchemaVersion: SchemaVersion, ProjectID: "example", ProjectCode: "exm", NextTaskNumber: 1, NextADRNumber: 1},
		{SchemaVersion: SchemaVersion, ProjectID: "example", ProjectCode: "EXM", NextTaskNumber: 0, NextADRNumber: 1},
		{SchemaVersion: SchemaVersion, ProjectID: "example", ProjectCode: "EXM", NextTaskNumber: 1, NextADRNumber: MaxSafeInteger + 1},
	}
	for _, value := range cases {
		if err := ValidateProjectIdentifiers(value); err == nil {
			t.Fatalf("accepted invalid identifiers: %#v", value)
		}
	}
	for _, value := range []string{"GTW-T0", "GTW-T01", "gtw-T1", "GTW-T9007199254740992", "GTW-T1-R01", "GTW-T1-R0", "GTW-T1-R9007199254740992", "GTW-A01", "GTW-A0", "GTW-A9007199254740992"} {
		if strings.Contains(value, "-R") {
			if _, _, err := ParseRunID(value); err == nil {
				t.Fatalf("accepted invalid run ID %q", value)
			}
		} else if strings.Contains(value, "-A") {
			if _, _, err := ParseADRID(value); err == nil {
				t.Fatalf("accepted invalid ADR ID %q", value)
			}
		} else if _, _, err := ParseTaskID(value); err == nil {
			t.Fatalf("accepted invalid task ID %q", value)
		}
	}
}

func TestProjectIdentifiersJSONIntegerSchemaParity(t *testing.T) {
	valid := "{\"schema_version\":1.0,\"project_id\":\"example\",\"project_code\":\"EXM\",\"next_task_number\":1.0,\"next_adr_number\":9007199254740991.0}"
	var identifiers ProjectIdentifiers
	if err := json.Unmarshal([]byte(valid), &identifiers); err != nil {
		t.Fatalf("integral JSON notation was rejected: %v", err)
	}
	if err := ValidateProjectIdentifiers(identifiers); err != nil || identifiers.SchemaVersion != 1 || identifiers.ProjectID != "example" || identifiers.ProjectCode != "EXM" || identifiers.NextTaskNumber != 1 || identifiers.NextADRNumber != MaxSafeInteger {
		t.Fatalf("integral JSON notation failed model validation: %#v %v", identifiers, err)
	}
	invalid := map[string]string{
		"bool":       strings.Replace(valid, "\"next_task_number\":1.0", "\"next_task_number\":true", 1),
		"fraction":   strings.Replace(valid, "\"next_task_number\":1.0", "\"next_task_number\":1.5", 1),
		"overflow":   strings.Replace(valid, "\"next_adr_number\":9007199254740991.0", "\"next_adr_number\":9007199254740992", 1),
		"non-finite": strings.Replace(valid, "\"next_task_number\":1.0", "\"next_task_number\":NaN", 1),
		"unknown":    strings.TrimSuffix(valid, "}") + ",\"extra\":1}",
		"duplicate":  strings.Replace(valid, "\"project_code\":\"EXM\"", "\"project_code\":\"EXM\",\"project_code\":\"ALT\"", 1),
		"missing":    strings.Replace(valid, "\"project_code\":\"EXM\",", "", 1),
		"non-object": "[]",
		"trailing":   valid + "{}",
	}
	for name, data := range invalid {
		var decoded ProjectIdentifiers
		if err := json.Unmarshal([]byte(data), &decoded); err == nil {
			t.Fatalf("accepted invalid project identifiers JSON case %q", name)
		}
	}
	if err := json.Unmarshal([]byte(valid+" "), &identifiers); err != nil {
		t.Fatalf("accepted JSON whitespace should remain valid: %v", err)
	}
}

func TestCompactIDsRequireExpectedProjectCode(t *testing.T) {
	if number, err := ParseTaskIDForProject("GTW-TSK9007199254740991", "GTW"); err != nil || number != MaxSafeInteger {
		t.Fatalf("matching task project code failed: %d %v", number, err)
	}
	if taskID, number, err := ParseRunIDForProject("GTW-TSK1-RUN9007199254740991", "GTW"); err != nil || taskID != "GTW-TSK1" || number != MaxSafeInteger {
		t.Fatalf("matching run project code failed: %q %d %v", taskID, number, err)
	}
	if number, err := ParseADRIDForProject("GTW-ADR9007199254740991", "GTW"); err != nil || number != MaxSafeInteger {
		t.Fatalf("matching ADR project code failed: %d %v", number, err)
	}
	for _, mismatch := range []struct {
		name string
		call func() error
	}{
		{name: "task", call: func() error { return ValidateTaskIDForProject("GRP-TSK1", "GTW") }},
		{name: "run", call: func() error { return ValidateRunIDForProject("GRP-TSK1-RUN1", "GTW") }},
		{name: "adr", call: func() error { return ValidateADRIDForProject("GRP-ADR1", "GTW") }},
	} {
		if err := mismatch.call(); err == nil {
			t.Fatalf("accepted mismatched %s project code", mismatch.name)
		}
	}
	for _, malformedCode := range []string{"gtw", "GT", "GTWW", "", "G1W"} {
		if err := ValidateTaskIDForProject("GTW-TSK1", malformedCode); err == nil {
			t.Fatalf("accepted malformed expected project code %q", malformedCode)
		}
	}
	if err := ValidateRunIDForProject("GRP-TSK1-RUN1", "GTW"); err == nil {
		t.Fatal("accepted run whose embedded task code mismatches expected project code")
	}
	if err := ValidateTaskIDForProject("GTW-TSK9007199254740992", "GTW"); err == nil {
		t.Fatal("accepted task number above maximum")
	}
}
