package main

import "github.com/rceman/gpt-tunnel-gateway/internal/agentguide"

func guide(args []string) {
	if len(args) != 0 {
		usage()
	}
	output(agentguide.Canonical())
}
