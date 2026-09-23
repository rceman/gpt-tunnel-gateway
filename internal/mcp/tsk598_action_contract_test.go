package mcp

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
)

func TestTSK598FrozenActionContractsAndRegisteredHandlers(t *testing.T) {
	inventory := loadTSK595Inventory(t)
	want := map[string]string{}
	for _, path := range append(append([]string{}, inventory.NormalActions.Keep...), inventory.NormalActions.Change...) {
		if _, exists := want[path]; exists {
			t.Fatalf("duplicate normal inventory action %q", path)
		}
		want[path] = "normal"
	}
	for _, path := range append(append([]string{}, inventory.ConditionalDebug.Keep...), inventory.ConditionalDebug.Change...) {
		if _, exists := want[path]; exists {
			t.Fatalf("duplicate debug inventory action %q", path)
		}
		want[path] = "debug"
	}
	want[tsk663MilestonePlanAction] = "normal"

	server := newSessionTestServer(t)
	server.Service.Config.Debug.Enabled = true
	entries := server.genericActionRegistry(server.tools())
	assertTSK595SelectorMigrations(t, inventory, entries)
	contracts := server.actionContractSet()
	if got := sortedTSK595Keys(entries); !equalTSK595Strings(got, sortedTSK595Keys(want)) {
		t.Fatalf("active handlers differ from frozen surviving actions: got %v, want %v", got, sortedTSK595Keys(want))
	}
	if got := contracts.Paths(); !equalTSK595Strings(got, sortedTSK595Keys(want)) {
		t.Fatalf("compiled contracts differ from frozen surviving actions: got %v, want %v", got, sortedTSK595Keys(want))
	}
	for path, surface := range want {
		contract, ok := contracts.Action(path)
		if !ok {
			t.Fatalf("frozen action %q has no compiled contract", path)
		}
		entry, ok := entries[path]
		if !ok {
			t.Fatalf("frozen action %q has no registered handler", path)
		}
		if contract.Path != path || contract.Metadata.Surface != surface || entry.Contract.Path != path {
			t.Fatalf("action %q contract/handler metadata mismatch: contract=%#v", path, contract)
		}
		if !reflect.DeepEqual(entry.InputSchema, contract.Input.JSONSchema()) || !reflect.DeepEqual(entry.OutputSchema, contract.Output.JSONSchema()) {
			t.Fatalf("action %q handler registry is not projected from its compiled contract", path)
		}
		if alias := tsk598IdentityAlias(contract.Input, path+".input"); alias != "" {
			t.Errorf("action %s input contains unexplained identity field %s", path, alias)
		}
		if alias := tsk598IdentityAlias(contract.Output, path+".output"); alias != "" {
			t.Errorf("action %s output contains unexplained identity field %s", path, alias)
		}
		if continuation := tsk598ContinuationField(contract.Output, path+".output"); continuation != "" {
			t.Errorf("action %s output contains result-level continuation field %s", path, continuation)
		}
		if contract.Metadata.Selector == "key" {
			if key := contract.Input.Properties["key"]; key == nil || key.RefName != "EntityKeyAndReference" {
				t.Errorf("action %s does not use the typed-path key selector", path)
			}
		}
	}
	for _, removed := range append(append([]string{}, inventory.NormalActions.Remove...), inventory.ConditionalDebug.Remove...) {
		if _, ok := entries[removed]; ok {
			t.Errorf("removed action %q remains registered", removed)
		}
		if _, ok := contracts.Action(removed); ok {
			t.Errorf("removed action %q remains compiled", removed)
		}
	}
	for _, retired := range []string{"system/call", "system/schema"} {
		if _, ok := entries[retired]; ok {
			t.Errorf("retired transport action %q remains registered", retired)
		}
		if _, ok := contracts.Action(retired); ok {
			t.Errorf("retired transport action %q remains compiled", retired)
		}
	}
}

func TestTSK598RuntimeOutputProjectionFailsClosed(t *testing.T) {
	server := newSessionTestServer(t)
	if _, err := server.projectContractOutput("session/info", map[string]any{"unexpected": true}, ""); err == nil {
		t.Fatal("runtime output projection accepted a value outside the compiled contract")
	}
}

func TestTSK598OperationSelectorsAndCompactDiscoveryAreContractDerived(t *testing.T) {
	contracts, err := actioncontract.LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"operation/read", "operation/await"} {
		action, ok := contracts.Action(path)
		if !ok {
			t.Fatalf("missing %s contract", path)
		}
		if action.Metadata.Selector != "key" || action.Input.Properties["key"] == nil || !containsTSK595String(action.Input.Required, "key") {
			t.Fatalf("%s does not use the canonical required key selector", path)
		}
		for _, alias := range []string{"operation_id", "operation", "id"} {
			input := map[string]any{"key": "EXM-OPR1", alias: "EXM-OPR1"}
			if err := contracts.ValidateInput(path, input); err == nil {
				t.Errorf("%s accepted compatibility alias %q", path, alias)
			}
		}
	}
	if err := contracts.ValidateInput("operation/read", map[string]any{"key": "EXM-OPR1"}); err != nil {
		t.Fatalf("operation/read rejected its canonical selector: %v", err)
	}
	if err := contracts.ValidateInput("operation/await", map[string]any{"key": "EXM-OPR1", "seconds": 1}); err != nil {
		t.Fatalf("operation/await rejected its canonical selector: %v", err)
	}
	if err := contracts.ValidateOutput("operation/read", map[string]any{"id": "EXM-OPR1"}); err == nil {
		t.Fatal("compiled output conformance accepted a bare identity alias")
	}

	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	discovery, err := genericSchemaV2(entries, "operation")
	if err != nil {
		t.Fatal(err)
	}
	actions, _ := discovery["actions"].([]map[string]any)
	var input map[string]any
	for _, action := range actions {
		if action["path"] == "operation/read" {
			input, _ = action["input"].(map[string]any)
			break
		}
	}
	properties, _ := input["properties"].(map[string]any)
	if len(properties) != 1 || properties["key"] == nil {
		t.Fatalf("compact input-first discovery is not compiled from operation/read: %#v", input)
	}
}

