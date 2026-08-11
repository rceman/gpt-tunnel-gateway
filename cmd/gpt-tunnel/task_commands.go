package main

import (
	"context"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func task(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "create":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.TaskCreateInput
		readFile(f, &in)
		t, r, e := s.TaskCreate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"task": t, "operation": r})
	case "list":
		require(args, 2)
		v, e := s.TaskList(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"tasks": v})
	case "train-create":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.TaskTrainCreateInput
		readFile(f, &in)
		train, operation, e := s.TaskTrainCreate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"train": train, "operation": operation})
	case "train-status":
		require(args, 2)
		v, e := s.TaskTrainStatus(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "train-poll":
		require(args, 2)
		cursor := ""
		if len(args) == 4 && args[2] == "--cursor" {
			cursor = args[3]
		} else if len(args) != 2 {
			usage()
		}
		v, e := s.TaskTrainPoll(ctx, service.TaskTrainPollInput{ProjectID: args[1], Cursor: cursor})
		if e != nil {
			fatal(e)
		}
		output(v)
	case "read":
		require(args, 2)
		v, e := s.TaskRead(ctx, args[1])
		if e != nil {
			t, e2 := s.TaskReadRecord(ctx, args[1])
			if e2 != nil {
				fatal(e)
			}
			output(t)
			return
		}
		fmt.Print(v.Text)
	case "review-report":
		taskReviewReportCLI(ctx, s, args[1:])
	case "report-read", "review-report-read":
		if len(args) < 2 || len(args) > 3 {
			usage()
		}
		runID := ""
		if len(args) == 3 {
			runID = args[2]
		}
		v, e := s.TaskReportRead(ctx, args[1], runID)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "dispatch":
		require(args, 2)
		ex, _ := expected(args[2:])
		r, o, e := s.TaskDispatch(ctx, service.DispatchInput{TaskID: args[1], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"run": service.PublicRunView(r), "operation": o})
	case "supersede":
		require(args, 2)
		f, _ := fileFlag("--file", args[2:])
		if f == "" {
			usage()
		}
		var in service.TaskCreateInput
		readFile(f, &in)
		t, r, e := s.TaskSupersede(ctx, args[1], in)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"task": t, "operation": r})
	case "cancel":
		require(args, 2)
		ex, _ := expected(args[2:])
		v, e := s.TaskCancel(ctx, args[1], ex)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "mark-merge-ready":
		require(args, 2)
		ex, rest := expected(args[2:])
		if len(rest) != 0 {
			usage()
		}
		v, e := s.TaskMarkMergeReady(ctx, service.TaskMarkMergeReadyInput{TaskID: args[1], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if e != nil {
			fatal(e)
		}
		output(v)
	case "defer":
		require(args, 2)
		reason := ""
		rest := make([]string, 0, len(args)-2)
		for i := 2; i < len(args); i++ {
			if args[i] == "--reason" {
				if reason != "" || i+1 >= len(args) {
					usage()
				}
				reason = args[i+1]
				i++
				continue
			}
			rest = append(rest, args[i])
		}
		ex, rest := expected(rest)
		if reason == "" || len(rest) != 0 {
			usage()
		}
		v, e := s.TaskDefer(ctx, service.TaskDeferInput{TaskID: args[1], Reason: reason, WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if e != nil {
			fatal(e)
		}
		output(v)
	case "mark-merged":
		require(args, 3)
		ex, rest := expected(args[3:])
		if len(rest) != 0 {
			usage()
		}
		v, e := s.TaskMarkMerged(ctx, service.TaskMarkMergedInput{TaskID: args[1], IntegrationHead: args[2], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if e != nil {
			fatal(e)
		}
		output(v)
	default:
		usage()
	}
}
