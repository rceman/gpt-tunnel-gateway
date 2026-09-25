package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type operatorADRReadCLIRequest struct {
	ProjectID string `json:"project_id"`
	Key       string `json:"key"`
}

func adr(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "list":
		require(args, 2)
		v, err := operatorCLIRequest[[]model.ADR](ctx, s.Config, "/operator/adr/list", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(map[string]any{"items": v})
	case "read":
		require(args, 3)
		v, err := operatorCLIRequest[model.ADR](ctx, s.Config, "/operator/adr/read", operatorADRReadCLIRequest{
			ProjectID: args[1],
			Key:       args[2],
		})
		if err != nil {
			fatal(err)
		}
		output(v)
	case "create":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.ADRCreateInput
		readFile(f, &in)
		v, err := operatorCLIRequest[service.OperationResult](ctx, s.Config, "/operator/adr/create", in)
		if err != nil {
			fatal(err)
		}
		output(v)
	default:
		usage()
	}
}
