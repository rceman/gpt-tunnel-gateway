package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureCodeActions() {
	if s.Service == nil {
		return
	}
	s.codeActions.Do(func() {
		s.codeActionErr = s.registerCodeActions()
	})
	if s.codeActionErr != nil {
		panic(s.codeActionErr)
	}
}

func (s *Server) registerCodeActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "code/read",
		Description:     "Read a bounded file from an existing local project worktree.",
		InputSchema:     codeReadInputSchema(),
		OutputSchema:    codeReadOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				WorktreeRef string `json:"worktree_ref"`
				BaseSHA     string `json:"base_sha"`
				Path        string `json:"path"`
				Offset      int64  `json:"offset,omitempty"`
				MaxBytes    int    `json:"max_bytes,omitempty"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.CodeRead(ctx, service.CodeReadInput{ProjectID: projectID, WorktreeRef: input.WorktreeRef, BaseSHA: input.BaseSHA, Path: input.Path, Offset: input.Offset, MaxBytes: input.MaxBytes})
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "code/search",
		Description:     "Search bounded explicit files in an existing local project worktree.",
		InputSchema:     codeSearchInputSchema(),
		OutputSchema:    codeSearchOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				WorktreeRef string   `json:"worktree_ref"`
				BaseSHA     string   `json:"base_sha"`
				Query       string   `json:"query"`
				Paths       []string `json:"paths"`
				Limit       int      `json:"limit,omitempty"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.CodeSearch(ctx, service.CodeSearchInput{ProjectID: projectID, WorktreeRef: input.WorktreeRef, BaseSHA: input.BaseSHA, Query: input.Query, Paths: input.Paths, Limit: input.Limit})
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:            "code/diff",
		Description:     "Read a bounded diff from an exact local base SHA to a local project worktree.",
		InputSchema:     codeDiffInputSchema(),
		OutputSchema:    codeDiffOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRolePlannerOrDelivery,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input struct {
				WorktreeRef string   `json:"worktree_ref"`
				BaseSHA     string   `json:"base_sha"`
				Paths       []string `json:"paths"`
				MaxBytes    int      `json:"max_bytes,omitempty"`
			}
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.CodeDiff(ctx, service.CodeDiffInput{ProjectID: projectID, WorktreeRef: input.WorktreeRef, BaseSHA: input.BaseSHA, Paths: input.Paths, MaxBytes: input.MaxBytes})
		},
	})
}

func (s *Server) boundCodeProject(ctx context.Context) (string, error) {
	sessionID := service.AgentSessionID(ctx)
	if sessionID == "" {
		return "", fmt.Errorf("code action requires a bound session")
	}
	record, err := s.activeSession(sessionID)
	if err != nil {
		return "", err
	}
	if record.ProjectID == "" {
		return "", fmt.Errorf("code action requires a bound project")
	}
	return record.ProjectID, nil
}

func codeInputProperties() map[string]any {
	worktree := str("Exact full local branch ref resolved by the server to an existing worktree.")
	worktree["minLength"] = 1
	base := str("Exact 40-character local base commit SHA.")
	base["minLength"], base["maxLength"] = 40, 40
	return map[string]any{"worktree_ref": worktree, "base_sha": base}
}

func codeReadInputSchema() map[string]any {
	properties := codeInputProperties()
	properties["path"] = str("Repository-relative regular-file path.")
	properties["offset"] = integer("Byte offset in the file.", 0, 1<<30)
	properties["max_bytes"] = integer("Maximum response bytes.", 1, service.LocalCodeMaxBytes)
	return obj(properties, "worktree_ref", "base_sha", "path")
}

func codeSearchInputSchema() map[string]any {
	properties := codeInputProperties()
	query := str("Literal bounded search query.")
	query["minLength"] = 1
	query["maxLength"] = service.LocalCodeMaxQueryBytes
	paths := array(str("Explicit repository-relative regular-file path."))
	paths["minItems"], paths["maxItems"] = 1, service.LocalCodeMaxPaths
	properties["query"], properties["paths"] = query, paths
	properties["limit"] = integer("Maximum compact matches.", 1, service.LocalCodeMaxMatches)
	return obj(properties, "worktree_ref", "base_sha", "query", "paths")
}

func codeDiffInputSchema() map[string]any {
	properties := codeInputProperties()
	paths := array(str("Explicit repository-relative diff path."))
	paths["maxItems"] = service.LocalCodeMaxPaths
	properties["paths"] = paths
	properties["max_bytes"] = integer("Maximum diff response bytes.", 1, service.LocalCodeMaxBytes)
	return obj(properties, "worktree_ref", "base_sha")
}

func codeIdentityOutputSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"project_id": map[string]any{"type": "string"}, "worktree_ref": map[string]any{"type": "string"},
			"base_sha": map[string]any{"type": "string"}, "current_head": map[string]any{"type": "string"}, "dirty": map[string]any{"type": "boolean"},
		},
	}
}

func codeReadOutputSchema() map[string]any {
	properties := codeIdentityOutputSchema()["properties"].(map[string]any)
	properties["path"] = map[string]any{"type": "string"}
	properties["offset"] = map[string]any{"type": "integer"}
	properties["total_bytes"] = map[string]any{"type": "integer"}
	properties["content"] = map[string]any{"type": "string"}
	properties["truncated"] = map[string]any{"type": "boolean"}
	return closedOutput(properties, "project_id", "worktree_ref", "base_sha", "current_head", "dirty", "path", "offset", "total_bytes", "content", "truncated")
}

func codeSearchOutputSchema() map[string]any {
	properties := codeIdentityOutputSchema()["properties"].(map[string]any)
	properties["paths_scanned"] = map[string]any{"type": "integer"}
	properties["matches"] = array(closedOutput(map[string]any{"path": map[string]any{"type": "string"}, "line": map[string]any{"type": "integer"}, "snippet": map[string]any{"type": "string"}}, "path", "line", "snippet"))
	properties["truncated"] = map[string]any{"type": "boolean"}
	return closedOutput(properties, "project_id", "worktree_ref", "base_sha", "current_head", "dirty", "paths_scanned", "matches", "truncated")
}

func codeDiffOutputSchema() map[string]any {
	properties := codeIdentityOutputSchema()["properties"].(map[string]any)
	properties["paths"] = array(map[string]any{"type": "string"})
	properties["diff"] = map[string]any{"type": "string"}
	properties["truncated"] = map[string]any{"type": "boolean"}
	return closedOutput(properties, "project_id", "worktree_ref", "base_sha", "current_head", "dirty", "paths", "diff", "truncated")
}
