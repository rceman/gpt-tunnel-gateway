package onboarding

import (
	"strings"
	"testing"
)

func TestDecodeRequestRejectsClosedShapeViolations(t *testing.T) {
	fixture := string(fixtureBytes(t))
	tests := map[string][]byte{
		"top-level unknown": []byte(strings.TrimSpace(fixture[:len(fixture)-1]) + `, "unknown": 1}`),
		"nested unknown": mustReplace(t, fixture, `"session_key": "airelay_master"
  },`, `"session_key": "airelay_master",
    "unknown": 1
  },`),
		"top-level duplicate": mustReplace(t, fixture, `"project_id": "airelay",`, `"project_id": "airelay", "project_id": "airelay",`),
		"nested duplicate":    mustReplace(t, fixture, `"session_required": true,`, `"session_required": true, "session_required": true,`),
		"top-level null":      []byte("null"),
		"nested null": mustReplace(t, fixture, `"airelay": {
    "session_required": true,
    "session_key": "airelay_master"
  },`, `"airelay": null,`),
		"trailing content":       append([]byte(strings.TrimSpace(fixture)), []byte(" {}")...),
		"invalid root path":      mustReplace(t, fixture, `"root": "/home/therceman/git/airelay"`, `"root": "/home/therceman/../airelay"`),
		"invalid repository URL": mustReplace(t, fixture, `"repository_url": "git@github.com-therceman:therceman/airelay.git"`, `"repository_url": "not a repository"`),
		"invalid branch":         mustReplace(t, fixture, `"default_branch": "master"`, `"default_branch": "bad..branch"`),
		"invalid remote":         mustReplace(t, fixture, `"remote": "origin"`, `"remote": "bad remote"`),
		"invalid project ID":     mustReplace(t, fixture, `"project_id": "airelay",`, `"project_id": "Airelay",`),
		"invalid project code":   mustReplace(t, fixture, `"project_code": "AIR"`, `"project_code": "air"`),
		"missing required session key": mustReplace(t, fixture, `"airelay": {
    "session_required": true,
    "session_key": "airelay_master"
  },`, `"airelay": {"session_required": true},`),
		"forbidden optional session key": mustReplace(t, fixture, `"session_required": true,`, `"session_required": false,`),
		"partial workflow": mustReplace(t, fixture, `"initial_plan": {`, `"workflow": {"repository": "rceman/gpt-review-planner"},
  "initial_plan": {`),
		"plan project mismatch": mustReplace(t, fixture, `"initial_plan": {
    "schema_version": 2,
    "project_id": "airelay"`, `"initial_plan": {
    "schema_version": 2,
    "project_id": "other-project"`),
		"duplicate queue IDs":      mustReplace(t, fixture, `"queue": [],`, `"queue": ["P1", "P1"],`),
		"duplicate section IDs":    mustReplace(t, fixture, `"sections": [],`, `"sections": [{"id":"S1","title":"One","short_description":"one","revision":1},{"id":"S1","title":"Two","short_description":"two","revision":1}],`),
		"invalid timestamp":        mustReplace(t, fixture, `"updated_at": "2026-08-05T09:00:00Z"`, `"updated_at": "2026-08-05 09:00:00"`),
		"active task field":        mustReplace(t, fixture, `"current_objective": "",`, `"current_objective": "", "active_task_id": "task-1",`),
		"invalid expected hub SHA": mustReplace(t, fixture, `"expected_hub_revision": "0000000000000000000000000000000000000000"`, `"expected_hub_revision": "bad"`),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			expectInvalid(t, data)
		})
	}
}

func TestSafeIntegerSemantics(t *testing.T) {
	fixture := string(fixtureBytes(t))
	valid := mustReplace(t, fixture, `"schema_version": 1,`, `"schema_version": 1.0,`)
	if _, err := DecodeRequest(valid); err != nil {
		t.Fatalf("integral 1.0 should be accepted: %v", err)
	}
	for name, value := range map[string]string{
		"boolean":       "true",
		"zero":          "0",
		"negative":      "-1",
		"fraction":      "1.5",
		"overflow":      "9007199254740992",
		"huge exponent": "1e999999",
	} {
		t.Run(name, func(t *testing.T) {
			expectInvalid(t, mustReplace(t, fixture, `"schema_version": 1,`, `"schema_version": `+value+`,`))
		})
	}
}

func TestDecodeRequestRequiresZeroValuedFields(t *testing.T) {
	fixture := string(fixtureBytes(t))
	expectInvalid(t, mustReplace(t, fixture, `"airelay": {
    "session_required": true,
    "session_key": "airelay_master"
  },
`, ""))
	expectInvalid(t, mustReplace(t, fixture, `"session_required": true,
`, ""))
	expectInvalid(t, mustReplace(t, fixture, `"current_objective": "",
`, ""))
}
