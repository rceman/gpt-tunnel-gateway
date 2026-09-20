package main

import (
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type projectOnboardOutput struct {
	Status string                `json:"status"`
	Agents []agentRegisterOutput `json:"agents,omitempty"`
}

func renderProjectOnboard(result service.ProjectOnboardResult) projectOnboardOutput {
	projected := projectOnboardOutput{Status: result.Status}
	for _, agent := range result.Agents {
		projected.Agents = append(projected.Agents, renderAgentRegister(agent))
	}
	return projected
}

func parseProjectOnboardArgs(args []string) (service.ProjectOnboardInput, error) {
	if len(args) == 2 && !strings.HasPrefix(args[0], "-") && !strings.HasPrefix(args[1], "-") && args[0] != "" && args[1] != "" {
		return service.ProjectOnboardInput{ProjectCode: args[0], WorkerRelay: args[1]}, nil
	}
	if len(args) == 4 && args[0] == "--root" && args[1] != "" && args[2] != "" && args[3] != "" && !strings.HasPrefix(args[2], "-") && !strings.HasPrefix(args[3], "-") {
		return service.ProjectOnboardInput{Root: args[1], ProjectCode: args[2], WorkerRelay: args[3]}, nil
	}
	return service.ProjectOnboardInput{}, fmt.Errorf("project onboard requires <PROJECT_CODE> <WORKER_RELAY>")
}
