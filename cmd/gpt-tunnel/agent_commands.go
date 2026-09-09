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
		if len(args) < 3 || len(args) > 4 {
			usage()
		}
		ex, rest := expected(args[3:])
		if len(rest) != 0 {
			usage()
		}
		db, err := sqlitestore.Open(s.Config.StateDir)
		if err != nil {
			fatal(fmt.Errorf("open Shared/Local durability for Agent registration: %w", err))
		}
		defer db.Close()
		s.Durability = db
		v, _, err := s.AgentRegister(ctx, service.AgentRegisterInput{ProjectID: args[1], AgentID: args[2], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if err != nil {
			fatal(err)
		}
		output(v)
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
