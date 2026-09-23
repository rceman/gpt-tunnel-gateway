package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
	"github.com/rceman/gpt-tunnel-gateway/internal/workflowrole"
)

const tsk595InventoryPath = "../../docs/MCP_ACTION_INVENTORY_TSK595.json"

type tsk595Inventory struct {
	PublicTransportTools struct {
		ObservedCount int      `json:"observed_count"`
		Names         []string `json:"names"`
	} `json:"public_transport_tools"`
	NormalActions struct {
		ObservedCount   int                  `json:"observed_count"`
		Keep            []string             `json:"keep"`
		Change          []string             `json:"change"`
		Remove          []string             `json:"remove"`
		ChangeDetails   []tsk595ChangeDetail `json:"change_details"`
		IdentityAliases []string             `json:"current_declared_identity_aliases"`
		OpaqueOutputs   []string             `json:"opaque_outputs_to_close"`
	} `json:"normal_action_inventory"`
	TrackMilestone struct {
		TrackLifecycle []string `json:"track_lifecycle_actions"`
		Milestone      []string `json:"milestone_actions"`
		Membership     struct {
			Track     []string `json:"track"`
			Milestone []string `json:"milestone"`
		} `json:"membership"`
		TrackStatuses []string `json:"track_statuses"`
	} `json:"track_milestone_contract"`
	SelectorContract struct {
		TypedPathSelector string   `json:"typed_path_exact_selector"`
		CrossDomainNames  []string `json:"cross_domain_reference_names"`
	} `json:"selector_contract"`
	ConditionalDebug struct {
		ObservedCount   int      `json:"observed_count"`
		Keep            []string `json:"keep"`
		Change          []string `json:"change"`
		Remove          []string `json:"remove"`
		IdentityAliases []string `json:"current_declared_identity_aliases"`
	} `json:"conditional_debug_actions"`
	SharedDefinitions []tsk595SharedDefinition `json:"shared_definitions"`
	SchemaCost        struct {
		Measurements []tsk595SchemaMeasurement `json:"measurements"`
	} `json:"schema_cost"`
}

type tsk595ChangeDetail struct {
	Paths           []string          `json:"paths"`
	CurrentSelector string            `json:"current_selector"`
	TargetSelector  string            `json:"target_selector"`
	RejectedAliases []string          `json:"rejected_aliases"`
	FieldMigrations map[string]string `json:"field_migrations"`
	OutputFields    map[string]string `json:"output_fields"`
}

type tsk595SharedDefinition struct {
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Values       any      `json:"values"`
	Statuses     []string `json:"statuses"`
	Stages       []string `json:"stages"`
	Outcomes     []string `json:"outcomes"`
	States       []string `json:"states"`
	PublicFields []string `json:"public_fields"`
}

type tsk595SchemaMeasurement struct {
	View      string `json:"view"`
	Tokens    int    `json:"tokens"`
	JSONBytes int    `json:"json_bytes"`
}

func loadTSK595Inventory(t *testing.T) tsk595Inventory {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(tsk595InventoryPath))
	if err != nil {
		t.Fatal(err)
	}
	var inventory tsk595Inventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatalf("decode frozen action inventory: %v", err)
	}
	return inventory
}

