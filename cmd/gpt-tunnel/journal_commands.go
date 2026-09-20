package main

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
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
		db, err := sqlitestore.Open(s.Config.StateDir)
		if err != nil {
			fatal(fmt.Errorf("open Shared/Local durability for journal migration: %w", err))
		}
		defer db.Close()
		s.Durability = db
		result, err := s.JournalMigrate(ctx, in)
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}
