package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func run(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "list":
		require(args, 2)
		v, e := s.RunList(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		public := make([]service.PublicRun, 0, len(v))
		for _, run := range v {
			public = append(public, service.PublicRunView(run))
		}
		output(map[string]any{"runs": public})
	case "read", "status":
		require(args, 2)
		v, e := s.RunRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(service.PublicRunView(v))
	case "report":
		require(args, 2)
		v, e := s.RunReport(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "review-snapshot":
		require(args, 2)
		v, e := s.RunReviewSnapshot(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "agent-tail":
		require(args, 2)
		lines := 4
		if len(args) > 2 {
			if len(args) != 4 || args[2] != "--lines" {
				usage()
			}
			value, e := strconv.Atoi(args[3])
			if e != nil {
				fatal(fmt.Errorf("invalid tail line count"))
			}
			lines = value
		}
		v, e := s.RunAgentTail(ctx, args[1], lines)
		if e != nil {
			fatal(e)
		}
		fmt.Println(strings.TrimRight(v, "\r\n"))
	case "resume":
		require(args, 2)
		v, e := s.RunResume(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "sweep":
		v, e := s.RunSweep(ctx)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "cancel":
		require(args, 2)
		ex, _ := expected(args[2:])
		v, e := s.RunCancel(ctx, args[1], ex)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "cancel-acknowledge-no-mutation":
		require(args, 2)
		ex, e := expectedStrict(args[2:])
		if e != nil {
			usage()
		}
		v, e := s.RunCancelAcknowledgeNoMutation(ctx, args[1], ex)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "finalize":
		require(args, 2)
		fs := flag.NewFlagSet("run finalize", flag.ExitOnError)
		summary := fs.String("summary", "", "bounded Agent-owned completion summary")
		ex := fs.String("expected-hub-revision", "", "optimistic revision")
		_ = fs.Parse(args[2:])
		report, result, e := s.RunFinalize(ctx, service.FinalizeInput{RunID: args[1], Summary: *summary, WriteOptions: service.WriteOptions{ExpectedHubRevision: *ex}})
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"status": "TASK_FINALIZED", "report": report, "operation": result})
	case "write-completion":
		require(args, 2)
		cf, parseErr := completionFileStrict(args[2:])
		if parseErr != nil {
			usage()
		}
		result, e := s.RunWriteCompletion(ctx, service.CompletionWriteInput{RunID: args[1], CompletionFile: cf})
		if e != nil {
			fatal(e)
		}
		output(result)
	default:
		usage()
	}
}