func TestTSK595FrozenActionInventory(t *testing.T) {
	inventory := loadTSK595Inventory(t)
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	classified := make(map[string]string, inventory.NormalActions.ObservedCount)
	for decision, paths := range map[string][]string{
		"KEEP":   inventory.NormalActions.Keep,
		"CHANGE": inventory.NormalActions.Change,
		"REMOVE": inventory.NormalActions.Remove,
	} {
		for _, path := range paths {
			if previous, exists := classified[path]; exists {
				t.Fatalf("action %s classified as both %s and %s", path, previous, decision)
			}
			classified[path] = decision
		}
	}
	if len(entries) != inventory.NormalActions.ObservedCount {
		t.Fatalf("normal registry has %d actions, inventory records %d", len(entries), inventory.NormalActions.ObservedCount)
	}
	if !equalTSK595Strings(sortedTSK595Keys(entries), sortedTSK595Keys(classified)) {
		t.Fatalf("normal action inventory differs from live registry; unclassified/missing paths: live=%v frozen=%v", sortedTSK595Keys(entries), sortedTSK595Keys(classified))
	}
	for _, path := range inventory.NormalActions.Change {
		if _, ok := entries[path]; !ok {
			t.Fatalf("CHANGE path %q is not currently registered", path)
		}
	}
	for _, path := range inventory.NormalActions.OpaqueOutputs {
		if entries[path].OutputSchema["additionalProperties"] != true {
			t.Fatalf("opaque output %s is no longer explicitly identified for CHANGE", path)
		}
		if !containsTSK595String(inventory.NormalActions.Change, path) {
			t.Fatalf("opaque output %s is not classified CHANGE", path)
		}
	}
	for _, path := range inventory.NormalActions.Remove {
		if _, ok := entries[path]; !ok {
			t.Fatalf("REMOVE path %q is not present in the current registry", path)
		}
	}

	if got := sortedTSK595Strings(canonicalToolNames()); !equalTSK595Strings(got, sortedTSK595Strings(inventory.PublicTransportTools.Names)) {
		t.Fatalf("public transport tools=%v, frozen=%v", got, inventory.PublicTransportTools.Names)
	}
	if len(canonicalToolNames()) != inventory.PublicTransportTools.ObservedCount {
		t.Fatalf("canonical transport tool count=%d, frozen=%d", len(canonicalToolNames()), inventory.PublicTransportTools.ObservedCount)
	}

	trackPaths := actionPathsWithPrefix(entries, "track/")
	wantTracks := append(append([]string{}, inventory.TrackMilestone.TrackLifecycle...), "track/guide")
	if !equalTSK595Strings(trackPaths, sortedTSK595Strings(wantTracks)) {
		t.Fatalf("Track action set=%v, want lifecycle plus guide=%v", trackPaths, wantTracks)
	}
	milestonePaths := actionPathsWithPrefix(entries, "milestone/")
	wantMilestones := append(append([]string{}, inventory.TrackMilestone.Milestone...), "milestone/guide")
	if !equalTSK595Strings(milestonePaths, sortedTSK595Strings(wantMilestones)) {
		t.Fatalf("Milestone action set=%v, want membership/lifecycle plus guide=%v", milestonePaths, wantMilestones)
	}
	assertActionProperties(t, entries, "track/create", "milestone", "tasks", "title")
	assertActionProperties(t, entries, "track/read", "key", "revision")
	assertActionProperties(t, entries, "milestone/append_task", "key", "reason", "tasks")
	assertActionProperties(t, entries, "milestone/remove_task", "key", "reason", "tasks")
	if containsSchemaProperty(entries["milestone/read"].OutputSchema, "tracks") || containsSchemaProperty(entries["milestone/update"].InputSchema, "tracks") {
		t.Fatal("Milestone contract regained embedded Track membership")
	}
	if _, exists := entries["task/submit-tests"]; exists {
		t.Fatal("normal submit-tests action must not be frozen")
	}

	for _, path := range inventory.NormalActions.Remove {
		if path != "system/call" && path != "system/schema" {
			t.Fatalf("unexpected registered REMOVE action %q", path)
		}
	}
	for _, path := range sortedTSK595Keys(entries) {
		if retiredTSK595ActionPath(path) {
			t.Fatalf("retired action family is registered: %s", path)
		}
		entry := entries[path]
		if containsSchemaProperty(entry.InputSchema, "agent_ref") || containsSchemaProperty(entry.OutputSchema, "agent_ref") || containsSchemaProperty(entry.InputSchema, "runtime_ref") || containsSchemaProperty(entry.OutputSchema, "runtime_ref") {
			t.Fatalf("normal action %s exposes a runtime/Agent reference selector", path)
		}
		for _, field := range []string{"authority_role", "required_role", "allowed_roles", "role_gate"} {
			if containsSchemaProperty(entry.InputSchema, field) || containsSchemaProperty(entry.OutputSchema, field) {
				t.Fatalf("normal action %s exposes distributed role-gate field %q", path, field)
			}
		}
		if path != "system/call" {
			if continuation := forbiddenContinuationFields(entry.OutputSchema); len(continuation) > 0 {
				t.Fatalf("%s exposes result-level continuation fields %v", path, continuation)
			}
		}
	}
	for _, path := range []string{"debug/task_legacy_revision_list", "debug/task_legacy_revision_read"} {
		if _, exists := entries[path]; exists {
			t.Fatalf("source-only legacy action %s is registered", path)
		}
	}

	assertOuterCallPagination(t)
	assertTSK595SelectorMigrations(t, inventory, entries)
	assertTSK595IdentityAliases(t, inventory.NormalActions.IdentityAliases, entries)

	server.Service.Config.Debug.Enabled = true
	debugEntries := server.genericActionRegistry(server.tools())
	debugPaths := actionPathsWithPrefix(debugEntries, "debug/")
	classifiedDebug := append(append(append([]string{}, inventory.ConditionalDebug.Keep...), inventory.ConditionalDebug.Change...), inventory.ConditionalDebug.Remove...)
	if len(debugPaths) != inventory.ConditionalDebug.ObservedCount || !equalTSK595Strings(debugPaths, sortedTSK595Strings(classifiedDebug)) {
		t.Fatalf("conditional debug actions=%v, frozen=%v", debugPaths, sortedTSK595Strings(classifiedDebug))
	}
	for _, path := range []string{"debug/prompt", "debug/tail", "debug/await"} {
		if !containsSchemaProperty(debugEntries[path].InputSchema, "agent_ref") {
			t.Fatalf("debug-only direct Agent reference missing from %s", path)
		}
	}
	var debugAliases []string
	for _, path := range debugPaths {
		collectTSK595IdentityFields(path+".input", debugEntries[path].InputSchema, &debugAliases)
		collectTSK595IdentityFields(path+".output", debugEntries[path].OutputSchema, &debugAliases)
	}
	if !equalTSK595Strings(sortedTSK595Strings(debugAliases), sortedTSK595Strings(inventory.ConditionalDebug.IdentityAliases)) {
		t.Fatalf("conditional debug identity alias inventory=%v, frozen=%v", sortedTSK595Strings(debugAliases), inventory.ConditionalDebug.IdentityAliases)
	}
}

