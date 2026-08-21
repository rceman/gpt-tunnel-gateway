package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func work(ctx context.Context, s *service.Service, args []string) {
	if len(args) == 0 || (args[0] != "checkpoint" && args[0] != "status") {
		fatalf("usage: gpt-tunnel work {checkpoint|status} --project PROJECT_ID")
	}
	projectID := ""
	for i := 1; i < len(args); i++ {
		if args[i] != "--project" || i+1 >= len(args) {
			fatalf("unexpected work progress argument %q", args[i])
		}
		projectID = args[i+1]
		i++
	}
	if projectID == "" {
		fatalf("--project is required")
	}
	input := service.WorkCheckpointInput{Root: mustWorkingDirectory(), ProjectID: projectID}
	if args[0] == "status" {
		status, err := s.WorkCheckpointStatus(ctx, input)
		if err != nil {
			fatal(err)
		}
		output(status)
		return
	}
	receipt, err := s.WorkCheckpoint(ctx, input)
	output(receipt)
	if err != nil || receipt.Status != "completed" {
		if err != nil {
			fmt.Fprintln(os.Stderr, "gpt-tunnel:", err)
		}
		os.Exit(1)
	}
}
