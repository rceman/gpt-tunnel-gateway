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
	malformed := json.RawMessage(`{"request":`)
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

func TestProjectOnboardingSchemasUseCanonicalUUIDAndClosedNestedRequest(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	for _, name := range []string{"project_onboard", "project_onboard_status", "project_onboard_recover"} {
		tool := server.tools()[name]
		if tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("%s input schema is not closed", name)
		}
		properties := tool.InputSchema["properties"].(map[string]any)
		operation := properties["operation_id"].(map[string]any)
		if operation["pattern"] != "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$" {
			t.Fatalf("%s operation_id pattern = %#v", name, operation["pattern"])
		}
		request := properties["request"].(map[string]any)
		if request["additionalProperties"] != false {
			t.Fatalf("%s request schema is not closed", name)
		}
		requestVersion := request["properties"].(map[string]any)["schema_version"].(map[string]any)
		if requestVersion["const"] != 1 {
			t.Fatalf("%s request schema_version = %#v, want const 1", name, requestVersion["const"])
		}
		initialPlan := request["properties"].(map[string]any)["initial_plan"].(map[string]any)
		planVersion := initialPlan["properties"].(map[string]any)["schema_version"].(map[string]any)
		if planVersion["const"] != 2 {
			t.Fatalf("%s initial_plan schema_version = %#v, want const 2", name, planVersion["const"])
		}
		expectedRequired := []string{"schema_version", "project_id", "root", "remote", "repository_url", "default_branch", "airelay", "project_code", "gateway_state_dir", "initial_plan", "expected_hub_revision"}
		if got := stringList(request["required"]); len(got) != len(expectedRequired) {
			t.Fatalf("%s request required fields = %v, want %v", name, got, expectedRequired)
		} else {
			for i := range expectedRequired {
				if got[i] != expectedRequired[i] {
					t.Fatalf("%s request required fields = %v, want %v", name, got, expectedRequired)
				}
			}
		}
		var decoded service.ProjectOnboardInput
		if err := decode([]byte(`{"operation_id":"11111111-1111-1111-1111-111111111111","request":{"unexpected":true}}`), &decoded); err == nil {
			t.Fatalf("%s accepted unknown/missing nested request fields", name)
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
