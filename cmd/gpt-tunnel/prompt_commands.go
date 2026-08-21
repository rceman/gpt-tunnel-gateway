package main

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func prompt(ctx context.Context, s *service.Service, args []string) {
	if len(args) != 1 {
		fatal(fmt.Errorf("usage: gpt-tunnel prompt <PMT-ID>"))
	}
	result, err := s.PMTReadCLI(ctx, args[0])
	if err != nil {
		fatal(err)
	}
	output(result)
}
