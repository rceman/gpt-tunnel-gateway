package actioncontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/workflowrole"
)

func TestCompositionOnlyObjectDelegatesPropertiesToBranches(t *testing.T) {
	closed := false
	action := &CompiledSchema{Type: "string"}
	sessionBranch := &CompiledSchema{
		Type:                 "object",
		Properties:           map[string]*CompiledSchema{"action": action, "session": action},
		Required:             []string{"action", "session"},
		AdditionalProperties: &closed,
	}
	listBranch := &CompiledSchema{
		Type:                 "object",
		Properties:           map[string]*CompiledSchema{"action": action, "sessions": {Type: "array"}},
		Required:             []string{"action", "sessions"},
		AdditionalProperties: &closed,
	}
	composed := &CompiledSchema{
		Type:  "object",
		OneOf: []*CompiledSchema{sessionBranch, listBranch},
	}
	if err := validateCompiledSchema(composed, map[string]any{"action": "info", "session": "HOM_EXM_P_1"}, "output"); err != nil {
		t.Fatalf("valid one_of branch rejected by composed object: %v", err)
	}
	if err := validateCompiledSchema(composed, map[string]any{"action": "info", "session": "HOM_EXM_P_1", "extra": true}, "output"); err == nil {
		t.Fatal("composed one_of accepted a property outside all closed branches")
	}
}

