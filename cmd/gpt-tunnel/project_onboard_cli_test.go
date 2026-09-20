package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestProjectOnboardPositionalArgsMapToCanonicalWorkerBinding(t *testing.T) {
	input, err := parseProjectOnboardArgs([]string{"AIR", "agentir_worker"})
	if err != nil || input.ProjectCode != "AIR" || input.WorkerRelay != "agentir_worker" || input.Root != "" || input.LeadRelay != "" {
		t.Fatalf("positional input=%#v err=%v", input, err)
	}
	input, err = parseProjectOnboardArgs([]string{"--root", "/tmp/agentir", "AIR", "agentir_worker"})
	if err != nil || input.Root != "/tmp/agentir" || input.ProjectCode != "AIR" || input.WorkerRelay != "agentir_worker" || input.LeadRelay != "" {
		t.Fatalf("root override input=%#v err=%v", input, err)
	}
}

func TestProjectOnboardRejectsMissingExtraAndLegacyArguments(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"AIR"},
		{"AIR", "agentir_worker", "extra"},
		{"--code", "AIR", "--worker-relay", "agentir_worker"},
		{"AIR", "--worker-relay"},
		{"--root", "/tmp/agentir", "AIR"},
	} {
		if _, err := parseProjectOnboardArgs(args); err == nil {
			t.Fatalf("invalid project onboard arguments accepted: %#v", args)
		}
	}
}

func TestAgentRegisterArgsDefaultRoleAndExplicitIdentity(t *testing.T) {
	input, err := parseAgentRegisterArgs([]string{"--relay", "repodex_master"})
	if err != nil || input.Relay != "repodex_master" || input.Role != "" || input.AgentID != "" {
		t.Fatalf("default registration input=%#v err=%v", input, err)
	}
	input, err = parseAgentRegisterArgs([]string{"--relay", "repodex_lead", "--role", "lead", "--code", "RDX-LEAD-ALT"})
	if err != nil || input.Role != "lead" || input.AgentID != "RDX-LEAD-ALT" {
		t.Fatalf("explicit registration input=%#v err=%v", input, err)
	}
}

func TestProjectOnboardCLIOutputIsBoundedActionProjection(t *testing.T) {
	data, err := json.Marshal(renderProjectOnboard(service.ProjectOnboardResult{
		ProjectID:     "gpt-tunnel-gateway",
		ProjectCode:   "GTW",
		Root:          "/srv/gtw",
		Remote:        "origin",
		DefaultBranch: "main",
		Status:        "onboarded",
		Agents: []service.AgentBootstrapResult{
			{ProjectID: "gpt-tunnel-gateway", AgentID: "GTW-WORKER", Role: "worker", Relay: "gtw_master", Status: "registered"},
			{ProjectID: "gpt-tunnel-gateway", AgentID: "GTW-LEAD", Role: "lead", Relay: "gtw_lead", Status: "already_registered"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"onboarded","agents":[{"agent":"GTW-WORKER","status":"registered"},{"agent":"GTW-LEAD","status":"already_registered"}]}`
	if string(data) != want {
		t.Fatalf("project onboard CLI projection drifted:\n got %s\nwant %s", data, want)
	}
	if strings.Contains(string(data), "gpt-tunnel-gateway") {
		t.Fatal("project onboard CLI projection leaked the internal project slug")
	}
}

func TestProjectOnboardCLIOutputOmitsAgentsWithoutRelays(t *testing.T) {
	data, err := json.Marshal(renderProjectOnboard(service.ProjectOnboardResult{
		ProjectID:     "gpt-tunnel-gateway",
		ProjectCode:   "GTW",
		Root:          "/srv/gtw",
		Remote:        "origin",
		DefaultBranch: "main",
		Status:        "already_registered",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"already_registered"}`
	if string(data) != want {
		t.Fatalf("project onboard CLI projection drifted without relays:\n got %s\nwant %s", data, want)
	}
}
