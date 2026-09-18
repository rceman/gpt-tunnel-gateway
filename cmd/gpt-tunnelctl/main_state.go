package main

import (
	"context"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func stateCommand(ctx context.Context, c config.Config) {
	if len(os.Args) < 3 {
		usage()
	}
	s := service.New(c)
	switch os.Args[2] {
	case "check":
		result, err := s.StateCheck(ctx)
		if err != nil {
			fatal(err)
		}
		output(result)
		if !result.Valid {
			os.Exit(1)
		}
	case "repair":
		if len(os.Args) < 4 || (os.Args[3] != "--dry-run" && os.Args[3] != "--apply") {
			usage()
		}
		result, err := s.StateRepair(ctx, os.Args[3] == "--apply")
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}
