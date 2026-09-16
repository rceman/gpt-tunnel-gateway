package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func agent(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	switch args[0] {
	case "register":
		input, err := parseAgentRegisterArgs(args[1:])
		if err != nil {
			usage()
		}
		projectID, err := s.ProjectIDForRoot(ctx, "")
		if err != nil {
			fatal(err)
		}
		db, err := sqlitestore.Open(s.Config.StateDir)
		if err != nil {
			fatal(fmt.Errorf("open Shared/Local durability for Agent registration: %w", err))
		}
		defer db.Close()
		s.Durability = db
		input.ProjectID = projectID
		result, err := s.AgentBootstrap(ctx, input)
		if err != nil {
			fatal(err)
		}
		output(renderAgentRegister(result))
	case "send":
		if len(args) != 4 || args[2] != "--text" {
			usage()
		}
		v, err := s.AgentSend(ctx, args[1], args[3])
		if err != nil {
			fatal(err)
		}
		output(v)
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
		v, err := s.AgentStatus(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		output(v)
	default:
		usage()
	}
}
