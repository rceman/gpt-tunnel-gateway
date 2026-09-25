package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type operatorVerifyCLIRequest struct {
	Root      string   `json:"root"`
	ProjectID string   `json:"project_id,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Packages  []string `json:"packages,omitempty"`
}

type operatorVerifyStatusCLIRequest struct {
	OperationID string `json:"operation_id"`
}

func verify(ctx context.Context, s *service.Service, args []string) {
	if len(args) > 0 && args[0] == "status" {
		if len(args) != 2 {
			fatalf("verify status requires an operation ID")
		}
		receipt, err := operatorCLIRequest[service.VerifyReceipt](ctx, s.Config, "/operator/verify/status", operatorVerifyStatusCLIRequest{OperationID: args[1]})
		output(receipt)
		if err != nil || receipt.Status == "failed" {
			os.Exit(1)
		}
		return
	}
	in := operatorVerifyCLIRequest{
		Root:  mustWorkingDirectory(),
		Scope: "full",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--scope":
			if i+1 >= len(args) {
				fatalf("--scope requires a value")
			}
			in.Scope = args[i+1]
			i++
		case "--project":
			if i+1 >= len(args) {
				fatalf("--project requires a value")
			}
			in.ProjectID = args[i+1]
			i++
		case "--package":
			if i+1 >= len(args) {
				fatalf("--package requires a value")
			}
			in.Packages = append(in.Packages, args[i+1])
			i++
		default:
			fatalf("unexpected verify argument %q", args[i])
		}
	}
	receipt, err := operatorCLIRequest[service.VerifyReceipt](ctx, s.Config, "/operator/verify", in)
	output(receipt)
	if err != nil || receipt.Status != "completed" {
		if err != nil {
			fmt.Fprintln(os.Stderr, "gpt-tunnel:", err)
		}
		os.Exit(1)
	}
}

func fatalf(format string, args ...any) { fatal(fmt.Errorf(format, args...)) }
