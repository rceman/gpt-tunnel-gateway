package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func tsk511SchemaKeys(t *testing.T, path string, schema map[string]any, want []string) {
	t.Helper()
	got := schemaProperties(schema)
	if len(got) != len(want) {
		t.Fatalf("%s properties=%v, want %v", path, got, want)
	}
	for _, key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("%s missing property %q: %v", path, key, got)
		}
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("%s is not closed: %#v", path, schema)
	}
}

func TestTSK511RelationActionContract(t *testing.T) {
	server := &Server{Service: service.NewWithDurabilityDeferredWorkers(config.Config{GatewayID: "tsk511-schema-test", StateDir: t.TempDir()}, nil)}
	entries := server.genericActionRegistry(server.tools())

	create, ok := entries["relation/create"]
	if !ok {
		t.Fatal("relation/create is not registered")
	}
	list, ok := entries["relation/list"]
	if !ok {
		t.Fatal("relation/list is not registered")
	}
	for _, path := range []string{"relation/remove", "relation/delete", "relation/update", "relation/history", "relation/supersede", "relation/correct"} {
		if _, ok := entries[path]; ok {
			t.Fatalf("unsupported relation action %q is registered", path)
		}
	}
	for name, entry := range map[string]genericActionEntry{"relation/create": create, "relation/list": list} {
		if entry.AuthorityRole != actionRoleWorkflow || !entry.SessionBound || !entry.LocalReceiptOnly {
			t.Fatalf("%s authority=%#v", name, entry)
		}
	}

	tsk511SchemaKeys(t, "relation/create input", create.InputSchema, []string{"source", "kind", "target"})
	tsk511SchemaKeys(t, "relation/list input", list.InputSchema, []string{"source", "kind", "direction", "cursor"})
	for path, entry := range map[string]genericActionEntry{"relation/create": create, "relation/list": list} {
		required := stringList(entry.ExecutionInputSchema["required"])
		if len(required) == 0 || required[0] != "project_id" {
			t.Fatalf("%s execution required=%v", path, required)
		}
	}
	if required := stringList(create.ExecutionInputSchema["required"]); len(required) != 4 {
		t.Fatalf("relation/create execution required=%v", required)
	}
	kindSchema, ok := schemaProperties(create.InputSchema)["kind"].(map[string]any)
	if !ok {
		t.Fatal("relation/create kind schema is missing")
	}
	kinds := stringList(kindSchema["enum"])
	if len(kinds) != 4 {
		t.Fatalf("relation kind enum=%v, want the closed v1 enum", kinds)
	}
	for _, want := range model.RelationKinds() {
		found := false
		for _, kind := range kinds {
			if kind == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("relation kind enum omitted %q: %v", want, kinds)
		}
	}
	directionSchema, ok := schemaProperties(list.InputSchema)["direction"].(map[string]any)
	if !ok {
		t.Fatal("relation/list direction schema is missing")
	}
	directions := stringList(directionSchema["enum"])
	if len(directions) != 3 {
		t.Fatalf("relation direction enum=%v", directions)
	}
	if _, has := directionSchema["default"]; has {
		t.Fatal("relation/list direction must not carry a default; outgoing is the server default")
	}
	if required := stringList(list.InputSchema["required"]); len(required) != 1 || required[0] != "source" {
		t.Fatalf("relation/list required=%v", required)
	}

	tsk511SchemaKeys(t, "relation/create output", create.OutputSchema, []string{"source", "kind", "target", "created"})
	tsk511SchemaKeys(t, "relation/list output", list.OutputSchema, []string{"source", "relations"})
	if required := stringList(create.OutputSchema["required"]); len(required) != 4 {
		t.Fatalf("relation/create output required=%v", required)
	}
	if required := stringList(list.OutputSchema["required"]); len(required) != 2 {
		t.Fatalf("relation/list output required=%v", required)
	}

	if projectionClasses["relation/create"] != projectionCompactDefault || projectionClasses["relation/list"] != projectionClosedDefault {
		t.Fatalf("relation projection classes=%v/%v", projectionClasses["relation/create"], projectionClasses["relation/list"])
	}

	tsk511SchemaKeys(t, "task/create input", entries["task/create"].InputSchema, []string{"title", "summary", "objective", "adr_relation", "adr_references", "type", "scope", "acceptance_criteria", "constraints", "priority", "dependencies", "preparation_references", "metadata", "relation_type", "relation_target"})
	tsk511SchemaKeys(t, "adr/create input", entries["adr/create"].InputSchema, []string{"title", "summary", "context", "decision", "consequences", "status", "relation_type", "relation_target"})
	for _, path := range []string{"task/create", "adr/create"} {
		properties := schemaProperties(entries[path].InputSchema)
		relationType, ok := properties["relation_type"].(map[string]any)
		if !ok || relationType["type"] != "string" {
			t.Fatalf("%s relation_type schema=%#v", path, properties["relation_type"])
		}
		if _, has := relationType["default"]; has {
			t.Fatalf("%s relation_type must not carry a default", path)
		}
		if _, ok := properties["relation_target"].(map[string]any); !ok {
			t.Fatalf("%s relation_target schema=%#v", path, properties["relation_target"])
		}
		for _, required := range stringList(entries[path].InputSchema["required"]) {
			if required == "relation_type" || required == "relation_target" {
				t.Fatalf("%s must keep the relation sugar optional", path)
			}
		}
	}
	for _, path := range []string{"task/read", "adr/read"} {
		properties := schemaProperties(entries[path].OutputSchema)
		if _, ok := properties["relations"]; !ok {
			t.Fatalf("%s output omitted the grouped relations projection", path)
		}
		found := false
		for _, required := range stringList(entries[path].OutputSchema["required"]) {
			if required == "relations" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s output must require relations", path)
		}
	}
	if _, ok := schemaProperties(entries["task/update"].InputSchema)["relation_type"]; ok {
		t.Fatal("task/update must not accept create-time relation sugar")
	}
}

