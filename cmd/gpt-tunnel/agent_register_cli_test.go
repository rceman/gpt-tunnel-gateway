package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestAgentRegisterCLIOutputIsBoundedActionProjection(t *testing.T) {
	data, err := json.Marshal(renderAgentRegister(service.AgentBootstrapResult{
		ProjectID: "gpt-tunnel-gateway",
		AgentID:   "GTW-LEAD",
		Role:      "lead",
		Relay:     "gtw_lead",
		Status:    "already_registered",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"agent":"GTW-LEAD","status":"already_registered"}`
	if string(data) != want {
		t.Fatalf("agent register CLI projection drifted:\n got %s\nwant %s", data, want)
	}
	if strings.Contains(string(data), "gpt-tunnel-gateway") {
		t.Fatal("agent register CLI projection leaked the internal project slug")
	}
}

func TestOnboardingCLICommandsPrintBoundedActionProjections(t *testing.T) {
	for file, want := range map[string]string{
		"project_commands.go": "output(renderProjectOnboard(result))",
		"agent_commands.go":   "output(renderAgentRegister(result))",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Fatalf("%s does not print the bounded onboarding action projection", file)
		}
	}
}
