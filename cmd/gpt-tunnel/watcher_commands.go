package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func watcher(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	switch args[0] {
	case "start":
		if len(args) != 2 {
			usage()
		}
		v, err := s.WatcherStart(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		output(v)
	case "stop":
		if len(args) != 2 {
			usage()
		}
		v, err := s.WatcherStop(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		output(v)
	case "watch":
		lines := 0
		if len(args) > 2 {
			if len(args) != 4 || args[2] != "--lines" {
				usage()
			}
			var err error
			lines, err = strconv.Atoi(args[3])
			if err != nil {
				fatal(fmt.Errorf("invalid watcher tail bound"))
			}
		}
		v, err := s.WatcherObserve(ctx, service.WatcherObserveInput{ProjectID: args[1], Lines: lines})
		if err != nil {
			fatal(err)
		}
		output(v)
	case "status":
		if len(args) != 2 {
			usage()
		}
		v, err := s.WatcherStatus(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		output(v)
	case "guide":
		if len(args) == 2 {
			v, err := s.WatcherGuideRead(ctx, args[1])
			if err != nil {
				fatal(err)
			}
			output(v)
			return
		}
		if len(args) != 5 || args[2] != "update" || args[3] != "--file" || args[4] == "" {
			usage()
		}
		var in service.WatcherGuideUpdateInput
		readFile(args[4], &in)
		if in.ProjectID == "" {
			in.ProjectID = args[1]
		}
		v, err := s.WatcherGuideUpdate(ctx, in)
		if err != nil {
			fatal(err)
		}
		output(v)
	default:
		usage()
	}
}
