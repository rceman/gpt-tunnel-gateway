package model

import (
	"strings"
	"testing"
	"time"
)

func validTestAgent() Agent {
	now := time.Now().UTC()
	return Agent{
		SchemaVersion:        AgentSchemaVersion,
		ProjectID:            "example",
		AgentID:              "coder",
		Role:                 AgentRoleCoding,
		Enabled:              true,
		RecommendedReasoning: ReasoningHigh,
		Capabilities:         []string{"git", "review"},
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func TestAgentValidationIsClosedAndPortable(t *testing.T) {
	agent := validTestAgent()
	if err := ValidateAgent(agent); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Agent){
		"unknown role":         func(v *Agent) { v.Role = "operator" },
		"unknown reasoning":    func(v *Agent) { v.RecommendedReasoning = "unbounded" },
		"duplicate capability": func(v *Agent) { v.Capabilities = []string{"git", "git"} },
		"invalid capability":   func(v *Agent) { v.Capabilities = []string{"git/exec"} },
		"zero schema":          func(v *Agent) { v.SchemaVersion = 0 },
	} {
		candidate := agent
		mutate(&candidate)
		if err := ValidateAgent(candidate); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	encoded := strings.ToLower(agent.ProjectID + agent.AgentID + agent.Role)
	if strings.Contains(encoded, "session") {
		t.Fatal("portable agent unexpectedly contains host session fields")
	}
}

func TestAgentAvailabilityValidation(t *testing.T) {
	status := AgentAvailabilityStatus{
		SchemaVersion: AgentSchemaVersion,
		ProjectID:     "example",
		AgentID:       "coder",
		Role:          AgentRoleCoding,
		Registered:    true,
		Enabled:       true,
		Bound:         true,
		Usable:        true,
		State:         "usable",
		Reason:        "ready",
	}
	if err := ValidateAgentAvailabilityStatus(status); err != nil {
		t.Fatal(err)
	}
	status.Usable = true
	status.State = "unbound"
	if err := ValidateAgentAvailabilityStatus(status); err == nil {
		t.Fatal("inconsistent usable status was accepted")
	}
}
