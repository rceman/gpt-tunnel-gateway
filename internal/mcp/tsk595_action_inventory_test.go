package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/tokenizer"
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
		ObservedCount   int                  `json:"observed_count"`
		Keep            []string             `json:"keep"`
		Change          []string             `json:"change"`
		Remove          []string             `json:"remove"`
		ChangeDetails   []tsk595ChangeDetail `json:"change_details"`
		IdentityAliases []string             `json:"current_declared_identity_aliases"`
	} `json:"conditional_debug_actions"`
	SharedDefinitions []tsk595SharedDefinition `json:"shared_definitions"`
	SchemaCost        struct {
		Measurements []tsk595SchemaMeasurement `json:"measurements"`
	} `json:"schema_cost"`
}

type tsk595ChangeDetail struct {
	Path            string            `json:"path"`
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
	wantNormal := append(append([]string{}, inventory.NormalActions.Keep...), inventory.NormalActions.Change...)
	wantNormal = append(wantNormal, tsk663MilestonePlanAction)
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	if !equalTSK595Strings(sortedTSK595Keys(entries), sortedTSK595Strings(wantNormal)) {
		t.Fatalf("normal action registry differs from frozen survivors: live=%v frozen=%v", sortedTSK595Keys(entries), sortedTSK595Strings(wantNormal))
	}
	for _, path := range inventory.NormalActions.Remove {
		if _, ok := entries[path]; ok {
			t.Fatalf("removed action %q remains registered", path)
		}
	}
	for _, path := range inventory.NormalActions.Change {
		if _, ok := entries[path]; !ok {
			t.Fatalf("CHANGE path %q is not registered", path)
		}
	}
	for _, path := range inventory.NormalActions.OpaqueOutputs {
		if entries[path].OutputSchema["additionalProperties"] == true {
			t.Fatalf("opaque output %s remains open-world in its compiled contract", path)
		}
		if !containsTSK595String(inventory.NormalActions.Change, path) {
			t.Fatalf("opaque output %s is not classified CHANGE", path)
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
	wantMilestones := append(append([]string{}, inventory.TrackMilestone.Milestone...), "milestone/guide", tsk663MilestonePlanAction)
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
	for _, path := range []string{"task/submit-tests", "system/call", "system/schema", "debug/task_legacy_revision_list", "debug/task_legacy_revision_read"} {
		if _, exists := entries[path]; exists {
			t.Fatalf("retired action %s is registered", path)
		}
	}
	for path, entry := range entries {
		if retiredTSK595ActionPath(path) {
			t.Fatalf("retired action family is registered: %s", path)
		}
		if containsSchemaProperty(entry.InputSchema, "agent_ref") || containsSchemaProperty(entry.OutputSchema, "agent_ref") || containsSchemaProperty(entry.InputSchema, "runtime_ref") || containsSchemaProperty(entry.OutputSchema, "runtime_ref") {
			t.Fatalf("normal action %s exposes a runtime/Agent reference selector", path)
		}
		for _, field := range []string{"authority_role", "required_role", "allowed_roles", "role_gate"} {
			if containsSchemaProperty(entry.InputSchema, field) || containsSchemaProperty(entry.OutputSchema, field) {
				t.Fatalf("normal action %s exposes distributed role-gate field %q", path, field)
			}
		}
		if continuation := forbiddenContinuationFields(entry.OutputSchema); len(continuation) > 0 {
			t.Fatalf("%s exposes result-level continuation fields %v", path, continuation)
		}
	}
	assertOuterCallPagination(t)

	debugServer := newSessionTestServer(t)
	debugServer.Service.Config.Debug.Enabled = true
	debugEntries := debugServer.genericActionRegistry(debugServer.tools())
	debugPaths := actionPathsWithPrefix(debugEntries, "debug/")
	wantDebug := append(append([]string{}, inventory.ConditionalDebug.Keep...), inventory.ConditionalDebug.Change...)
	if !equalTSK595Strings(debugPaths, sortedTSK595Strings(wantDebug)) {
		t.Fatalf("conditional debug actions=%v, want surviving inventory=%v", debugPaths, sortedTSK595Strings(wantDebug))
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
	if len(debugAliases) != 0 {
		t.Fatalf("conditional debug identity aliases remain after migration: %v", debugAliases)
	}
	for _, detail := range inventory.ConditionalDebug.ChangeDetails {
		path := detail.Path
		if path == "" && len(detail.Paths) == 1 {
			path = detail.Paths[0]
		}
		entry, ok := debugEntries[path]
		if !ok {
			t.Fatalf("debug migration path %q is not registered", path)
		}
		for current, target := range detail.FieldMigrations {
			if schemaHasMigrationField(entry.OutputSchema, current) {
				t.Errorf("%s output retains migrated field %q", path, current)
			}
			if !schemaHasMigrationField(entry.OutputSchema, target) {
				t.Errorf("%s output omits migrated field %q", path, target)
			}
		}
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
	wantNames := []string{"WorkflowRole", "EntityKeyAndReference", "Track", "Milestone", "Revision", "CursorAndCompactHandle", "GitFingerprint", "NonGitDigestOrHandle", "MutationReason", "Timestamp", "Duration", "TrackReviewSnapshot", "TaskExecution", "TaskVerification", "Operation"}
	for _, name := range wantNames {
		if _, ok := definitions[name]; !ok {
			t.Fatalf("frozen inventory omitted shared definition %q", name)
		}
	}
	if inventory.SelectorContract.TypedPathSelector != "key" {
		t.Fatalf("typed selector=%q, want key", inventory.SelectorContract.TypedPathSelector)
	}
	if len(inventory.SchemaCost.Measurements) == 0 {
		t.Fatal("frozen inventory omitted representative schema-cost measurements")
	}
	for _, measurement := range inventory.SchemaCost.Measurements {
		if measurement.View == "" || measurement.Tokens <= 0 || measurement.JSONBytes <= 0 {
			t.Fatalf("invalid frozen schema-cost measurement: %#v", measurement)
		}
	}

	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	if server.actionContractSet().DefinitionNames() == nil {
		t.Fatal("compiled action contracts have no shared definitions")
	}
	assertBoundedReasonSchema(t, entries["track/update"].InputSchema)
	assertBoundedReasonSchema(t, entries["milestone/update"].InputSchema)
	counter := tokenizer.NewCounter()
	for _, path := range []string{"task", "track", "operation", "task/create", "track/read", "operation/read"} {
		value, err := genericSchemaV2(entries, path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		tokens, err := counter.CountText(data)
		if err != nil || tokens <= 0 || len(data) == 0 {
			t.Fatalf("model-facing schema %s was not measurable: tokens=%d bytes=%d err=%v", path, tokens, len(data), err)
		}
	}
}

func assertTSK595SelectorMigrations(t *testing.T, inventory tsk595Inventory, entries map[string]genericActionEntry) {
	t.Helper()
	for _, detail := range inventory.NormalActions.ChangeDetails {
		group := make([]genericActionEntry, 0, len(detail.Paths))
		for _, path := range detail.Paths {
			entry, ok := entries[path]
			if !ok {
				t.Fatalf("migration path %s is not registered", path)
			}
			group = append(group, entry)
			if detail.CurrentSelector != "" && detail.TargetSelector != "" {
				if lastSelectorPart(detail.TargetSelector) != "key" {
					t.Fatalf("selector migration target %q is not key", detail.TargetSelector)
				}
				if !schemaHasFieldPath(entry.InputSchema, detail.TargetSelector) {
					t.Errorf("%s omits migrated selector %q", path, detail.TargetSelector)
				}
				if schemaHasFieldPath(entry.InputSchema, detail.CurrentSelector) {
					t.Errorf("%s retains retired selector %q", path, detail.CurrentSelector)
				}
			}
		}
		for current, target := range detail.FieldMigrations {
			targetFound := false
			for _, entry := range group {
				if schemaHasMigrationField(entry.InputSchema, current) || schemaHasMigrationField(entry.OutputSchema, current) {
					t.Errorf("%v retains migrated field %q", detail.Paths, current)
				}
				targetFound = targetFound || schemaHasMigrationField(entry.InputSchema, target) || schemaHasMigrationField(entry.OutputSchema, target)
			}
			if !targetFound {
				t.Errorf("%v omits migrated field %q", detail.Paths, target)
			}
		}
		for current, target := range detail.OutputFields {
			targetFound := false
			for _, entry := range group {
				if schemaHasMigrationField(entry.OutputSchema, current) {
					t.Errorf("%v output retains migrated field %q", detail.Paths, current)
				}
				targetFound = targetFound || schemaHasMigrationField(entry.OutputSchema, target)
			}
			if !targetFound {
				t.Errorf("%v output omits migrated field %q", detail.Paths, target)
			}
		}
	}
}

func schemaHasMigrationField(schema any, path string) bool {
	if strings.Contains(path, ".") {
		return schemaHasFieldPath(schema, path)
	}
	return containsSchemaProperty(schema, path)
}

func schemaHasFieldPath(schema any, path string) bool {
	parts := strings.Split(path, ".")
	var visit func(any, int) bool
	visit = func(value any, index int) bool {
		if index == len(parts) {
			return true
		}
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if properties, ok := object["properties"].(map[string]any); ok {
			if child, exists := properties[parts[index]]; exists && visit(child, index+1) {
				return true
			}
		}
		for _, key := range []string{"allOf", "oneOf", "anyOf"} {
			if branches, ok := object[key].([]any); ok {
				for _, branch := range branches {
					if visit(branch, index) {
						return true
					}
				}
			}
		}
		if items, exists := object["items"]; exists {
			return visit(items, index)
		}
		return false
	}
	return visit(schema, 0)
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
