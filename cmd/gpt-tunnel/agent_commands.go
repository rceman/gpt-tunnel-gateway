package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type operatorAgentRegisterCLIRequest struct {
	Root    string `json:"root"`
	AgentID string `json:"agent_id,omitempty"`
	Role    string `json:"role"`
	Relay   string `json:"relay"`
}

type operatorAgentCLIRequest struct {
	ProjectID string `json:"project_id"`
}

type operatorAgentSendCLIRequest struct {
	ProjectID string `json:"project_id"`
	Message   string `json:"message"`
}

func agent(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	switch args[0] {
	case "register":
		input, err := parseAgentRegisterArgs(args[1:])
		if err != nil {
			usage()
		}
		result, err := operatorCLIRequest[service.AgentBootstrapResult](ctx, s.Config, "/operator/agent/register", operatorAgentRegisterCLIRequest{
			Root:    mustWorkingDirectory(),
			AgentID: input.AgentID,
			Role:    input.Role,
			Relay:   input.Relay,
		})
		if err != nil {
			fatal(err)
		}
		output(renderAgentRegister(result))
	case "send":
		if len(args) != 4 || args[2] != "--text" {
			usage()
		}
		result, err := operatorCLIRequest[service.AgentSendResult](ctx, s.Config, "/operator/agent/send", operatorAgentSendCLIRequest{
			ProjectID: args[1],
			Message:   args[3],
		})
		if err != nil {
			fatal(err)
		}
		output(result)
	case "tail":
		seenLines := false
		seenSession := false
		for i := 2; i < len(args); {
			if i+1 >= len(args) {
				usage()
			}
			switch args[i] {
			case "--lines":
				_, err := strconv.Atoi(args[i+1])
				if err != nil {
					fatal(fmt.Errorf("invalid agent tail bound"))
				}
				if seenLines {
					usage()
				}
				seenLines = true
			case "--session":
				if seenSession || args[i+1] == "" {
					usage()
				}
				seenSession = true
			default:
				usage()
			}
			i += 2
		}
		if !seenSession {
			fatal(fmt.Errorf("agent tail requires --session <durable-agent-session>"))
		}
		fatal(fmt.Errorf("agent tail is available through Gateway agent/tail; CLI transport is deferred to GTW-TSK547"))
	case "status":
		if len(args) != 2 {
			usage()
		}
		result, err := operatorCLIRequest[service.AgentStatusResult](ctx, s.Config, "/operator/agent/status", operatorAgentCLIRequest{ProjectID: args[1]})
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}
