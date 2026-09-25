package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func journal(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "migrate":
		f, rest := fileFlag("--file", args[1:])
		if f == "" || len(rest) != 0 {
			usage()
		}
		var in service.JournalMigrateInput
		readFile(f, &in)
		result, err := operatorCLIRequest[service.JournalMigrateResult](ctx, s.Config, "/operator/journal/migrate", in)
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}
