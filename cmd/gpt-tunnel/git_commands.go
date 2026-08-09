package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func gitcmd(ctx context.Context, s *service.Service, args []string) {
	require(args, 2)
	p, err := s.EffectiveProjectConfig(args[1])
	if err != nil {
		fatal(err)
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
