package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func work(ctx context.Context, s *service.Service, args []string) {
	if len(args) == 0 || args[0] != "progress" {
		fatalf("usage: gpt-tunnel work progress [--project PROJECT_ID]")
	}
	input := service.WorkProgressInput{Root: mustWorkingDirectory()}
	for i := 1; i < len(args); i++ {
		if args[i] != "--project" || i+1 >= len(args) {
			fatalf("unexpected work progress argument %q", args[i])
		}
		input.ProjectID = args[i+1]
		i++
	}
	receipt, err := s.WorkProgress(ctx, input)
	output(receipt)
	if err != nil || receipt.Status != "completed" {
		if err != nil {
			fmt.Fprintln(os.Stderr, "gpt-tunnel:", err)
		}
		os.Exit(1)
	}
}
