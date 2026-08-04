package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

var version = "0.6.1"

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
	case "agent":
		agent(ctx, s, args)
	case "git":
		gitcmd(ctx, s, args)
	default:
		usage()
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, "usage: gpt-tunnel {project|plan|adr|task|run|agent|git} <command> [args]")
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
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		fatal(fmt.Errorf("trailing JSON content"))
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
func expectedStrict(args []string) (string, error) {
	expectedRevision, rest := expected(args)
	if len(rest) != 0 {
		return "", fmt.Errorf("unexpected run cancellation acknowledgement arguments")
	}
	return expectedRevision, nil
}
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
	case "cutover":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanCutoverInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, e := s.PlanCutover(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
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
	case "section-read":
		require(args, 3)
		v, e := s.PlanSectionRead(ctx, args[1], args[2])
		if e != nil {
			fatal(e)
		}
		output(v)
	case "section-create":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanSectionCreateInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, e := s.PlanSectionCreate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "section-update":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanSectionUpdateInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, e := s.PlanSectionUpdate(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "section-delete":
		f, rest := fileFlag("--file", args[1:])
		ex, _ := expected(rest)
		if f == "" {
			usage()
		}
		var in service.PlanSectionDeleteInput
		readFile(f, &in)
		if ex != "" {
			in.ExpectedHubRevision = ex
		}
		v, e := s.PlanSectionDelete(ctx, in)
		if e != nil {
			fatal(e)
		}
		output(v)
	case "render":
		require(args, 2)
		v, e := s.PlanRender(ctx, args[1])
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
		cf := fs.String("completion-file", "", "completion JSON")
		ex := fs.String("expected-hub-revision", "", "optimistic revision")
		_ = fs.Parse(args[2:])
		report, result, e := s.RunFinalize(ctx, service.FinalizeInput{RunID: args[1], CompletionFile: *cf, WriteOptions: service.WriteOptions{ExpectedHubRevision: *ex}})
		if e != nil {
			fatal(e)
		}
		output(map[string]any{"status": "TASK_FINALIZED", "report": report, "operation": result})
	default:
		usage()
	}
}

func agent(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	switch args[0] {
	case "send":
		if len(args) != 4 || args[2] != "--text" {
			usage()
		}
		v, err := s.AgentSend(ctx, args[1], args[3])
		if err != nil {
			fatal(err)
		}
		output(v)
	case "tail":
		lines, skip := 4, 0
		seenLines, seenSkip := false, false
		for i := 2; i < len(args); {
			if i+1 >= len(args) {
				usage()
			}
			value, err := strconv.Atoi(args[i+1])
			if err != nil {
				fatal(fmt.Errorf("invalid agent tail bound"))
			}
			switch args[i] {
			case "--lines":
				if seenLines {
					usage()
				}
				lines, seenLines = value, true
			case "--skip":
				if seenSkip {
					usage()
				}
				skip, seenSkip = value, true
			default:
				usage()
			}
			i += 2
		}
		v, err := s.AgentTail(ctx, args[1], lines, skip)
		if err != nil {
			fatal(err)
		}
		output(v)
	case "status":
		if len(args) != 2 {
			usage()
		}
		v, err := s.AgentStatus(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		output(v)
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
