package main

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func session(ctx context.Context, s *service.Service, args []string) {
	if len(args) != 2 || args[0] != "token" || args[1] == "" {
		usage()
	}
	token, err := sessionToken(ctx, s, args[1])
	if err != nil {
		fatal(err)
	}
	fmt.Println(token)
}

func sessionToken(ctx context.Context, s *service.Service, projectCode string) (string, error) {
	db, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		return "", fmt.Errorf("open Local durability for session token: %w", err)
	}
	defer db.Close()
	s.Durability = db
	return s.SessionBootstrapToken(ctx, projectCode)
}