func TestCanonicalContractTreeAndSharedDefinitionCoverage(t *testing.T) {
	shared, actions := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("..", "..", "contracts", "actions.yaml"))
	compiled, err := Compile(shared, actions)
	if err != nil {
		t.Fatal(err)
	}
	expectedDefinitions := requiredSharedDefinitionNames()
	sort.Strings(expectedDefinitions)
	if got := compiled.DefinitionNames(); !reflect.DeepEqual(got, expectedDefinitions) {
		t.Fatalf("shared definition names = %v, want %v", got, expectedDefinitions)
	}
	actionsList := compiled.Actions()
	if len(actionsList) == 0 {
		t.Fatal("canonical action catalog is empty")
	}
	for _, action := range actionsList {
		if action.Path == "system/call" || action.Path == "system/schema" {
			t.Fatalf("retired action %q remains in the canonical catalog", action.Path)
		}
	}
	agentPrompt, ok := compiled.Action("agent/prompt")
	if !ok {
		t.Fatal("agent/prompt contract is missing")
	}
	promptMessage := agentPrompt.Input.Properties["message"]
	if promptMessage == nil || promptMessage.Type != "string" || promptMessage.MaxLength == nil || *promptMessage.MaxLength != 4096 {
		t.Fatalf("Agent prompt message body is not a bounded string: %#v", agentPrompt.Input.Properties)
	}
	messageRead, ok := compiled.Action("message/read")
	if !ok {
		t.Fatal("message/read contract is missing")
	}
	messageIdentity := messageRead.Output.Properties["message"]
	if messageIdentity == nil || messageIdentity.RefName != "EntityKeyAndReference" {
		t.Fatalf("Message identity does not reuse its shared semantic reference: %#v", messageRead.Output.Properties)
	}
	if got := compiled.definitions["WorkflowRole"].Enum; !reflect.DeepEqual(got, stringValues(workflowrole.Names())) {
		t.Fatalf("WorkflowRole values = %v, want current workflow roles %v", got, workflowrole.Names())
	}
	if got := compiled.definitions["Track"].Properties["status"].Enum; !reflect.DeepEqual(got, stringValues(model.TrackStatuses())) {
		t.Fatalf("Track statuses = %v, want %v", got, model.TrackStatuses())
	}
	if got := compiled.definitions["Milestone"].Properties["status"].Enum; !reflect.DeepEqual(got, stringValues(model.MilestoneStatuses())) {
		t.Fatalf("Milestone statuses = %v, want %v", got, model.MilestoneStatuses())
	}
	executionStatuses := []string{model.TaskExecutionPlanned, model.TaskExecutionDispatched, model.TaskExecutionInProgress, model.TaskExecutionAwaitingReview, model.TaskExecutionChangesRequested, model.TaskExecutionReadyForVerification, model.TaskExecutionVerifying, model.TaskExecutionVerified, model.TaskExecutionIntegrating, model.TaskExecutionIntegrated, model.TaskExecutionDone, model.TaskExecutionBlocked, model.TaskExecutionFailed}
	if got := compiled.definitions["TaskExecution"].Properties["status"].Enum; !reflect.DeepEqual(got, stringValues(executionStatuses)) {
		t.Fatalf("TaskExecution statuses = %v, want %v", got, executionStatuses)
	}
	verificationOutcomes := []string{model.TaskExecutionVerificationSucceeded, model.TaskExecutionVerificationFailed, model.TaskExecutionVerificationInterrupted}
	if got := compiled.definitions["TaskVerification"].Properties["outcome"].Enum; !reflect.DeepEqual(got, stringValues(verificationOutcomes)) {
		t.Fatalf("TaskVerification outcomes = %v, want %v", got, verificationOutcomes)
	}
	operationStatuses := []string{"accepted", "running", "completed", "failed", "outcome_unknown"}
	if got := compiled.definitions["Operation"].Properties["status"].Enum; !reflect.DeepEqual(got, stringValues(operationStatuses)) {
		t.Fatalf("Operation statuses = %v, want %v", got, operationStatuses)
	}
	trackReview := compiled.definitions["TrackReviewSnapshot"]
	if trackReview.Properties["head"].RefName != "GitFingerprint" || trackReview.Properties["tree"].RefName != "GitFingerprint" {
		t.Fatalf("TrackReviewSnapshot does not expose compact Git fingerprints: %#v", trackReview.Properties)
	}
	for _, internalField := range []string{"digest", "revision_sha256", "full_head", "full_tree"} {
		if _, exists := trackReview.Properties[internalField]; exists {
			t.Fatalf("TrackReviewSnapshot exposes internal field %q", internalField)
		}
	}
	taskVerification := compiled.definitions["TaskVerification"]
	if taskVerification.Properties["operation"].RefName != "EntityKeyAndReference" || taskVerification.Properties["candidate_head"].RefName != "GitFingerprint" {
		t.Fatalf("TaskVerification lacks semantic refs and compact Git fingerprints: %#v", taskVerification.Properties)
	}
	taskExecution := compiled.definitions["TaskExecution"]
	if taskExecution.Properties["worktree"].Pattern != `^WT-TSK[0-9]+-[a-f0-9]{8}$` {
		t.Fatalf("TaskExecution worktree pattern = %q", taskExecution.Properties["worktree"].Pattern)
	}
	for _, internalField := range []string{"branch", "base_head", "head", "task_revision_sha256"} {
		if _, exists := taskExecution.Properties[internalField]; exists {
			t.Fatalf("TaskExecution exposes internal field %q", internalField)
		}
	}
	timestamp := compiled.definitions["Timestamp"]
	if timestamp.Format != formatTimestamp || timestamp.MinLength == nil || *timestamp.MinLength != 17 || timestamp.MaxLength == nil || *timestamp.MaxLength != 17 {
		t.Fatalf("canonical Timestamp = %#v", timestamp)
	}
	operationKey := compiled.definitions["Operation"].Properties["key"]
	if err := validateCompiledSchema(operationKey, "EXM-OPR1", "operation.key"); err != nil {
		t.Fatalf("valid Operation key rejected: %v", err)
	}
	if err := validateCompiledSchema(operationKey, "EXM-OPR0", "operation.key"); err == nil {
		t.Fatal("invalid Operation key accepted")
	}
	if _, err := Compile(shared, []byte("version: 1\ndomains:\n  - name: train\n    actions: []\n")); err == nil {
		t.Fatal("retired Train domain compiled")
	}
}

