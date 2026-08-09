package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func taskReviewReportCLI(ctx context.Context, s *service.Service, args []string) {
	if len(args) < 1 {
		usage()
	}
	switch args[0] {
	case "start":
		if len(args) != 3 {
			usage()
		}
		v, err := s.TaskReviewReportStart(ctx, args[1], args[2])
		if err != nil {
			fatal(err)
		}
		output(v)
	case "update":
		if len(args) < 6 {
			usage()
		}
		revision, file, seenRevision, seenFile := 0, "", false, false
		for i := 4; i < len(args); {
			if i+1 >= len(args) {
				usage()
			}
			switch args[i] {
			case "--revision":
				if seenRevision {
					usage()
				}
				value, err := strconv.Atoi(args[i+1])
				if err != nil || value < 1 {
					fatal(fmt.Errorf("invalid draft revision"))
				}
				revision, seenRevision = value, true
			case "--file":
				if seenFile || args[i+1] == "" {
					usage()
				}
				file, seenFile = args[i+1], true
			default:
				usage()
			}
			i += 2
		}
		if !seenRevision || !seenFile {
			usage()
		}
		payload, err := os.ReadFile(file)
		if err != nil {
			fatal(err)
		}
		v, err := s.TaskReviewReportSectionUpdate(ctx, service.TaskReviewReportSectionUpdateInput{TaskID: args[1], RunID: args[2], SectionID: args[3], ExpectedDraftRevision: revision, Payload: payload})
		if err != nil {
			fatal(err)
		}
		output(v)
	case "validate":
		if len(args) != 3 {
			usage()
		}
		v, err := s.TaskReviewReportValidate(ctx, args[1], args[2])
		if err != nil {
			fatal(err)
		}
		output(v)
	case "finalize":
		if len(args) < 4 {
			usage()
		}
		revision, expectedHub, seenRevision, seenExpected := 0, "", false, false
		for i := 3; i < len(args); {
			if i+1 >= len(args) {
				usage()
			}
			switch args[i] {
			case "--revision":
				if seenRevision {
					usage()
				}
				value, err := strconv.Atoi(args[i+1])
				if err != nil || value < 1 {
					fatal(fmt.Errorf("invalid draft revision"))
				}
				revision, seenRevision = value, true
			case "--expected-hub-revision":
				if seenExpected || args[i+1] == "" {
					usage()
				}
				expectedHub, seenExpected = args[i+1], true
			default:
				usage()
			}
			i += 2
		}
		if !seenRevision {
			usage()
		}
		report, operation, err := s.TaskReviewReportFinalize(ctx, service.TaskReviewReportFinalizeInput{TaskID: args[1], RunID: args[2], ExpectedDraftRevision: revision, WriteOptions: service.WriteOptions{ExpectedHubRevision: expectedHub}})
		if err != nil {
			fatal(err)
		}
		output(map[string]any{"report": report, "operation": operation})
	default:
		usage()
	}
}
