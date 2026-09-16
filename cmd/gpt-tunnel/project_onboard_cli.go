package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

var errInteractiveOnboard = errors.New("interactive project onboarding")

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
	if len(args) == 0 {
		return service.ProjectOnboardInput{}, errInteractiveOnboard
	}
	var input service.ProjectOnboardInput
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			return service.ProjectOnboardInput{}, fmt.Errorf("project onboard flag %q requires a value", args[i])
		}
		name, value := args[i], args[i+1]
		if value == "" || seen[name] {
			return service.ProjectOnboardInput{}, fmt.Errorf("invalid or repeated project onboard flag %q", name)
		}
		seen[name] = true
		switch name {
		case "--code":
			input.ProjectCode = value
		case "--root":
			input.Root = value
		case "--worker-relay":
			input.WorkerRelay = value
		case "--lead-relay":
			input.LeadRelay = value
		default:
			return service.ProjectOnboardInput{}, fmt.Errorf("unknown project onboard flag %q", name)
		}
		i++
	}
	if input.ProjectCode == "" {
		return service.ProjectOnboardInput{}, fmt.Errorf("project onboard requires --code CODE")
	}
	return input, nil
}

func promptProjectOnboard() (service.ProjectOnboardInput, error) {
	reader := bufio.NewReader(os.Stdin)
	read := func(label string, optional bool) (string, error) {
		if optional {
			fmt.Fprint(os.Stderr, label+" (optional): ")
		} else {
			fmt.Fprint(os.Stderr, label+": ")
		}
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	code, err := read("Project code", false)
	if err != nil {
		return service.ProjectOnboardInput{}, err
	}
	worker, err := read("Worker relay", true)
	if err != nil {
		return service.ProjectOnboardInput{}, err
	}
	lead, err := read("Lead relay", true)
	if err != nil {
		return service.ProjectOnboardInput{}, err
	}
	return service.ProjectOnboardInput{ProjectCode: code, WorkerRelay: worker, LeadRelay: lead}, nil
}
