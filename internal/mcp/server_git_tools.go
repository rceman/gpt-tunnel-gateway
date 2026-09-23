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
	publicRevision := func(description string) map[string]any {
		revision := str(description)
		revision["minLength"], revision["maxLength"] = 1, 512
		revision["not"] = publicGitFingerprintExclusionSchema()
		return revision
	}
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
	add("git_refs", "List bounded local, remote, and tag refs with deterministic continuation through outer call.pagination.next_cursor.", obj(map[string]any{"project_id": str("Project identifier"), "limit": collectionLimit, "cursor": publicServerCursorSchema()}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
		if e != nil {
			return nil, e
		}
		return genericActionPageResult(map[string]any{"refs": v}, page.HasMore, page.NextCursor)
	})
	logLimit := integer("Maximum commits", 1, service.MaxPublicCollectionLimit)
	logLimit["default"] = service.DefaultPublicCollectionLimit
	add("git_log", "Read bounded commit history at a revision with deterministic continuation through outer call.pagination.next_cursor.", obj(map[string]any{"project_id": str("Project identifier"), "revision": publicRevision("Revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "limit": logLimit, "cursor": publicServerCursorSchema()}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
		if e != nil {
			return nil, e
		}
		return genericActionPageResult(map[string]any{"commits": v}, page.HasMore, page.NextCursor)
	})
	add("git_show", "Show bounded commit metadata, summary, and stat.", obj(map[string]any{"project_id": str("Project identifier"), "revision": publicRevision("Revision or ref; commit fingerprints use 8 lowercase hexadecimal characters")}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
	add("git_tree", "List bounded files at a revision with deterministic continuation through outer call.pagination.next_cursor.", obj(map[string]any{"project_id": str("Project identifier"), "revision": publicRevision("Revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "path": str("Optional relative path"), "limit": collectionLimit, "cursor": publicServerCursorSchema()}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
		if e != nil {
			return nil, e
		}
		return genericActionPageResult(map[string]any{"paths": v}, page.HasMore, page.NextCursor)
	})
	add("git_worktree_status", "Read the local managed worktree status.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		return s.Service.Git.WorktreeStatus(ctx, p)
	})
	add("git_read_file", "Read a UTF-8 file at any revision.", obj(map[string]any{"project_id": str("Project identifier"), "revision": publicRevision("Revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "path": str("Relative file path")}, "project_id", "revision", "path"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
	add("git_diff", "Read bounded diff between two revisions.", obj(map[string]any{"project_id": str("Project identifier"), "from_revision": publicRevision("Base revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "to_revision": publicRevision("Target revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "paths": array(str("Optional relative path"))}, "project_id", "from_revision", "to_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
	add("git_compare", "Compare divergence and merge base.", obj(map[string]any{"project_id": str("Project identifier"), "left": publicRevision("Left revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "right": publicRevision("Right revision or ref; commit fingerprints use 8 lowercase hexadecimal characters")}, "project_id", "left", "right"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
	add("git_merge_base", "Find merge base.", obj(map[string]any{"project_id": str("Project identifier"), "left": publicRevision("Left revision or ref; commit fingerprints use 8 lowercase hexadecimal characters"), "right": publicRevision("Right revision or ref; commit fingerprints use 8 lowercase hexadecimal characters")}, "project_id", "left", "right"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
