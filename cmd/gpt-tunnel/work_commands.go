package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type operatorWorkCLIRequest struct {
	Root      string `json:"root"`
	ProjectID string `json:"project_id"`
}

func work(ctx context.Context, s *service.Service, args []string) {
	if len(args) == 0 || (args[0] != "checkpoint" && args[0] != "status") {
		fatalf("usage: gpt-tunnel work {checkpoint|status} --project PROJECT_ID")
	}
	projectID := ""
	for i := 1; i < len(args); i++ {
		if args[i] != "--project" || i+1 >= len(args) {
			fatalf("unexpected work checkpoint argument %q", args[i])
		}
		projectID = args[i+1]
		i++
	}
	if projectID == "" {
		fatalf("--project is required")
	}
	input := operatorWorkCLIRequest{
		Root:      mustWorkingDirectory(),
		ProjectID: projectID,
	}
	if args[0] == "status" {
		status, err := operatorCLIRequest[service.WorkCheckpointStatus](ctx, s.Config, "/operator/work/status", input)
		if err != nil {
			fatal(err)
		}
		output(status)
		return
	}
	receipt, err := operatorCLIRequest[service.WorkProgressReceipt](ctx, s.Config, "/operator/work/checkpoint", input)
	output(receipt)
	if err == nil && receipt.Status == "running" {
		fmt.Fprintf(os.Stderr, "gpt-tunnel: checkpoint %s is running; use `gpt-tunnel work status --project %s` for progress\n", receipt.OperationID, projectID)
		return
	}
	if err != nil || receipt.Status != "completed" {
		if err != nil {
			fmt.Fprintln(os.Stderr, "gpt-tunnel:", err)
		}
		os.Exit(1)
	}
}
