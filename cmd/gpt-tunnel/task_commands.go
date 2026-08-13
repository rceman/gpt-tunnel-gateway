package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func task(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "create":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.TaskCreateInput
		readFile(f, &in)
		t, r, e := s.TaskCreate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"task": t, "operation": r})
	case "list":
		require(args, 2)
		v, e := s.TaskList(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"tasks": v})
	case "read":
		require(args, 2)
		t, e := s.TaskAuthoringFind(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(t)
	case "supersede":
		require(args, 2)
		f, _ := fileFlag("--file", args[2:])
		if f == "" {
			usage()
		}
		var in service.TaskCreateInput
		readFile(f, &in)
		t, r, e := s.TaskSupersede(ctx, args[1], in)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"task": t, "operation": r})
	default:
		usage()
	}
}
