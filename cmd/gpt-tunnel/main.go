package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

var version = "0.2.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	if os.Args[1] == "version" || os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	c, err := config.Load("")
	if err != nil {
		fatal(err)
	}
	s := service.New(c)
	ctx := context.Background()
	group := os.Args[1]
	args := os.Args[2:]
	switch group {
	case "project":
		project(ctx, s, args)
	case "plan":
		plan(ctx, s, args)
	case "adr":
		adr(ctx, s, args)
	case "task":
		task(ctx, s, args)
	case "run":
		run(ctx, s, args)
	case "git":
		gitcmd(ctx, s, args)
	default:
		usage()
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnel {project|plan|adr|task|run|git} <command> [args]")
	os.Exit(2)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "gpt-tunnel:", err); os.Exit(1) }
func output(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(data))
}
func readFile(path string, out any) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		fatal(err)
	}
}
func fileFlag(name string, args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1], append(args[:i], args[i+2:]...)
		}
	}
	return "", args
}
func expected(args []string) (string, []string) { return fileFlag("--expected-hub-revision", args) }
func require(args []string, n int) {
	if len(args) < n {
		usage()
	}
}
func project(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
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
func plan(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "read":
		require(args, 2)
		v, e := s.PlanRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "history":
		require(args, 2)
		limit := 50
		if len(args) > 2 {
			limit, _ = strconv.Atoi(args[2])
		}
		v, e := s.PlanHistory(ctx, args[1], limit)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"history": v})
	case "update":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.PlanUpdateInput
		readFile(f, &in)
		v, e := s.PlanUpdate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	default:
		usage()
	}
}
func adr(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "list":
		require(args, 2)
		v, e := s.ADRList(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"adrs": v})
	case "read":
		require(args, 3)
		v, e := s.ADRRead(ctx, args[1], args[2])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "create":
		f, _ := fileFlag("--file", args[1:])
		if f == "" {
			usage()
		}
		var in service.ADRCreateInput
		readFile(f, &in)
		v, e := s.ADRCreate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	default:
		usage()
	}
}
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
	case "dispatch":
		require(args, 2)
		ex, _ := expected(args[2:])
		r, o, e := s.TaskDispatch(ctx, service.DispatchInput{TaskID: args[1], WriteOptions: service.WriteOptions{ExpectedHubRevision: ex}})
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"run": r, "operation": o})
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
	default:
		usage()
	}
}
func run(ctx context.Context, s *service.Service, args []string) {
	require(args, 1)
	switch args[0] {
	case "list":
		require(args, 2)
		v, e := s.RunList(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"runs": v})
	case "read", "status":
		require(args, 2)
		v, e := s.RunRead(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "report":
		require(args, 2)
		v, e := s.RunReport(ctx, args[1])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "evidence":
		require(args, 2)
		v, e := s.RunEvidence(ctx, args[1])
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
	case "finalize":
		require(args, 2)
		fs := flag.NewFlagSet("run finalize", flag.ExitOnError)
		rf := fs.String("result-file", "", "agent result JSON")
		ef := fs.String("evidence-file", "", "evidence JSON")
		ex := fs.String("expected-hub-revision", "", "optimistic revision")
		_ = fs.Parse(args[2:])
		report, result, e := s.RunFinalize(ctx, service.FinalizeInput{RunID: args[1], ResultFile: *rf, EvidenceFile: *ef, WriteOptions: service.WriteOptions{ExpectedHubRevision: *ex}})
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"status": "TASK_FINALIZED", "report": report, "operation": result})
	default:
		usage()
	}
}
func gitcmd(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	p, ok := s.Config.Projects[args[1]]
	if !ok {
		fatal(fmt.Errorf("unknown project %q", args[1]))
	}
	switch args[0] {
	case "refresh":
		if e := s.Git.Refresh(ctx, p); e != nil {
			fatal(e)
		}
		output(map[string]any{"refreshed": true})
	case "refs":
		v, e := s.Git.Refs(ctx, p)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"refs": v})
	case "log":
		require(args, 3)
		limit := 50
		if len(args) > 3 {
			limit, _ = strconv.Atoi(args[3])
		}
		v, e := s.Git.Log(ctx, p, args[2], limit)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"commits": v})
	case "show":
		require(args, 3)
		v, e := s.Git.Show(ctx, p, args[2])
		if e != nil {
			fatal(e)
		}
		fmt.Print(v)
	case "tree":
		require(args, 3)
		path := ""
		if len(args) > 3 {
			path = args[3]
		}
		v, e := s.Git.Tree(ctx, p, args[2], path)
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"paths": v})
	case "read-file":
		require(args, 4)
		v, e := s.Git.ReadFile(ctx, p, args[2], args[3])
		if e != nil {
			fatal(e)
		}
		fmt.Print(v)
	case "diff":
		require(args, 4)
		v, e := s.Git.Diff(ctx, p, args[2], args[3], args[4:])
		if e != nil {
			fatal(e)
		}
		fmt.Print(v)
	case "compare":
		require(args, 4)
		v, e := s.Git.Compare(ctx, p, args[2], args[3])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "merge-base":
		require(args, 4)
		v, e := s.Git.MergeBase(ctx, p, args[2], args[3])
		if e != nil {
			fatal(e)
		}
		fmt.Println(v)
	case "worktree-status":
		v, e := s.Git.WorktreeStatus(ctx, p)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "worktree-diff":
		staged := len(args) > 2 && args[2] == "--staged"
		v, e := s.Git.WorktreeDiff(ctx, p, staged)
		if e != nil {
			fatal(e)
		}
		fmt.Print(v)
	default:
		usage()
	}
}
