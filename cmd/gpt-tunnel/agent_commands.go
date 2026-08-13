package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func agent(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	switch args[0] {
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
		lines := 0
		dedupe := true
		seenLines, seenDedupe := false, false
		for i := 2; i < len(args); {
			if i+1 >= len(args) {
				usage()
			}
			switch args[i] {
			case "--lines":
				value, err := strconv.Atoi(args[i+1])
				if err != nil {
					fatal(fmt.Errorf("invalid agent tail bound"))
				}
				if seenLines {
					usage()
				}
				lines, seenLines = value, true
			case "--dedupe":
				if seenDedupe {
					usage()
				}
				value := args[i+1]
				if value != "true" && value != "false" {
					fatal(fmt.Errorf("invalid agent tail dedupe"))
				}
				dedupe, seenDedupe = value == "true", true
			default:
				usage()
			}
			i += 2
		}
		v, err := s.AgentTailPage(ctx, args[1], service.AgentTailInput{Lines: lines, Dedupe: dedupe, DedupeSet: true})
		if err != nil {
			fatal(err)
		}
		output(v)
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
