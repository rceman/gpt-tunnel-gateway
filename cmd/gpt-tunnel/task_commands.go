package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func task(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "work":
		require(args, 2)
		result, e := s.TaskWork(ctx, service.TaskWorkInput{TaskID: args[1]})
		if e != nil {
			fatal(e)
		}
		output(result)
	case "finalize":
		require(args, 2)
		result, e := s.TaskFinalize(ctx, service.TaskFinalizeInput{TaskID: args[1]})
		if e != nil {
			fatal(e)
		}
		output(result)
	default:
		usage()
	}
}