func TestCompiledFixtureDrivesSchemasDiscoveryAndConformance(t *testing.T) {
	shared, actions := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	compiled, err := Compile(shared, actions)
	if err != nil {
		t.Fatal(err)
	}
	mutationReasonCatalog := replaceContract("role: {ref: WorkflowRole}", "role: {ref: WorkflowRole}\n            reason: {ref: MutationReason}")(string(actions))
	if _, err := Compile(shared, []byte(mutationReasonCatalog)); err != nil {
		t.Fatalf("MutationReason reference rejected: %v", err)
	}
	optionalSelectorActions := replaceContract("required: [key, role, at]", "required: [role, at]")(string(actions))
	optionalSelectorSet, err := Compile(shared, []byte(optionalSelectorActions))
	if err != nil {
		t.Fatalf("optional typed-path selector failed to compile: %v", err)
	}
	if err := optionalSelectorSet.ValidateInput("sample/inspect", map[string]any{"role": "worker", "at": "26-09-23T13:42:12"}); err != nil {
		t.Fatalf("optional typed-path selector was treated as required: %v", err)
	}

	inputSchema, err := compiled.ActionInputSchema("sample/inspect")
	if err != nil {
		t.Fatal(err)
	}
	inputProperties := inputSchema["properties"].(map[string]any)
	limit := inputProperties["limit"].(map[string]any)
	if limit["default"] != int64(25) {
		t.Fatalf("compiled default override = %#v, want 25", limit["default"])
	}
	at := inputProperties["at"].(map[string]any)
	if at["pattern"] != compiled.definitions["Timestamp"].Pattern || at["format"] != formatTimestamp {
		t.Fatalf("compact Timestamp schema = %#v", at)
	}
	outputSchema, err := compiled.ActionOutputSchema("sample/inspect")
	if err != nil {
		t.Fatal(err)
	}
	outputProperties := outputSchema["properties"].(map[string]any)
	fingerprint := outputProperties["fingerprint"].(map[string]any)
	if fingerprint["pattern"] != "^[0-9a-f]{8}$" {
		t.Fatalf("Git fingerprint schema = %#v", fingerprint)
	}
	handle := outputProperties["handle"].(map[string]any)
	if handle["maxLength"] != 8 || handle["pattern"] != `^[\x20-\x7E]{1,8}$` {
		t.Fatalf("Non-Git handle schema = %#v", handle)
	}
	duration := outputProperties["duration"].(map[string]any)
	if duration["x-unit"] != "seconds" {
		t.Fatalf("duration unit projection = %#v", duration)
	}

	rootDiscovery, err := compiled.CompactDiscovery("")
	if err != nil || !reflect.DeepEqual(rootDiscovery.Domains, []string{"sample"}) || len(rootDiscovery.Actions) != 0 {
		t.Fatalf("root compact discovery = %#v, err=%v", rootDiscovery, err)
	}
	domainDiscovery, err := compiled.CompactDiscovery("sample")
	if err != nil || len(domainDiscovery.Actions) != 1 || domainDiscovery.Actions[0].Path != "sample/inspect" {
		t.Fatalf("domain compact discovery = %#v, err=%v", domainDiscovery, err)
	}
	encoded, err := json.Marshal(inputSchema)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(shared, actions)
	if err != nil {
		t.Fatal(err)
	}
	secondSchema, err := second.ActionInputSchema("sample/inspect")
	if err != nil {
		t.Fatal(err)
	}
	secondEncoded, err := json.Marshal(secondSchema)
	if err != nil || string(encoded) != string(secondEncoded) {
		t.Fatalf("compiled schema is not deterministic: %s != %s, err=%v", encoded, secondEncoded, err)
	}
	settingsDefault := inputProperties["settings"].(map[string]any)["default"].(map[string]any)
	settingsDefault["mode"] = "unsafe"
	freshInputSchema, err := compiled.ActionInputSchema("sample/inspect")
	if err != nil {
		t.Fatal(err)
	}
	if freshInputSchema["properties"].(map[string]any)["settings"].(map[string]any)["default"].(map[string]any)["mode"] != "safe" {
		t.Fatal("schema projection mutation changed compiled defaults")
	}

	validInput := map[string]any{"key": "GTW-TSK1", "role": "worker", "at": "26-09-23T13:42:12", "cursor": "Abcdef23"}
	if err := compiled.ValidateInput("sample/inspect", validInput); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	validOutput := map[string]any{
		"task": "GTW-TSK1", "finished_at": "26-09-23T13:42:12", "fingerprint": "deadbeef", "handle": "a1b2",
		"duration": 3, "revision": 1, "status": "accepted",
	}
	if err := compiled.ValidateOutput("sample/inspect", validOutput); err != nil {
		t.Fatalf("valid output rejected: %v", err)
	}
	allOfActions := replaceContract("status: {type: string, enum: [accepted, failed]}", "status:\n              all_of:\n                - type: string\n                - type: string\n                  enum: [accepted, failed]")(string(actions))
	allOfSet, err := Compile(shared, []byte(allOfActions))
	if err != nil {
		t.Fatalf("all_of contract failed to compile: %v", err)
	}
	allOfSchema, err := allOfSet.ActionOutputSchema("sample/inspect")
	if err != nil {
		t.Fatal(err)
	}
	if len(allOfSchema["properties"].(map[string]any)["status"].(map[string]any)["allOf"].([]any)) != 2 {
		t.Fatalf("all_of JSON Schema = %#v", allOfSchema)
	}
	if err := allOfSet.ValidateOutput("sample/inspect", validOutput); err != nil {
		t.Fatalf("all_of runtime validation rejected valid output: %v", err)
	}
	invalidAllOf := map[string]any{"task": "GTW-TSK1", "finished_at": "26-09-23T13:42:12", "fingerprint": "deadbeef", "duration": 3, "revision": 1, "status": "running"}
	if err := allOfSet.ValidateOutput("sample/inspect", invalidAllOf); err == nil {
		t.Fatal("all_of runtime validation accepted invalid enum")
	}
	invalidOutputs := []map[string]any{
		{"task": "GTW-TSK1", "finished_at": "2026-09-23T13:42:12Z", "fingerprint": "deadbeef", "duration": 3, "revision": 1, "status": "accepted"},
		{"task": "GTW-TSK1", "finished_at": "26-02-30T13:42:12", "fingerprint": "deadbeef", "duration": 3, "revision": 1, "status": "accepted"},
		{"task": "GTW-TSK1", "finished_at": "26-09-23T13:42:12", "fingerprint": "DEADBEEF", "duration": 3, "revision": 1, "status": "accepted"},
		{"task": "GTW-TSK1", "finished_at": "26-09-23T13:42:12", "fingerprint": "deadbeef", "duration": -1, "revision": 1, "status": "accepted"},
	}
	for index, value := range invalidOutputs {
		if err := compiled.ValidateOutput("sample/inspect", value); err == nil {
			t.Errorf("invalid output %d was accepted", index)
		}
	}
	if err := compiled.ValidateInput("sample/inspect", map[string]any{"key": "GTW-TSK1", "role": "worker", "at": "26-09-23T13:42:12", "cursor": "abcdefghX"}); err == nil {
		t.Fatal("long compact cursor was accepted")
	}
	invalidHandle := map[string]any{"task": "GTW-TSK1", "finished_at": "26-09-23T13:42:12", "fingerprint": "deadbeef", "handle": "hash12345", "duration": 3, "revision": 1, "status": "accepted"}
	if err := compiled.ValidateOutput("sample/inspect", invalidHandle); err == nil {
		t.Fatal("oversized non-Git handle was accepted")
	}
	if err := compiled.ValidateInput("sample/inspect", map[string]any{"key": "GTW-TSK1", "role": "operator", "at": "26-09-23T13:42:12"}); err == nil {
		t.Fatal("unknown WorkflowRole was accepted")
	}
}

