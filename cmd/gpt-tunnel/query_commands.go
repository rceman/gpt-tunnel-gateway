package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func query(ctx context.Context, s *service.Service, args []string) {
	if len(args) != 3 || args[0] != "run" || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[2]) == "" {
		fatal(fmt.Errorf("usage: gpt-tunnel query run <project-id> <dsl>"))
	}
	result, err := s.QueryRun(ctx, service.QueryRunInput{ProjectID: args[1], DSL: args[2]})
	if err != nil {
		fatal(err)
	}
	output(result)
}