func TestTSK595SharedDefinitionsAndSchemaCost(t *testing.T) {
	inventory := loadTSK595Inventory(t)
	definitions := make(map[string]tsk595SharedDefinition, len(inventory.SharedDefinitions))
	for _, definition := range inventory.SharedDefinitions {
		if definition.State != "canonical" {
			t.Fatalf("shared definition %s state=%q, want canonical", definition.Name, definition.State)
		}
		definitions[definition.Name] = definition
	}
	for _, required := range []string{"WorkflowRole", "EntityKeyAndReference", "Track", "Milestone", "Revision", "CursorAndCompactHandle", "GitFingerprint", "NonGitDigestOrHandle", "MutationReason", "Timestamp", "Duration", "TrackReviewSnapshot", "TaskExecution", "TaskVerification", "Operation"} {
		if _, ok := definitions[required]; !ok {
			t.Fatalf("frozen inventory omitted shared definition %q", required)
		}
	}
	workflowRoles := sharedStringValues(t, definitions["WorkflowRole"].Values)
	if !equalTSK595Strings(workflowRoles, sortedTSK595Strings(workflowrole.Names())) {
		t.Fatalf("WorkflowRole values=%v, implementation=%v", workflowRoles, workflowrole.Names())
	}
	entityValues := sharedObjectValues(t, definitions["EntityKeyAndReference"].Values)
	if entityValues["typed_path_selector"] != "key" || !equalTSK595Strings(sharedStringValues(t, entityValues["cross_domain_fields"]), []string{"project", "task", "milestone", "track", "operation", "session", "agent"}) {
		t.Fatalf("EntityKeyAndReference definition=%#v", entityValues)
	}
	if sharedObjectValues(t, definitions["Revision"].Values)["minimum"] != float64(1) {
		t.Fatalf("Revision definition=%#v", definitions["Revision"].Values)
	}
	cursorValues := sharedObjectValues(t, definitions["CursorAndCompactHandle"].Values)
	if cursorValues["continuation_output"] != "call.pagination.next_cursor" || cursorValues["public_handle_max_ascii_length"] != float64(8) || cursorValues["self_contained_public_state"] != false {
		t.Fatalf("CursorAndCompactHandle definition=%#v", cursorValues)
	}
	gitValues := sharedObjectValues(t, definitions["GitFingerprint"].Values)
	if gitValues["wire_length"] != float64(8) || gitValues["pattern"] != "^[0-9a-f]{8}$" {
		t.Fatalf("GitFingerprint definition=%#v", gitValues)
	}
	nonGitValues := sharedObjectValues(t, definitions["NonGitDigestOrHandle"].Values)
	if nonGitValues["default"] != "omit if caller does not need it" || !strings.Contains(nonGitValues["needed_reference"].(string), "8 ASCII") {
		t.Fatalf("NonGitDigestOrHandle definition=%#v", nonGitValues)
	}
	reasonValues := sharedObjectValues(t, definitions["MutationReason"].Values)
	if reasonValues["minimum_length"] != float64(1) || reasonValues["maximum_length"] != float64(1024) {
		t.Fatalf("MutationReason definition=%#v", reasonValues)
	}
	if sharedObjectValues(t, definitions["Timestamp"].Values)["json_schema_format"] != "date-time" {
		t.Fatalf("Timestamp definition=%#v", definitions["Timestamp"].Values)
	}
	if sharedObjectValues(t, definitions["Duration"].Values)["wire_type"] != "integer" {
		t.Fatalf("Duration definition=%#v", definitions["Duration"].Values)
	}
	if inventory.SelectorContract.TypedPathSelector != "key" {
		t.Fatalf("typed selector=%q, want key", inventory.SelectorContract.TypedPathSelector)
	}
	for _, required := range []string{"project", "task", "milestone", "track", "operation", "session", "agent"} {
		if !containsTSK595String(inventory.SelectorContract.CrossDomainNames, required) {
			t.Fatalf("cross-domain reference names omit %q", required)
		}
	}
	if !equalTSK595Strings(inventory.TrackMilestone.TrackStatuses, model.TrackStatuses()) || !equalTSK595Strings(definitions["Track"].Statuses, model.TrackStatuses()) {
		t.Fatalf("Track statuses=%v shared=%v model=%v", inventory.TrackMilestone.TrackStatuses, definitions["Track"].Statuses, model.TrackStatuses())
	}
	if !equalTSK595Strings(definitions["Milestone"].Statuses, model.MilestoneStatuses()) {
		t.Fatalf("Milestone statuses=%v, model=%v", definitions["Milestone"].Statuses, model.MilestoneStatuses())
	}
	if !equalTSK595Strings(definitions["TrackReviewSnapshot"].PublicFields, []string{"head", "tree", "track_revision", "tasks[{key,revision}]", "submitted_at", "submitted_by"}) {
		t.Fatalf("TrackReviewSnapshot public fields=%v", definitions["TrackReviewSnapshot"].PublicFields)
	}
	executionStatuses := []string{
		model.TaskExecutionPlanned, model.TaskExecutionDispatched, model.TaskExecutionInProgress,
		model.TaskExecutionAwaitingReview, model.TaskExecutionChangesRequested, model.TaskExecutionReadyForVerification,
		model.TaskExecutionVerifying, model.TaskExecutionVerified, model.TaskExecutionIntegrating,
		model.TaskExecutionIntegrated, model.TaskExecutionDone, model.TaskExecutionBlocked, model.TaskExecutionFailed,
	}
	if !equalTSK595Strings(definitions["TaskExecution"].Statuses, executionStatuses) {
		t.Fatalf("TaskExecution statuses=%v, model=%v", definitions["TaskExecution"].Statuses, executionStatuses)
	}
	if !equalTSK595Strings(definitions["TaskExecution"].Stages, []string{"code", "tests", "rebase"}) {
		t.Fatalf("TaskExecution stages=%v", definitions["TaskExecution"].Stages)
	}
	verificationOutcomes := []string{model.TaskExecutionVerificationSucceeded, model.TaskExecutionVerificationFailed, model.TaskExecutionVerificationInterrupted}
	if !equalTSK595Strings(definitions["TaskVerification"].Outcomes, verificationOutcomes) {
		t.Fatalf("TaskVerification outcomes=%v, model=%v", definitions["TaskVerification"].Outcomes, verificationOutcomes)
	}
	if !equalTSK595Strings(definitions["Operation"].States, []string{"accepted", "running", "completed", "failed", "outcome_unknown"}) {
		t.Fatalf("Operation states=%v", definitions["Operation"].States)
	}

	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	cursor := publicServerCursorSchema()
	if cursor["minLength"] != 8 || cursor["maxLength"] != 8 {
		t.Fatalf("current compact cursor schema=%v", cursor)
	}
	assertBoundedReasonSchema(t, entries["track/update"].InputSchema)
	assertBoundedReasonSchema(t, entries["milestone/update"].InputSchema)
	if outputDateTime()["format"] != "date-time" {
		t.Fatalf("Timestamp schema=%v", outputDateTime())
	}
	assertTrackReviewSchema(t, entries["track/read"].OutputSchema)

	measurements := map[string]tsk595SchemaMeasurement{}
	for _, measurement := range inventory.SchemaCost.Measurements {
		measurements[measurement.View] = measurement
	}
	counter := tokenizer.NewCounter()
	for _, path := range []string{"task", "track", "operation", "task/create", "track/read", "operation/read"} {
		value, err := genericSchemaV2(entries, path)
		if err != nil {
			t.Fatal(err)
		}
		assertSchemaMeasurement(t, counter, measurements, path, value)
	}
	for _, domain := range []string{"task", "track", "operation"} {
		paths := actionPathsWithPrefix(entries, domain+"/")
		tokens, jsonBytes := 0, 0
		for _, path := range paths {
			value, err := genericSchemaV2(entries, path)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			count, err := counter.CountText(data)
			if err != nil {
				t.Fatal(err)
			}
			tokens += count
			jsonBytes += len(data)
		}
		assertSchemaMeasurementTotals(t, measurements, domain+"/full_actions", tokens, jsonBytes)
	}
	var descriptors []map[string]any
	publicTools := server.publicTools()
	for _, name := range canonicalToolNames() {
		tool := publicTools[name]
		descriptors = append(descriptors, map[string]any{
			"name": name, "description": tool.Description, "inputSchema": tool.InputSchema,
			"outputSchema": tool.OutputSchema, "annotations": tool.Annotations,
		})
	}
	assertSchemaMeasurement(t, counter, measurements, "tools/list", descriptors)
}