func TestOperationSelectorsUseKeyOnly(t *testing.T) {
	sharedBytes, actionsBytes := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	var shared sharedFile
	if err := decodeStrictYAML(sharedBytes, &shared); err != nil {
		t.Fatal(err)
	}
	for _, actionName := range []string{"read", "await"} {
		t.Run(actionName, func(t *testing.T) {
			var catalog catalogSpec
			if err := decodeStrictYAML(actionsBytes, &catalog); err != nil {
				t.Fatal(err)
			}
			catalog.Domains[0].Name = "operation"
			catalog.Domains[0].Actions[0].Name = actionName
			if _, err := compileSpecs(shared, catalog); err != nil {
				t.Fatalf("Operation selector contract rejected: %v", err)
			}
			catalog.Domains[0].Actions[0].Input.Properties["operation_id"] = schemaSpec{Type: "string"}
			if _, err := compileSpecs(shared, catalog); err == nil {
				t.Fatal("Operation alias selector compiled")
			}
		})
	}
}

func TestCompilerFailsClosedOnInvalidContracts(t *testing.T) {
	shared, actions := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	tests := []struct {
		name    string
		shared  func(string) string
		actions func(string) string
	}{
		{name: "unknown shared ref", actions: replaceContract("role: {ref: WorkflowRole}", "role: {ref: UnknownRole}")},
		{name: "required property missing", actions: replaceContract("required: [key, role, at]", "required: [key, role, missing]")},
		{name: "empty required", actions: replaceContract("required: [key, role, at]", "required: []")},
		{name: "null required", actions: replaceContract("required: [key, role, at]", "required: null")},
		{name: "null enum", actions: replaceContract("enum: [accepted, failed]", "enum: null")},
		{name: "duplicate required", actions: replaceContract("required: [key, role, at]", "required: [key, role, role]")},
		{name: "required default conflict", actions: replaceContract("required: [key, role, at]", "required: [key, role, at, limit]")},
		{name: "invalid default type", actions: replaceContract("default: 20", "default: wrong")},
		{name: "invalid default bound", actions: replaceContract("value: 25", "value: 101")},
		{name: "invalid default override metadata", actions: replaceContract("reason: Explicit compiler fixture override.", "reason: ''")},
		{name: "invalid bounds", actions: replaceContract("maximum: 100", "maximum: 0")},
		{name: "duplicate enum", actions: replaceContract("enum: [accepted, failed]", "enum: [accepted, accepted]")},
		{name: "enum value violates pattern", actions: replaceContract("status: {type: string, enum: [accepted, failed]}", "status: {type: string, pattern: '^ok$', enum: [accepted, failed]}")},
		{name: "const outside enum", actions: replaceContract("status: {type: string, enum: [accepted, failed]}", "status: {type: string, enum: [accepted, failed], const: running}")},
		{name: "invalid pattern", shared: replaceContract("pattern: '^[0-9a-f]{8}$'", "pattern: '['")},

		{name: "pattern incompatible with type", actions: replaceContract("minimum: 1", "minimum: 1\n              pattern: '^x$'")},
		{name: "ad hoc RFC3339 pattern", actions: replaceContract("status: {type: string, enum: [accepted, failed]}", "status: {type: string, pattern: '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'}")},
		{name: "invalid duration unit", actions: replaceContract("unit: seconds", "unit: hours")},
		{name: "missing selector metadata", actions: replaceContract("          selector: key\n", "")},
		{name: "invalid selector metadata", actions: replaceContract("selector: key", "selector: alias")},
		{name: "key without selector", actions: replaceContract("selector: key", "selector: none")},
		{name: "invalid metadata value", actions: replaceContract("surface: normal", "surface: planner")},
		{name: "invalid cursor metadata", shared: replaceContract("continuation_output: call.pagination.next_cursor", "continuation_output: result.next_cursor")},
		{name: "invalid timestamp metadata", shared: replaceContract("year_mapping: 2000-2099", "year_mapping: 1900-1999")},
		{name: "shared cross-domain scalar", shared: replaceContract("milestone: {ref: EntityKeyAndReference}", "milestone: {type: string}")},
		{name: "unsupported catalog version", actions: replaceContract("version: 1", "version: 2")},
		{name: "empty all_of", actions: replaceContract("status: {type: string, enum: [accepted, failed]}", "status: {all_of: []}")},
		{name: "role gate field", actions: replaceContract("status: {type: string, enum: [accepted, failed]}", "required_role: {ref: WorkflowRole}")},
		{name: "runtime ref field", actions: replaceContract("role: {ref: WorkflowRole}", "runtime_ref: {type: string}")},
		{name: "untyped mutation reason", actions: replaceContract("role: {ref: WorkflowRole}", "role: {ref: WorkflowRole}\n            reason: {type: string}")},
		{name: "cursor is not compact handle", actions: replaceContract("cursor: {ref: CursorAndCompactHandle}", "cursor: {type: string, min_length: 1}")},

		{name: "unknown metadata field", actions: replaceContract("            open_world: false", "            open_world: false\n            role_gate: worker")},
		{name: "malformed YAML", actions: replaceContract("version: 1", "version: [")},
		{name: "unknown YAML field", actions: replaceContract("description: Inspect one typed sample contract.", "description: Inspect one typed sample contract.\n        execution_role: worker")},
		{name: "control in description", actions: replaceContract("description: Inspect one typed sample contract.", `description: "Inspect one typed sample contract.\x1b"`)},
		{name: "timestamp bypass", actions: replaceContract("finished_at: {ref: Timestamp}", "finished_at: {type: string, format: date-time}")},
		{name: "output identity alias", actions: replaceContract("task: {ref: EntityKeyAndReference}", "task_id: {ref: EntityKeyAndReference}")},
		{name: "result continuation field", actions: replaceContract("            status: {type: string, enum: [accepted, failed]}", "            next_cursor: {ref: CursorAndCompactHandle}\n            status: {type: string, enum: [accepted, failed]}")},
		{name: "duration unit missing", actions: replaceContract(", unit: seconds", "")},
		{name: "operation default override without base", actions: replaceContract("role: {ref: WorkflowRole}", "role:\n              ref: WorkflowRole\n              default_override: {value: worker, reason: Fixture}")},
		{name: "empty enum", actions: replaceContract("enum: [accepted, failed]", "enum: []")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sharedInput := shared
			actionsInput := actions
			if test.shared != nil {
				sharedInput = []byte(test.shared(string(sharedInput)))
			}
			if test.actions != nil {
				actionsInput = []byte(test.actions(string(actionsInput)))
			}
			if _, err := Compile(sharedInput, actionsInput); err == nil {
				t.Fatal("invalid contract compiled")
			}
		})
	}
}

