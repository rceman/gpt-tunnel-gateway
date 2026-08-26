package main

import (
	"context"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func plan(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "read":
		require(args, 2)
		v, e := s.PlanRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "history":
		require(args, 2)
		limit := 50
		if len(args) > 2 {
			limit, _ = strconv.Atoi(args[2])
		}
		v, e := s.PlanHistory(ctx, args[1], limit)
		if e != nil {
			fatal(e)
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
		v, e := s.PlanCutover(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "update":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.PlanUpdateInput
		readFile(f, &in)
		v, e := s.PlanUpdate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "section-read":
		require(args, 3)
		v, e := s.PlanSectionRead(ctx, args[1], args[2])
		if e != nil {
			fatal(e)
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
		v, e := s.PlanSectionCreate(ctx, in)
		if e != nil {
			fatal(e)
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
		v, e := s.PlanSectionUpdate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "render":
		require(args, 2)
		v, e := s.PlanRender(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	default:
		usage()
	}
}
