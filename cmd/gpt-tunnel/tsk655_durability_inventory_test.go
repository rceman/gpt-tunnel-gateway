package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type tsk655DurabilityRoute struct {
	Path           string `json:"path"`
	Boundary       string `json:"boundary"`
	Classification string `json:"classification"`
}

func TestTSK655Gate20OperatorCLIDurabilityInventory(t *testing.T) {
	expectedDirect := map[string]struct {
		calls  int
		routes []string
	}{
		"admin":        {calls: 1, routes: []string{"admin session mint", "admin session revoke"}},
		"agent":        {calls: 1, routes: []string{"agent register"}},
		"journal":      {calls: 1, routes: []string{"journal migrate"}},
		"project":      {calls: 2, routes: []string{"project onboard", "project update"}},
		"sessionToken": {calls: 1, routes: []string{"session token"}},
	}
	directCalls := tsk655DirectDurabilityCalls(t)
	if len(directCalls) != len(expectedDirect) {
		t.Fatalf("direct durability functions=%#v", directCalls)
	}
	inventory := []tsk655DurabilityRoute{
		{Path: "task read", Boundary: "gateway HTTP", Classification: "daemon-safe"},
		{Path: "task current", Boundary: "gateway HTTP", Classification: "daemon-safe"},
		{Path: "task submit-code", Boundary: "durable gateway HTTP admission", Classification: "daemon-safe"},
		{Path: "task submit-rebase", Boundary: "durable gateway HTTP admission", Classification: "daemon-safe"},
		{Path: "project list", Boundary: "Hub read-only", Classification: "daemon-safe"},
	}
	for function, expected := range expectedDirect {
		if len(directCalls[function]) != expected.calls {
			t.Fatalf("%s direct durability calls=%#v", function, directCalls[function])
		}
		for _, route := range expected.routes {
			inventory = append(inventory, tsk655DurabilityRoute{
				Path:           route,
				Boundary:       "direct sqlitestore.Open",
				Classification: "daemon-down",
			})
		}
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
	encoded, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gate20 operator CLI durability inventory: %s", encoded)
}

func tsk655DirectDurabilityCalls(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string][]string{}
	for _, path := range files {
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Open" {
					return true
				}
				owner, ok := selector.X.(*ast.Ident)
				if !ok || owner.Name != "sqlitestore" {
					return true
				}
				found[function.Name.Name] = append(found[function.Name.Name], path)
				return true
			})
		}
	}
	return found
}