func assertTSK595SelectorMigrations(t *testing.T, inventory tsk595Inventory, entries map[string]genericActionEntry) {
	t.Helper()
	for _, detail := range inventory.NormalActions.ChangeDetails {
		if detail.CurrentSelector == "" || detail.TargetSelector == "" {
			continue
		}
		if lastSelectorPart(detail.TargetSelector) != "key" {
			t.Fatalf("selector migration target %q is not key", detail.TargetSelector)
		}
		for _, path := range detail.Paths {
			entry, ok := entries[path]
			if !ok {
				t.Fatalf("selector migration path %s is not registered", path)
			}
			if !containsSchemaProperty(entry.InputSchema, lastSelectorPart(detail.CurrentSelector)) {
				t.Fatalf("%s current selector %q is not present in the registered schema", path, detail.CurrentSelector)
			}
		}
	}
	for _, path := range []string{"operation/read", "operation/await"} {
		properties := schemaProperties(entries[path].InputSchema)
		if _, ok := properties["operation_id"]; !ok {
			t.Fatalf("%s current selector inventory is stale", path)
		}
		if _, ok := properties["key"]; ok {
			t.Fatalf("%s current schema unexpectedly exposes both current and target selectors", path)
		}
	}
}

func assertTSK595IdentityAliases(t *testing.T, expected []string, entries map[string]genericActionEntry) {
	t.Helper()
	var actual []string
	for path, entry := range entries {
		collectTSK595IdentityFields(path+".input", entry.InputSchema, &actual)
		collectTSK595IdentityFields(path+".output", entry.OutputSchema, &actual)
	}
	actual = sortedTSK595Strings(actual)
	if !equalTSK595Strings(actual, sortedTSK595Strings(expected)) {
		t.Fatalf("declared identity alias inventory differs; live=%v frozen=%v", actual, sortedTSK595Strings(expected))
	}
	changed := make(map[string]bool, len(entries))
	for _, path := range []string{
		"agent/await", "agent/interrupt", "agent/prompt", "agent/status", "agent/tail", "callback/remove",
		"gateway/capabilities", "gateway/status", "operation/await", "operation/read", "project/status",
		"runtime/logs", "runtime/restart", "session/end", "session/info", "session/list", "system/await",
		"task/current", "task/dispatch", "task/integrate", "task/review_decide", "task/rework", "task/status",
		"task/submit-code", "task/submit-rebase", "task/test",
	} {
		changed[path] = true
	}
	for _, alias := range actual {
		path := strings.SplitN(alias, ".", 2)[0]
		if !changed[path] {
			t.Fatalf("unclassified public identity alias %s", alias)
		}
	}
}

