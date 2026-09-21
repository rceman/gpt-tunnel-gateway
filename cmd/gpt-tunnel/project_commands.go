package main

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func project(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "onboard":
		input, err := parseProjectOnboardArgs(args[1:])
		if err != nil {
			fatal(err)
		}
		db, err := sqlitestore.Open(s.Config.StateDir)
		if err != nil {
			fatal(fmt.Errorf("open Shared/Local durability for project onboarding: %w", err))
		}
		defer db.Close()
		s.Durability = db
		result, err := s.ProjectOnboard(ctx, input)
		if err != nil {
			fatal(err)
		}
		output(renderProjectOnboard(result))
	case "token":
		if len(args) != 1 {
			usage()
		}
		token, e := projectTokenGatewayCall(ctx, s)
		if e != nil {
			fatal(e)
		}
		fmt.Println(token)
	case "list":
		v, e := s.ProjectList(ctx)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"projects": v})
	case "read":
		require(args, 2)
		v, e := s.ProjectRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "update":
		if len(args) != 4 || args[2] != "--project-code" {
			usage()
		}
		db, e := sqlitestore.Open(s.Config.StateDir)
		if e != nil {
			fatal(fmt.Errorf("open Shared/Local durability for project update: %w", e))
		}
		defer db.Close()
		s.Durability = db
		v, e := s.ProjectUpdate(ctx, service.ProjectUpdateInput{ProjectID: args[1], ProjectCode: args[3]})
		if e != nil {
			fatal(e)
		}
		output(v)
	case "identifiers-read":
		require(args, 2)
		if len(args) != 2 {
			usage()
		}
		v, e := s.ProjectIdentifiersRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "identifiers-adopt":
		require(args, 3)
		ex, e := expectedStrict(args[3:])
		if e != nil {
			usage()
		}
		identifiers, operation, e := s.ProjectIdentifiersAdopt(ctx, service.ProjectIdentifiersAdoptInput{ProjectID: args[1], ProjectCode: args[2], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"identifiers": identifiers, "operation": operation})
	case "workflow-policy-read":
		require(args, 2)
		if len(args) != 2 {
			usage()
		}
		v, e := s.ProjectWorkflowPolicyRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "status":
		require(args, 2)
		v, e := s.ProjectStatus(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "register":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.ProjectRegisterInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, e := s.ProjectRegister(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	default:
		usage()
	}
}
