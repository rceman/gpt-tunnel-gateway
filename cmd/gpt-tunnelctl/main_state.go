package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	case "migrate-train-v2-attempts":
		stateMigrateTrainV2Attempts(ctx, s)
	case "migrate-train-v2-legacy":
		stateMigrateTrainV2Legacy(ctx, s)
	default:
		usage()
	}
}
func stateMigrateTrainV2Legacy(ctx context.Context, s *service.Service) {
	input := service.TrainV2LegacyStateMigrationInput{}
	modeSet, projectSet := false, false
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--dry-run" || os.Args[i] == "--apply" {
			if modeSet {
				usage()
			}
			modeSet = true
			input.Apply = os.Args[i] == "--apply"
			continue
		}
		if i+1 >= len(os.Args) {
			usage()
		}
		value := os.Args[i+1]
		switch os.Args[i] {
		case "--project":
			input.ProjectID, projectSet = value, true
		case "--expected-hub-revision":
			input.ExpectedHubRevision = value
		case "--reason":
			input.Reason = value
		case "--action":
			parts := strings.Split(value, ":")
			if len(parts) != 3 && len(parts) != 4 && len(parts) != 6 {
				fatal(fmt.Errorf("--action requires action:train_id:train_sha256[:integration_sha256[:mutation_id:mutation_sha256]]"))
			}
			action := service.TrainV2LegacyStateMigrationAction{Action: parts[0], TrainID: parts[1], TrainSHA256: parts[2]}
			if len(parts) == 4 {
				action.IntegrationSHA256 = parts[3]
			}
			if len(parts) == 6 {
				action.IntegrationSHA256, action.IntegrationMutationID, action.IntegrationMutationSHA256 = parts[3], parts[4], parts[5]
			}
			input.Actions = append(input.Actions, action)
		default:
			usage()
		}
		i++
	}
	if !modeSet || !projectSet || len(input.Actions) == 0 || (input.Apply && input.ExpectedHubRevision == "") {
		usage()
	}
	result, err := s.TrainV2MigrateLegacyState(ctx, input)
	if err != nil {
		fatal(err)
	}
	output(result)
}
func stateMigrateTrainV2Attempts(ctx context.Context, s *service.Service) {
	input := service.TrainV2AttemptMigrationInput{}
	modeSet, projectSet, trainSet := false, false, false
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--dry-run" || os.Args[i] == "--apply" {
			if modeSet {
				usage()
			}
			modeSet = true
			input.Apply = os.Args[i] == "--apply"
			continue
		}
		if i+1 >= len(os.Args) {
			usage()
		}
		value := os.Args[i+1]
		switch os.Args[i] {
		case "--project":
			input.ProjectID, projectSet = value, true
		case "--train":
			input.TrainID, trainSet = value, true
		case "--expected-hub-revision":
			input.ExpectedHubRevision = value
		default:
			usage()
		}
		i++
	}
	if !modeSet || !projectSet || !trainSet {
		usage()
	}
	if input.Apply && input.ExpectedHubRevision == "" {
		fatal(fmt.Errorf("--expected-hub-revision is required with --apply"))
	}
	result, err := s.TrainV2MigrateAttempts(ctx, input)
	if err != nil {
		fatal(err)
	}
	output(result)
}
