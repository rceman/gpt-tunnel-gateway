package main

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type agentRegisterOutput struct {
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

func renderAgentRegister(result service.AgentBootstrapResult) agentRegisterOutput {
	return agentRegisterOutput{
		Agent:  result.AgentID,
		Status: result.Status,
	}
}

func parseAgentRegisterArgs(args []string) (service.AgentBootstrapInput, error) {
	var input service.AgentBootstrapInput
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return service.AgentBootstrapInput{}, fmt.Errorf("agent register flag %q requires a value", args[i])
		}
		name, value := args[i], args[i+1]
		if value == "" || seen[name] {
			return service.AgentBootstrapInput{}, fmt.Errorf("invalid or repeated agent register flag %q", name)
		}
		seen[name] = true
		switch name {
		case "--relay":
			input.Relay = value
		case "--role":
			input.Role = value
		case "--code":
			input.AgentID = value
		default:
			return service.AgentBootstrapInput{}, fmt.Errorf("unknown agent register flag %q", name)
		}
		i++
	}
	if input.Relay == "" {
		return service.AgentBootstrapInput{}, fmt.Errorf("agent register requires --relay <airelay-session>")
	}
	return input, nil
}
