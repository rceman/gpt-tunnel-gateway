package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func adr(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "list":
		require(args, 2)
		v, e := s.ADRList(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"adrs": v})
	case "read":
		require(args, 3)
		v, e := s.ADRRead(ctx, args[1], args[2])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "create":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.ADRCreateInput
		readFile(f, &in)
		v, e := s.ADRCreate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	default:
		usage()
	}
}
