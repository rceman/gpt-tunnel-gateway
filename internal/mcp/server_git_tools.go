package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func addGitTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	collectionLimit := integer("Maximum collection items", 1, service.MaxPublicCollectionLimit)
	collectionLimit["default"] = service.DefaultPublicCollectionLimit
	projectConfig := func(raw json.RawMessage) (string, config.ProjectConfig, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return "", config.ProjectConfig{}, e
		}
		p, err := s.Service.EffectiveProjectConfig(id)
		if err != nil {
			return "", config.ProjectConfig{}, err
		}
		return id, p, nil
	}
	add("git_refresh", "Refresh managed read-only mirror from project remote.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		e = s.Service.Git.Refresh(ctx, p)
		return map[string]any{"project_id": id, "refreshed": e == nil}, e
	})
	add("git_refs", "List bounded local, remote, and tag refs with deterministic continuation.", obj(map[string]any{"project_id": str("Project identifier"), "limit": collectionLimit, "cursor": str("Opaque continuation cursor")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			ProjectID string `json:"project_id"`
			Limit     int    `json:"limit,omitempty"`
			Cursor    string `json:"cursor,omitempty"`
		}
		if e := decode(raw, &args); e != nil {
			return nil, e
		}
		p, e := s.Service.EffectiveProjectConfig(args.ProjectID)
		if e != nil {
			return nil, e
		}
		limit, e := service.PublicCollectionLimit(args.Limit, s.Service.Config.MaxListItems)
		if e != nil {
			return nil, e
		}
		v, page, e := s.Service.Git.RefsPage(ctx, p, limit, args.Cursor)
		return map[string]any{"refs": v, "next_cursor": page.NextCursor, "has_more": page.HasMore}, e
	})
	logLimit := integer("Maximum commits", 1, service.MaxPublicCollectionLimit)
	logLimit["default"] = service.DefaultPublicCollectionLimit
	add("git_log", "Read bounded commit history at a revision with deterministic continuation.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref"), "limit": logLimit, "cursor": str("Opaque continuation cursor")}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			ProjectID string `json:"project_id"`
			Revision  string `json:"revision"`
			Limit     int    `json:"limit,omitempty"`
			Cursor    string `json:"cursor,omitempty"`
		}
		if e := decode(raw, &args); e != nil {
			return nil, e
		}
		p, e := s.Service.EffectiveProjectConfig(args.ProjectID)
		if e != nil {
			return nil, e
		}
		limit, e := service.PublicCollectionLimit(args.Limit, s.Service.Config.MaxListItems)
		if e != nil {
			return nil, e
		}
		v, page, e := s.Service.Git.LogPage(ctx, p, args.Revision, limit, args.Cursor)
		return map[string]any{"commits": v, "next_cursor": page.NextCursor, "has_more": page.HasMore}, e
	})
	add("git_show", "Show bounded commit metadata, summary, and stat.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref")}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		rev, e := getString(raw, "revision")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Show(ctx, p, rev)
		return map[string]any{"text": v}, e
	})
	add("git_tree", "List bounded files at a revision with deterministic continuation.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref"), "path": str("Optional relative path"), "limit": collectionLimit, "cursor": str("Opaque continuation cursor")}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args struct {
			ProjectID string `json:"project_id"`
			Revision  string `json:"revision"`
			Path      string `json:"path,omitempty"`
			Limit     int    `json:"limit,omitempty"`
			Cursor    string `json:"cursor,omitempty"`
		}
		if e := decode(raw, &args); e != nil {
			return nil, e
		}
		p, e := s.Service.EffectiveProjectConfig(args.ProjectID)
		if e != nil {
			return nil, e
		}
		limit, e := service.PublicCollectionLimit(args.Limit, s.Service.Config.MaxListItems)
		if e != nil {
			return nil, e
		}
		v, page, e := s.Service.Git.TreePage(ctx, p, args.Revision, args.Path, limit, args.Cursor)
		return map[string]any{"paths": v, "next_cursor": page.NextCursor, "has_more": page.HasMore}, e
	})
	add("git_read_file", "Read a UTF-8 file at any revision.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref"), "path": str("Relative file path")}, "project_id", "revision", "path"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		rev, e := getString(raw, "revision")
		if e != nil {
			return nil, e
		}
		path, e := getString(raw, "path")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.ReadFile(ctx, p, rev, path)
		return map[string]any{"path": path, "revision": rev, "content": v}, e
	})
	add("git_diff", "Read bounded diff between two revisions.", obj(map[string]any{"project_id": str("Project identifier"), "from_revision": str("Base revision"), "to_revision": str("Target revision"), "paths": array(str("Optional relative path"))}, "project_id", "from_revision", "to_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		var in struct {
			ProjectID string   `json:"project_id"`
			From      string   `json:"from_revision"`
			To        string   `json:"to_revision"`
			Paths     []string `json:"paths"`
		}
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Diff(ctx, p, in.From, in.To, in.Paths)
		return map[string]any{"diff": v}, e
	})
	add("git_compare", "Compare divergence and merge base.", obj(map[string]any{"project_id": str("Project identifier"), "left": str("Left revision"), "right": str("Right revision")}, "project_id", "left", "right"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		left, e := getString(raw, "left")
		if e != nil {
			return nil, e
		}
		right, e := getString(raw, "right")
		if e != nil {
			return nil, e
		}
		return s.Service.Git.Compare(ctx, p, left, right)
	})
	add("git_merge_base", "Find merge base.", obj(map[string]any{"project_id": str("Project identifier"), "left": str("Left revision"), "right": str("Right revision")}, "project_id", "left", "right"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		left, e := getString(raw, "left")
		if e != nil {
			return nil, e
		}
		right, e := getString(raw, "right")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.MergeBase(ctx, p, left, right)
		return map[string]any{"merge_base": v}, e
	})
	add("git_worktree_status", "Read current local worktree state.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		return s.Service.Git.WorktreeStatus(ctx, p)
	})
	add("git_worktree_diff", "Read unstaged or staged local worktree diff.", obj(map[string]any{"project_id": str("Project identifier"), "staged": map[string]any{"type": "boolean"}}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		staged, _ := m["staged"].(bool)
		v, e := s.Service.Git.WorktreeDiff(ctx, p, staged)
		return map[string]any{"diff": v, "staged": staged}, e
	})
}