func tsk598IdentityAlias(schema *actioncontract.CompiledSchema, path string) string {
	if schema == nil {
		return ""
	}
	for key, child := range schema.Properties {
		childPath := path + "." + key
		if key == "id" || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "_key") {
			return childPath
		}
		if alias := tsk598IdentityAlias(child, childPath); alias != "" {
			return alias
		}
	}
	if schema.Items != nil {
		if alias := tsk598IdentityAlias(schema.Items, path+"[]"); alias != "" {
			return alias
		}
	}
	for _, branch := range append(append(append([]*actioncontract.CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if alias := tsk598IdentityAlias(branch, path); alias != "" {
			return alias
		}
	}
	if schema.AdditionalPropertySchema != nil {
		return tsk598IdentityAlias(schema.AdditionalPropertySchema, path+".*")
	}
	return ""
}

func tsk598ContinuationField(schema *actioncontract.CompiledSchema, path string) string {
	if schema == nil {
		return ""
	}
	for key, child := range schema.Properties {
		childPath := path + "." + key
		if key == "next_cursor" || key == "has_more" || key == "pagination" || key == "_pagination" || key == "cursor" {
			return childPath
		}
		if found := tsk598ContinuationField(child, childPath); found != "" {
			return found
		}
	}
	if schema.Items != nil {
		if found := tsk598ContinuationField(schema.Items, path+"[]"); found != "" {
			return found
		}
	}
	for _, branch := range append(append(append([]*actioncontract.CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if found := tsk598ContinuationField(branch, path); found != "" {
			return found
		}
	}
	if schema.AdditionalPropertySchema != nil {
		return tsk598ContinuationField(schema.AdditionalPropertySchema, path+".*")
	}
	return ""
}

func TestTSK598TimestampDefinitionsUseCompactProjection(t *testing.T) {
	contracts, err := actioncontract.LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	definitions := contracts.DefinitionNames()
	want := []string{"CursorAndCompactHandle", "Duration", "EntityKeyAndReference", "GitFingerprint", "Milestone", "MutationReason", "NonGitDigestOrHandle", "Operation", "Revision", "TaskExecution", "TaskVerification", "Timestamp", "Track", "TrackReviewSnapshot", "WorkflowRole"}
	sort.Strings(want)
	if !reflect.DeepEqual(definitions, want) {
		t.Fatalf("canonical shared definition set=%v, want %v", definitions, want)
	}
	for _, action := range contracts.Actions() {
		if field := tsk598NonCanonicalTimestamp(action.Input, action.Path+".input"); field != "" {
			t.Errorf("%s has timestamp field without Timestamp ref: %s", action.Path, field)
		}
		if field := tsk598NonCanonicalTimestamp(action.Output, action.Path+".output"); field != "" {
			t.Errorf("%s has timestamp field without Timestamp ref: %s", action.Path, field)
		}
	}
}

func tsk598NonCanonicalTimestamp(schema *actioncontract.CompiledSchema, path string) string {
	if schema == nil {
		return ""
	}
	for key, child := range schema.Properties {
		childPath := path + "." + key
		if timestampFieldTSK598(key) && child.RefName != "Timestamp" {
			return childPath
		}
		if found := tsk598NonCanonicalTimestamp(child, childPath); found != "" {
			return found
		}
	}
	if schema.Items != nil {
		if found := tsk598NonCanonicalTimestamp(schema.Items, path+"[]"); found != "" {
			return found
		}
	}
	for _, branch := range append(append(append([]*actioncontract.CompiledSchema{}, schema.AllOf...), schema.OneOf...), schema.AnyOf...) {
		if found := tsk598NonCanonicalTimestamp(branch, path); found != "" {
			return found
		}
	}
	return ""
}

func timestampFieldTSK598(field string) bool {
	return strings.HasSuffix(field, "_at") || strings.HasSuffix(field, "_time") || field == "timestamp" || field == "time"
}

func TestTSK598ActionCatalogLoadIsDeterministic(t *testing.T) {
	first, err := actioncontract.LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	second, err := actioncontract.LoadCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Paths(), second.Paths()) {
		t.Fatal(fmt.Sprintf("canonical action path order differs: %v vs %v", first.Paths(), second.Paths()))
	}
	if !reflect.DeepEqual(first.Actions(), second.Actions()) {
		t.Fatal("canonical compiled action models are not deterministic")
	}
	domains, err := first.CompactDiscovery("")
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range domains.Domains {
		firstDiscovery, err := first.CompactDiscovery(domain)
		if err != nil {
			t.Fatal(err)
		}
		secondDiscovery, err := second.CompactDiscovery(domain)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(firstDiscovery, secondDiscovery) {
			t.Fatalf("compact discovery for %s is not deterministic", domain)
		}
	}
}
