package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func operator(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "record":
		f, rest := fileFlag("--file", args[1:])
		ex, rest := expected(rest)
		if f == "" || len(rest) != 0 {
			usage()
		}
		var in service.OperatorRecordInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		event, operation, err := s.OperatorRecord(ctx, in)
		if err != nil {
			fatal(err)
		}
		output(map[string]any{"event": event, "operation": operation})
	case "history":
		require(args, 2)
		in := service.OperatorHistoryInput{ProjectID: args[1]}
		seen := map[string]bool{}
		for i := 2; i < len(args); {
			if i+1 >= len(args) || seen[args[i]] {
				usage()
			}
			seen[args[i]] = true
			switch args[i] {
			case "--after-event-id":
				in.AfterEventID = args[i+1]
			case "--kind":
				in.Kind = model.OperatorJournalKind(args[i+1])
			case "--limit":
				value, err := strconv.Atoi(args[i+1])
				if err != nil {
					fatal(fmt.Errorf("invalid operator history limit"))
				}
				in.Limit = value
			default:
				usage()
			}
			i += 2
		}
		result, err := s.OperatorHistory(ctx, in)
		if err != nil {
			fatal(err)
		}
		output(result)
	case "checkpoint":
		f, rest := fileFlag("--file", args[1:])
		ex, rest := expected(rest)
		if f == "" || len(rest) != 0 {
			usage()
		}
		var in service.OperatorCheckpointInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		event, operation, err := s.OperatorCheckpoint(ctx, in)
		if err != nil {
			fatal(err)
		}
		output(map[string]any{"event": event, "operation": operation})
	default:
		usage()
	}
}
