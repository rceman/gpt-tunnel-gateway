package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func admin(ctx context.Context, c config.Config, args []string) {
	require(args, 2)
	if args[0] != "session" {
		usage()
	}
	db, err := sqlitestore.Open(c.StateDir)
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	s := service.NewWithDurabilityDeferredWorkers(c, db)
	s.ConfigPath = config.DefaultPath()
	switch args[1] {
	case "mint":
		label, rest := stringFlag("--label", args[2:])
		if len(rest) != 0 {
			usage()
		}
		result, err := s.AdminSessionMint(optionalString(label))
		if err != nil {
			fatal(err)
		}
		output(result)
	case "revoke":
		if len(args) != 3 {
			usage()
		}
		result, err := s.AdminSessionRevoke(args[2])
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}

func stringFlag(name string, args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1], append(args[:i], args[i+2:]...)
		}
	}
	return "", args
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