func tsk511Call(t *testing.T, fixture *tsk571HTTPFixture, session, action string, input map[string]any) map[string]any {
	t.Helper()
	structured := fixture.call(t, session, action, input)
	if structured["ok"] != true {
		t.Fatalf("%s failed: %#v", action, structured)
	}
	result, ok := structured["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s omitted its result: %#v", action, structured)
	}
	return result
}

func TestTSK511RelationActionsOverPublicTransport(t *testing.T) {
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner, durableSession.RoleWorker}, true, true)
	sessionID := fixture.sessions[durableSession.RolePlanner]
	ctx := context.Background()

	first, err := fixture.server.Service.ADRCreate(ctx, service.ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: "First canonical ADR", Summary: "First ADR summary.", Context: "Context.", Decision: "Decision.", Consequences: "Consequences.", CreatedBy: "planner", UpdatedBy: "planner"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.server.Service.ADRCreate(ctx, service.ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: "Second canonical ADR", Summary: "Second ADR summary.", Context: "Context.", Decision: "Decision.", Consequences: "Consequences.", CreatedBy: "planner", UpdatedBy: "planner"}})
	if err != nil {
		t.Fatal(err)
	}

	created := tsk511Call(t, fixture, sessionID, "relation/create", map[string]any{"source": fixture.task.ID, "kind": model.RelationKindAuthority, "target": first.EntityKey})
	if created["created"] != true || created["source"] != fixture.task.ID || created["kind"] != model.RelationKindAuthority || created["target"] != first.EntityKey {
		t.Fatalf("relation/create result=%#v", created)
	}
	repeat := tsk511Call(t, fixture, sessionID, "relation/create", map[string]any{"source": fixture.task.ID, "kind": model.RelationKindAuthority, "target": first.EntityKey})
	if repeat["created"] != false {
		t.Fatalf("relation/create was not idempotent: %#v", repeat)
	}
	if _, err := fixture.server.Service.ADRCreate(ctx, service.ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: "Replacement ADR", Summary: "Replacement summary.", Context: "Context.", Decision: "Decision.", Consequences: "Consequences.", CreatedBy: "planner", UpdatedBy: "planner"}, RelationType: model.RelationKindSupersedes, RelationTarget: first.EntityKey}); err != nil {
		t.Fatal(err)
	}
	if second.EntityKey == first.EntityKey {
		t.Fatal("ADR identity did not advance")
	}

	outgoing := tsk511Call(t, fixture, sessionID, "relation/list", map[string]any{"source": fixture.task.ID})
	relations, ok := outgoing["relations"].(map[string]any)
	if !ok {
		t.Fatalf("relation/list relations=%#v", outgoing["relations"])
	}
	authority, ok := relations[model.RelationKindAuthority].(map[string]any)
	if !ok || authority[first.EntityKey] != "First canonical ADR" {
		t.Fatalf("grouped authority relations=%#v", relations)
	}

	incoming := tsk511Call(t, fixture, sessionID, "relation/list", map[string]any{"source": first.EntityKey, "direction": "incoming"})
	incomingRelations, _ := incoming["relations"].(map[string]any)
	supersedes, _ := incomingRelations[model.RelationKindSupersedes].(map[string]any)
	if len(supersedes) != 1 {
		t.Fatalf("incoming supersedes relations=%#v", incomingRelations)
	}

	current, err := fixture.server.Service.ADRReadRevision(ctx, "example", first.EntityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.server.Service.ADRUpdateCurrent(ctx, service.ADRUpdateInput{ProjectID: "example", ADRID: first.EntityKey, Title: stringPointer("Renamed canonical ADR"), ExpectedRevision: int(current.Revision), Reason: "rename", UpdatedBy: "planner"}); err != nil {
		t.Fatal(err)
	}
	renamed := tsk511Call(t, fixture, sessionID, "relation/list", map[string]any{"source": fixture.task.ID})
	renamedRelations, _ := renamed["relations"].(map[string]any)
	renamedAuthority, _ := renamedRelations[model.RelationKindAuthority].(map[string]any)
	if renamedAuthority[first.EntityKey] != "Renamed canonical ADR" {
		t.Fatalf("relation/list did not resolve the live title: %#v", renamedRelations)
	}

	read := tsk511Call(t, fixture, sessionID, "task/read", map[string]any{"key": fixture.task.ID})
	readRelations, ok := read["relations"].(map[string]any)
	if !ok {
		t.Fatalf("task/read omitted the grouped relations projection: %#v", read)
	}
	readAuthority, _ := readRelations[model.RelationKindAuthority].(map[string]any)
	if readAuthority[first.EntityKey] != "Renamed canonical ADR" {
		t.Fatalf("task/read grouped relations=%#v", readRelations)
	}

	for name, input := range map[string]map[string]any{
		"unknown kind":      {"source": fixture.task.ID, "kind": "depends_on", "target": first.EntityKey},
		"unknown target":    {"source": fixture.task.ID, "kind": model.RelationKindAuthority, "target": "EXM-ADR99"},
		"wrong family pair": {"source": first.EntityKey, "kind": model.RelationKindCorrects, "target": fixture.task.ID},
	} {
		result := fixture.call(t, sessionID, "relation/create", input)
		if result["ok"] == true {
			t.Fatalf("%s was accepted: %#v", name, result)
		}
	}
	badDirection := fixture.call(t, sessionID, "relation/list", map[string]any{"source": fixture.task.ID, "direction": "sideways"})
	if badDirection["ok"] == true {
		t.Fatalf("invalid direction was accepted: %#v", badDirection)
	}

	worker := fixture.call(t, fixture.sessions[durableSession.RoleWorker], "relation/list", map[string]any{"source": fixture.task.ID})
	if worker["ok"] != true {
		t.Fatalf("bound Worker relation/list was rejected: %#v", worker)
	}
}

