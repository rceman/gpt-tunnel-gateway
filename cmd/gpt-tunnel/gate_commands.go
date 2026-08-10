package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rceman/gpt-tunnel-gateway/internal/gates"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func gate(ctx context.Context, s *service.Service, name string, args []string) {
	if len(args) != 0 {
		usage()
	}
	if name != "format" && name != "check" && name != "test" {
		usage()
	}
	if name == "format" {
		if err := gates.Format(ctx, mustWorkingDirectory()); err != nil {
			fatal(err)
		}
		fmt.Println("OK format")
		return
	}
	if name == "test" {
		if err := s.ExecuteCanonicalTestGate(ctx, mustWorkingDirectory()); err != nil {
			fatal(err)
		}
		fmt.Println("OK test")
		return
	}
	results, err := gates.NewExecutor().Execute(ctx, mustWorkingDirectory(), []string{name})
	if err != nil {
		fatal(err)
	}
	if len(results) == 1 && results[0].ExitCode == 0 {
		fmt.Printf("OK %s\n", name)
		return
	}
	fatal(fmt.Errorf("gate %s failed", name))
}

func mustWorkingDirectory() string {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	return root
}