func assertOuterCallPagination(t *testing.T) {
	t.Helper()
	callSchema := genericCallOutputSchema()
	branches, ok := callSchema["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("call output schema=%#v", callSchema)
	}
	var success map[string]any
	for _, branch := range branches {
		value, _ := branch.(map[string]any)
		properties, _ := value["properties"].(map[string]any)
		if _, ok := properties["pagination"]; ok {
			success = properties
			break
		}
	}
	if success == nil {
		t.Fatal("call success schema omitted outer pagination")
	}
	paginationSchema, _ := success["pagination"].(map[string]any)
	paginationProperties, _ := paginationSchema["properties"].(map[string]any)
	if _, ok := paginationProperties["next_cursor"]; !ok {
		t.Fatalf("outer pagination schema=%#v", paginationSchema)
	}
	if _, ok := success["next_cursor"]; ok {
		t.Fatal("call success schema exposes next_cursor outside pagination")
	}
	cursor := publicServerCursorSchema()
	if cursor["maxLength"] != 8 {
		t.Fatalf("outer cursor exceeds compact handle bound: %#v", cursor)
	}
}

func assertTrackReviewSchema(t *testing.T, schema map[string]any) {
	t.Helper()
	properties := schemaProperties(schema)
	review, ok := properties["review"].(map[string]any)
	if !ok {
		t.Fatalf("track/read review schema=%#v", properties["review"])
	}
	reviewProperties := schemaProperties(review)
	for _, field := range []string{"head", "tree", "track_revision", "tasks", "submitted_at", "submitted_by"} {
		if _, ok := reviewProperties[field]; !ok {
			t.Fatalf("Track review snapshot omitted %q", field)
		}
	}
	for _, field := range []string{"digest", "revision_sha256"} {
		if _, ok := reviewProperties[field]; ok {
			t.Fatalf("Track review public snapshot exposes unnecessary full digest %q", field)
		}
	}
	head := reviewProperties["head"].(map[string]any)
	if head["maxLength"] != 8 || head["pattern"] != "^[0-9a-f]{8}$" {
		t.Fatalf("Track review head fingerprint=%#v", head)
	}
}