func TestTSK511RelationListPaginatesOverPublicTransport(t *testing.T) {
	fixture := newTSK571HTTPFixture(t, []string{durableSession.RolePlanner}, true, true)
	sessionID := fixture.sessions[durableSession.RolePlanner]
	ctx := context.Background()
	for i := 0; i < service.DefaultPublicCollectionLimit+3; i++ {
		adr, err := fixture.server.Service.ADRCreate(ctx, service.ADRCreateInput{ADR: model.ADR{ProjectID: "example", Title: fmt.Sprintf("Paged ADR %02d", i), Summary: "Paged ADR summary.", Context: "Context.", Decision: "Decision.", Consequences: "Consequences.", CreatedBy: "planner", UpdatedBy: "planner"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.server.Service.RelationCreate(ctx, service.RelationCreateInput{ProjectID: "example", Source: fixture.task.ID, Kind: model.RelationKindAuthority, Target: adr.EntityKey, CreatedBy: "planner"}); err != nil {
			t.Fatal(err)
		}
	}
	first := tsk511Call(t, fixture, sessionID, "relation/list", map[string]any{"source": fixture.task.ID})
	authority, _ := first["relations"].(map[string]any)[model.RelationKindAuthority].(map[string]any)
	if len(authority) != service.DefaultPublicCollectionLimit {
		t.Fatalf("first relation page size=%d, want %d", len(authority), service.DefaultPublicCollectionLimit)
	}
	paginationValue, ok := fixture.call(t, sessionID, "relation/list", map[string]any{"source": fixture.task.ID})["pagination"].(map[string]any)
	if !ok {
		t.Fatal("relation/list omitted the public pagination envelope")
	}
	cursor, ok := paginationValue["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("relation/list pagination=%#v", paginationValue)
	}
	second := tsk511Call(t, fixture, sessionID, "relation/list", map[string]any{"source": fixture.task.ID, "cursor": cursor})
	secondAuthority, _ := second["relations"].(map[string]any)[model.RelationKindAuthority].(map[string]any)
	if len(secondAuthority) != 3 {
		t.Fatalf("second relation page size=%d, want 3", len(secondAuthority))
	}
	for key := range secondAuthority {
		if _, repeated := authority[key]; repeated {
			t.Fatalf("relation %s repeated across pages", key)
		}
	}
	stale := fixture.call(t, sessionID, "relation/list", map[string]any{"source": fixture.task.ID, "direction": "incoming", "cursor": cursor})
	if stale["ok"] == true {
		t.Fatalf("cursor crossed the direction scope: %#v", stale)
	}
}

func stringPointer(value string) *string { return &value }
