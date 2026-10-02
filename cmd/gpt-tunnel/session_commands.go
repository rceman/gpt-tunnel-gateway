package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

// session attaches durable managed-role Sessions through the gatewayd operator
// surface. Planner remains bootstrapped only through the project token and the
// public session/start action.
func session(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "attach":
		label, rest := stringFlag("--label", args[1:])
		if len(rest) != 3 {
			usage()
		}
		result, err := operatorCLIRequest[service.SessionAttachResult](ctx, s.Config, "/operator/session/attach", service.SessionAttachInput{
			ProjectCode: rest[0],
			Role:        rest[1],
			AgentID:     rest[2],
			Label:       optionalString(label),
		})
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}