func assertBoundedReasonSchema(t *testing.T, schema map[string]any) {
	t.Helper()
	reason, ok := schemaProperties(schema)["reason"].(map[string]any)
	if !ok || reason["minLength"] != 1 || reason["maxLength"] != 1024 {
		t.Fatalf("mutation reason schema=%#v", reason)
	}
}

func assertSchemaMeasurement(t *testing.T, counter *tokenizer.Counter, measurements map[string]tsk595SchemaMeasurement, view string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := counter.CountText(data)
	if err != nil {
		t.Fatal(err)
	}
	measurement, ok := measurements[view]
	if !ok {
		t.Fatalf("schema cost inventory omitted %q", view)
	}
	if tokens != measurement.Tokens || len(data) != measurement.JSONBytes {
		t.Fatalf("schema cost %s tokens=%d bytes=%d, frozen tokens=%d bytes=%d", view, tokens, len(data), measurement.Tokens, measurement.JSONBytes)
	}
}

func assertSchemaMeasurementTotals(t *testing.T, measurements map[string]tsk595SchemaMeasurement, view string, tokens, jsonBytes int) {
	t.Helper()
	measurement, ok := measurements[view]
	if !ok {
		t.Fatalf("schema cost inventory omitted %q", view)
	}
	if tokens != measurement.Tokens || jsonBytes != measurement.JSONBytes {
		t.Fatalf("schema cost %s tokens=%d bytes=%d, frozen tokens=%d bytes=%d", view, tokens, jsonBytes, measurement.Tokens, measurement.JSONBytes)
	}
}

