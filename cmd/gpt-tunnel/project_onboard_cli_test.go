package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestProjectOnboardInteractiveInputMapsToCanonicalPrimitives(t *testing.T) {
	original := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = original
		_ = reader.Close()
	})
	if _, err := writer.WriteString("RDX\n\nrepodex_lead\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	input, err := promptProjectOnboard()
	if err != nil {
		t.Fatal(err)
	}
	if input.ProjectCode != "RDX" || input.WorkerRelay != "" || input.LeadRelay != "repodex_lead" {
		t.Fatalf("interactive input=%#v", input)
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
