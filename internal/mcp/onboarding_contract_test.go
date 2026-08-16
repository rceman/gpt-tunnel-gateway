package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/onboarding"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestProjectOnboardingMutationAuthorityPrecedesDecode(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	malformed := json.RawMessage(`{"root":`)
	for _, name := range []string{"project_onboard", "project_onboard_recover"} {
		_, err := server.tools()[name].Execute(context.Background(), malformed)
		if err == nil || err.Error() != "AUTHORITY_UNAVAILABLE" {
			t.Fatalf("%s error = %v, want authority before decode", name, err)
		}
	}
	for _, name := range []string{"project_onboard", "project_onboard_recover"} {
		_, err := server.tools()[name].Execute(authority.WithOperator(context.Background()), malformed)
		if err == nil || err.Error() != "AUTHORITY_UNAVAILABLE" {
			t.Fatalf("%s accepted operator authority: %v", name, err)
		}
	}
}

func TestProjectOnboardingTrustedContextReachesStrictDecoder(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	malformed := json.RawMessage(`{"request":`)
	_, err := server.tools()["project_onboard"].Execute(authority.WithPlanner(context.Background()), malformed)
	if err == nil || err.Error() == "AUTHORITY_UNAVAILABLE" {
		t.Fatalf("trusted context did not reach strict decoder: %v", err)
	}
}

func TestProjectOnboardingSchemasUseMinimalClosedIntent(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tool := server.tools()["project_onboard"]
	if tool.InputSchema["additionalProperties"] != false {
		t.Fatal("project_onboard input schema is not closed")
	}
	properties := tool.InputSchema["properties"].(map[string]any)
	for _, field := range []string{"project_id", "root", "project_code", "initial_objective"} {
		if _, ok := properties[field]; !ok {
			t.Fatalf("project_onboard omitted %s", field)
		}
	}
	if got := stringList(tool.InputSchema["required"]); len(got) != 2 || got[0] != "project_id" || got[1] != "root" {
		t.Fatalf("project_onboard required fields = %v", got)
	}
	if err := validateToolArguments(tool.InputSchema, json.RawMessage(`{"project_id":"example","root":"/tmp","unexpected":true}`)); err == nil {
		t.Fatal("project_onboard accepted an unknown field")
	}
	for _, name := range []string{"project_onboard_status", "project_onboard_recover"} {
		tool := server.tools()[name]
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("%s input schema is not closed", name)
		}
	}
	if annotations := server.tools()["project_onboard"].Annotations; annotations != (ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}) {
		t.Fatalf("project_onboard annotations = %#v", annotations)
	}
	if annotations := server.tools()["project_onboard_recover"].Annotations; annotations != (ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: false,
		IdempotentHint:  true,
		OpenWorldHint:   true,
	}) {
		t.Fatalf("project_onboard_recover annotations = %#v", annotations)
	}
	if annotations := server.tools()["project_onboard_status"].Annotations; annotations != readOnlyAnnotations() {
		t.Fatalf("project_onboard_status annotations = %#v", annotations)
	}
}

func TestProjectRegisterRemainsSeparateFromOnboarding(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(sourceFile), "..", "..")
	registerSource, err := os.ReadFile(filepath.Join(root, "internal", "service", "service.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(registerSource), "ProjectOnboard") {
		t.Fatal("ProjectRegister source references ProjectOnboard")
	}
	serverSource, err := os.ReadFile(filepath.Join(root, "internal", "mcp", "server_plan_tools.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(serverSource)
	start := strings.Index(text, `add("project_register"`)
	end := strings.Index(text[start:], `add("plan_read"`)
	if start < 0 || end < 0 {
		t.Fatal("could not isolate project_register MCP handler")
	}
	if strings.Contains(text[start:start+end], "ProjectOnboard") {
		t.Fatal("project_register MCP handler references ProjectOnboard")
	}
}

func TestProjectOnboardingStatusOutputRedactsLocalCapabilities(t *testing.T) {
	projection := onboarding.StatusProjection{
		OperationID: "11111111-1111-1111-1111-111111111111", ProjectID: "example", State: onboarding.StatePrepared,
		RecoveryStatus: string(onboarding.RecoveryNotRequired), HubBefore: strings.Repeat("a", 40),
	}
	data, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"/home/secret", "project_master", "gateway_state_dir", "mirror_path", "session_key", "repository_url"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status leaked %q: %s", forbidden, text)
		}
	}
}
