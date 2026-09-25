package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type operatorProjectIdentifiersResult struct {
	Identifiers model.ProjectIdentifiers `json:"identifiers"`
	Operation   service.OperationResult  `json:"operation"`
}

func project(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "onboard":
		input, err := parseProjectOnboardArgs(args[1:])
		if err != nil {
			fatal(err)
		}
		if input.Root == "" {
			input.Root = mustWorkingDirectory()
		} else {
			input.Root, err = filepath.Abs(input.Root)
			if err != nil {
				fatal(err)
			}
		}
		result, err := operatorCLIRequest[service.ProjectOnboardResult](ctx, s.Config, "/operator/project/onboard", input)
		if err != nil {
			fatal(err)
		}
		output(renderProjectOnboard(result))
	case "token":
		if len(args) > 2 {
			usage()
		}
		projectCode := ""
		if len(args) == 2 {
			projectCode = args[1]
		}
		token, err := projectTokenGatewayCall(ctx, s, projectCode)
		if err != nil {
			fatal(err)
		}
		fmt.Println(token)
	case "list":
		projects, err := operatorCLIRequest[[]model.Project](ctx, s.Config, "/operator/project/list", struct{}{})
		if err != nil {
			fatal(err)
		}
		output(map[string]any{"projects": projects})
	case "read":
		require(args, 2)
		project, err := operatorCLIRequest[model.Project](ctx, s.Config, "/operator/project/read", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(project)
	case "update":
		if len(args) != 4 || args[2] != "--project-code" {
			usage()
		}
		result, err := operatorCLIRequest[service.ProjectUpdateResult](ctx, s.Config, "/operator/project/update", service.ProjectUpdateInput{ProjectID: args[1], ProjectCode: args[3]})
		if err != nil {
			fatal(err)
		}
		output(result)
	case "identifiers-read":
		require(args, 2)
		if len(args) != 2 {
			usage()
		}
		identifiers, err := operatorCLIRequest[model.ProjectIdentifiers](ctx, s.Config, "/operator/project/identifiers-read", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(identifiers)
	case "identifiers-adopt":
		require(args, 3)
		ex, err := expectedStrict(args[3:])
		if err != nil {
			usage()
		}
		result, err := operatorCLIRequest[operatorProjectIdentifiersResult](ctx, s.Config, "/operator/project/identifiers-adopt", service.ProjectIdentifiersAdoptInput{ProjectID: args[1], ProjectCode: args[2], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if err != nil {
			fatal(err)
		}
		output(result)
	case "workflow-policy-read":
		require(args, 2)
		if len(args) != 2 {
			usage()
		}
		policy, err := operatorCLIRequest[model.ProjectWorkflowPolicy](ctx, s.Config, "/operator/project/workflow-policy-read", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(policy)
	case "status":
		require(args, 2)
		status, err := operatorCLIRequest[service.ProjectStatus](ctx, s.Config, "/operator/project/status", map[string]string{"project_id": args[1]})
		if err != nil {
			fatal(err)
		}
		output(status)
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
		result, err := operatorCLIRequest[service.OperationResult](ctx, s.Config, "/operator/project/register", in)
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}
