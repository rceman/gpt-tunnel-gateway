package model

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"testing"
)

func TestCanonicalDurableIdentifiers(t *testing.T) {
	taskID, err := FormatTaskID("GTW", 7)
	if err != nil || taskID != "GTW-TSK7" {
		t.Fatalf("task ID = %q, err = %v", taskID, err)
	}
	code, number, err := ParseTaskID(taskID)
	if err != nil || code != "GTW" || number != 7 {
		t.Fatalf("parsed task = %q/%d, err = %v", code, number, err)
	}
	runID, err := FormatRunID(taskID, 11)
	if err != nil || runID != "GTW-TSK7-RUN11" {
		t.Fatalf("run ID = %q, err = %v", runID, err)
	}
	parsedTask, runNumber, err := ParseRunID(runID)
	if err != nil || parsedTask != taskID || runNumber != 11 {
		t.Fatalf("parsed run = %q/%d, err = %v", parsedTask, runNumber, err)
	}
	adrID, err := FormatADRID("GTW", 13)
	if err != nil || adrID != "GTW-ADR13" {
		t.Fatalf("ADR ID = %q, err = %v", adrID, err)
	}
	adrCode, adrNumber, err := ParseADRID(adrID)
	if err != nil || adrCode != "GTW" || adrNumber != 13 {
		t.Fatalf("parsed ADR = %q/%d, err = %v", adrCode, adrNumber, err)
	}
	eventID, err := FormatOperatorEventID("GTW", 17)
	if err != nil || eventID != "GTW-OPR17" {
		t.Fatalf("operator event ID = %q, err = %v", eventID, err)
	}
	if code, number, err := ParseOperatorEventID(eventID); err != nil || code != "GTW" || number != 17 {
		t.Fatalf("parsed operator event = %q/%d, err = %v", code, number, err)
	}
	for _, legacy := range []string{"GTW-T1", "GTW-T1-R1", "GTW-A1", "GTW-O1"} {
		if ValidateDurableIdentifier(legacy) == nil {
			t.Fatalf("legacy identifier accepted as canonical: %q", legacy)
		}
	}
}

func TestCanonicalTaskSlug(t *testing.T) {
	for _, value := range []string{"bootstrap", "context-compaction-v2", "a1-b2"} {
		if err := ValidateTaskSlug(value); err != nil {
			t.Fatalf("valid slug %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "-leading", "trailing-", "UPPER", "a_b", "a--b", "a/b"} {
		if err := ValidateTaskSlug(value); err == nil {
			t.Fatalf("invalid slug accepted: %q", value)
		}
	}
}

func TestTaskRunCounterStrictIntegerDecoding(t *testing.T) {
	var counter TaskRunCounter
	if err := json.Unmarshal([]byte(`{"schema_version":1.0,"project_id":"example","task_id":"EXM-TSK1","next_run_number":2.0}`), &counter); err != nil {
		t.Fatalf("integral notation rejected: %v", err)
	}
	if counter.NextRunNumber != 2 {
		t.Fatalf("counter = %#v", counter)
	}
	for _, value := range []string{
		`{"schema_version":1,"project_id":"example","task_id":"EXM-TSK1","next_run_number":true}`,
		`{"schema_version":1,"project_id":"example","task_id":"EXM-TSK1","next_run_number":1.5}`,
		`{"schema_version":1,"project_id":"example","task_id":"EXM-TSK1","next_run_number":9007199254740992}`,
		`{"schema_version":1,"project_id":"example","task_id":"EXM-TSK1","next_run_number":2,"extra":1}`,
	} {
		if err := json.Unmarshal([]byte(value), &counter); err == nil {
			t.Fatalf("invalid counter accepted: %s", value)
		}
	}
}

func TestCompletionSchemaRunIDUsesSafeIntegerBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../schemas/gpt-tunnel-completion.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	pattern, err := regexp.Compile(schema.Properties["run_id"].Pattern)
	if err != nil {
		t.Fatal(err)
	}
	valid := "GTW-TSK" + strconv.FormatUint(MaxSafeInteger, 10) + "-RUN" + strconv.FormatUint(MaxSafeInteger, 10)
	if !pattern.MatchString(valid) {
		t.Fatalf("schema rejected safe-integer maximum: %s", valid)
	}
	for _, invalid := range []string{
		"GTW-TSK" + strconv.FormatUint(MaxSafeInteger+1, 10) + "-RUN1",
		"GTW-TSK1-RUN" + strconv.FormatUint(MaxSafeInteger+1, 10),
	} {
		if pattern.MatchString(invalid) {
			t.Fatalf("schema accepted out-of-range run ID: %s", invalid)
		}
	}
}