func collectTSK595IdentityFields(path string, value any, fields *[]string) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			childPath := path + "." + key
			if key == "id" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_key") {
				*fields = append(*fields, childPath)
			}
			collectTSK595IdentityFields(childPath, child, fields)
		}
	case []any:
		for index, child := range current {
			collectTSK595IdentityFields(path+"["+itoaTSK595(index)+"]", child, fields)
		}
	}
}

func forbiddenContinuationFields(value any) []string {
	var fields []string
	var visit func(any, string)
	visit = func(current any, path string) {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				childPath := path + "." + key
				if key == "cursor" || key == "next_cursor" || key == "has_more" || key == "_pagination" {
					fields = append(fields, childPath)
				}
				visit(child, childPath)
			}
		case []any:
			for index, child := range item {
				visit(child, path+"["+itoaTSK595(index)+"]")
			}
		}
	}
	visit(value, "output")
	return fields
}

func containsSchemaProperty(value any, name string) bool {
	found := false
	var visit func(any)
	visit = func(current any) {
		switch item := current.(type) {
		case map[string]any:
			for key, child := range item {
				if key == name {
					found = true
				}
				visit(child)
			}
		case []any:
			for _, child := range item {
				visit(child)
			}
		}
	}
	visit(value)
	return found
}

func actionPathsWithPrefix(entries map[string]genericActionEntry, prefix string) []string {
	var paths []string
	for path := range entries {
		if strings.HasPrefix(path, prefix) {
			paths = append(paths, path)
		}
	}
	return sortedTSK595Strings(paths)
}

func retiredTSK595ActionPath(path string) bool {
	for _, prefix := range []string{"queue/", "train/", "hotfix/", "attempt/", "wave/", "deploy/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return path == "project/deploy" || path == "task/submit-tests"
}

func sharedObjectValues(t *testing.T, value any) map[string]any {
	t.Helper()
	values, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("shared object values=%#v", value)
	}
	return values
}

func sharedStringValues(t *testing.T, value any) []string {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("shared string values=%#v", value)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("shared string value=%#v", value)
		}
		result = append(result, text)
	}
	return sortedTSK595Strings(result)
}

func lastSelectorPart(value string) string {
	parts := strings.Split(value, ".")
	return parts[len(parts)-1]
}

func assertActionProperties(t *testing.T, entries map[string]genericActionEntry, path string, properties ...string) {
	t.Helper()
	entry, ok := entries[path]
	if !ok {
		t.Fatalf("missing action %s", path)
	}
	for _, property := range properties {
		if !containsSchemaProperty(entry.InputSchema, property) {
			t.Fatalf("%s input schema omitted %q", path, property)
		}
	}
}

func sortedTSK595Keys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return sortedTSK595Strings(keys)
}

func sortedTSK595Strings(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

func equalTSK595Strings(left, right []string) bool {
	left, right = sortedTSK595Strings(left), sortedTSK595Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsTSK595String(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func itoaTSK595(value int) string {
	const digits = "0123456789"
	if value < 10 {
		return string(digits[value])
	}
	return itoaTSK595(value/10) + string(digits[value%10])
}