func TestCompilerRejectsRetiredActionPaths(t *testing.T) {
	sharedBytes, actionsBytes := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	var shared sharedFile
	if err := decodeStrictYAML(sharedBytes, &shared); err != nil {
		t.Fatal(err)
	}
	retired := []struct{ domain, action string }{
		{"queue", "list"}, {"train", "read"}, {"hotfix", "create"}, {"attempt", "read"}, {"wave", "run"},
		{"project", "deploy"}, {"task", "submit-tests"}, {"system", "call"}, {"system", "schema"},
		{"git", "refs"}, {"git", "log"}, {"git", "tree"}, {"debug", "task_legacy_revision_list"},
	}
	for _, item := range retired {
		t.Run(item.domain+"/"+item.action, func(t *testing.T) {
			var catalog catalogSpec
			if err := decodeStrictYAML(actionsBytes, &catalog); err != nil {
				t.Fatal(err)
			}
			catalog.Domains[0].Name = item.domain
			catalog.Domains[0].Actions[0].Name = item.action
			if _, err := compileSpecs(shared, catalog); err == nil {
				t.Fatal("retired action path compiled")
			}
		})
	}
}

func TestCompilerRejectsDuplicateDefinitionsAndActions(t *testing.T) {
	shared, actions := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	duplicateDefinition := append(append([]byte(nil), shared...), []byte("\n  WorkflowRole:\n    schema: {type: string}\n    metadata: {kind: enum}\n")...)
	if _, err := Compile(duplicateDefinition, actions); err == nil {
		t.Fatal("duplicate shared definition compiled")
	}
	var sharedSpec sharedFile
	var catalog catalogSpec
	if err := decodeStrictYAML(shared, &sharedSpec); err != nil {
		t.Fatal(err)
	}
	if err := decodeStrictYAML(actions, &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Domains[0].Actions = append(catalog.Domains[0].Actions, catalog.Domains[0].Actions[0])
	if _, err := compileSpecs(sharedSpec, catalog); err == nil {
		t.Fatal("duplicate action compiled")
	}
}

func TestCompilerRejectsNonCanonicalDefinitionAndRFC3339Format(t *testing.T) {
	shared, actions := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	extraDefinition := append(append([]byte(nil), shared...), []byte("\n  PublicTimestamp:\n    schema: {ref: Timestamp}\n    metadata: {kind: timestamp}\n")...)
	if _, err := Compile(extraDefinition, actions); err == nil {
		t.Fatal("non-canonical shared definition compiled")
	}
	rfc3339 := replaceContract("format: gtw-timestamp", "format: date-time")(string(shared))
	if _, err := Compile([]byte(rfc3339), actions); err == nil {
		t.Fatal("raw date-time format compiled")
	}
}

func TestCompiledSetReturnsDetachedTypedActions(t *testing.T) {
	shared, actions := readContractInputs(t, filepath.Join("..", "..", "contracts", "shared-definitions.yaml"), filepath.Join("testdata", "valid-actions.yaml"))
	compiled, err := Compile(shared, actions)
	if err != nil {
		t.Fatal(err)
	}
	first := compiled.Actions()
	first[0].Input.Properties["key"].Type = "number"
	second := compiled.Actions()
	if second[0].Input.Properties["key"].Type != "string" {
		t.Fatal("compiled action accessor exposed mutable internal schema")
	}
}

func readContractInputs(t *testing.T, sharedPath, actionsPath string) ([]byte, []byte) {
	t.Helper()
	shared, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := os.ReadFile(actionsPath)
	if err != nil {
		t.Fatal(err)
	}
	return shared, actions
}

func replaceContract(old, replacement string) func(string) string {
	return func(value string) string {
		if strings.Count(value, old) != 1 {
			panic("fixture replacement is not unique: " + old)
		}
		return strings.Replace(value, old, replacement, 1)
	}
}

func stringValues(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
