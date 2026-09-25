package main

import (
	"context"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type operatorPlanHistoryCLIRequest struct {
	ProjectID string `json:"project_id"`
	Limit     int    `json:"limit"`
}

type operatorPlanSectionCLIRequest struct {
	ProjectID string `json:"project_id"`
	SectionID string `json:"section_id"`
}

func plan(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "read":
		require(args, 2)
		v, err := operatorCLIRequest[model.Plan](ctx, s.Config, "/operator/plan/read", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(v)
	case "history":
		require(args, 2)
		limit := 50
		if len(args) > 2 {
			limit, _ = strconv.Atoi(args[2])
		}
		v, err := operatorCLIRequest[[]map[string]string](ctx, s.Config, "/operator/plan/history", operatorPlanHistoryCLIRequest{
			ProjectID: args[1],
			Limit:     limit,
		})
		if err != nil {
			fatal(err)
		}
		output(map[string]any{"history": v})
	case "cutover":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanCutoverInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, err := operatorCLIRequest[service.OperationResult](ctx, s.Config, "/operator/plan/cutover", in)
		if err != nil {
			fatal(err)
		}
		output(v)
	case "update":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.PlanUpdateInput
		readFile(f, &in)
		v, err := operatorCLIRequest[service.OperationResult](ctx, s.Config, "/operator/plan/update", in)
		if err != nil {
			fatal(err)
		}
		output(v)
	case "section-read":
		require(args, 3)
		v, err := operatorCLIRequest[model.PlanSection](ctx, s.Config, "/operator/plan/section-read", operatorPlanSectionCLIRequest{
			ProjectID: args[1],
			SectionID: args[2],
		})
		if err != nil {
			fatal(err)
		}
		output(v)
	case "section-create":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanSectionCreateInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, err := operatorCLIRequest[service.OperationResult](ctx, s.Config, "/operator/plan/section-create", in)
		if err != nil {
			fatal(err)
		}
		output(v)
	case "section-update":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanSectionUpdateInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, err := operatorCLIRequest[service.OperationResult](ctx, s.Config, "/operator/plan/section-update", in)
		if err != nil {
			fatal(err)
		}
		output(v)
	case "render":
		require(args, 2)
		v, err := operatorCLIRequest[model.PlanRender](ctx, s.Config, "/operator/plan/render", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(v)
	default:
		usage()
	}
}
