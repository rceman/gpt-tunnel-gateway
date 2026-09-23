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
	s.codeActions.Do(func() { s.codeActionErr = s.registerCodeActions() })
	if s.codeActionErr != nil {
		panic(s.codeActionErr)
	}
}

func (s *Server) registerCodeActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "code/worktree",
		Description:     "List bounded server-owned project worktree selectors.",
		InputSchema:     codeWorktreeInputSchema(),
		OutputSchema:    codeWorktreeOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRoleWorkflow,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.CodeWorktreeInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			input.ProjectID = projectID
			return s.Service.CodeWorktree(ctx, input)
		},
	}); err != nil {
		return err
	}

	if err := s.RegisterGenericAction(GenericAction{
		Path:            "code/tree",
		Description:     "List bounded repository-relative files in a server-owned worktree.",
		InputSchema:     codeTreeInputSchema(),
		OutputSchema:    codeTreeOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRoleWorkflow,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.CodeTreeInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			input.ProjectID = projectID
			return s.Service.CodeTree(ctx, input)
		},
	}); err != nil {
		return err
	}

	if err := s.RegisterGenericAction(GenericAction{
		Path:            "code/read",
		Description:     "Read a bounded line range from a server-owned worktree.",
		InputSchema:     codeReadInputSchema(),
		OutputSchema:    codeReadOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRoleWorkflow,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.CodeReadInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			input.ProjectID = projectID
			return s.Service.CodeRead(ctx, input)
		},
	}); err != nil {
		return err
	}

	if err := s.RegisterGenericAction(GenericAction{
		Path:            "code/search",
		Description:     "Search bounded repository-relative files in a server-owned worktree with literal alternatives and optional case-insensitive matching.",
		InputSchema:     codeSearchInputSchema(),
		OutputSchema:    codeSearchOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRoleWorkflow,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.CodeSearchInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			input.ProjectID = projectID
			return s.Service.CodeSearch(ctx, input)
		},
	}); err != nil {
		return err
	}

	return s.RegisterGenericAction(GenericAction{
		Path:            "code/diff",
		Description:     "Read a bounded diff from an authoritative local worktree base to the current head or an optional authoritative recorded head.",
		InputSchema:     codeDiffInputSchema(),
		OutputSchema:    codeDiffOutputSchema(),
		Annotations:     readOnlyAnnotations(),
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   actionRoleWorkflow,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var input service.CodeDiffInput
			if err := decode(raw, &input); err != nil {
				return nil, err
			}
			projectID, err := s.boundCodeProject(ctx)
			if err != nil {
				return nil, err
			}
			input.ProjectID = projectID
			return s.Service.CodeDiff(ctx, input)
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

func codeCursorSchema() map[string]any {
	cursor := publicServerCursorSchema()
	cursor["description"] = "Opaque server-owned continuation cursor."
	return cursor
}

func codeLiveSchema() map[string]any {
	return map[string]any{"type": "boolean", "default": false, "description": "Allow bounded observation of current dirty worktree bytes."}
}

func codeSelectorSchema() map[string]any {
	selector := str("Project-bound WT-MAIN-<sha8> or WT-TRN<n>-<sha8> selector.")
	selector["minLength"], selector["maxLength"] = 13, 80
	return selector
}

func codeWorktreeInputSchema() map[string]any {
	query := str("Optional bounded selector or label filter.")
	query["maxLength"] = service.LocalCodeMaxQueryBytes
	return obj(map[string]any{"query": query, "cursor": codeCursorSchema()})
}

func codeTreeInputSchema() map[string]any {
	pathValue := str("Optional repository-relative path prefix.")
	pathValue["maxLength"] = 4096
	query := str("Optional bounded path filter.")
	query["maxLength"] = service.LocalCodeMaxQueryBytes
	return obj(map[string]any{"worktree": codeSelectorSchema(), "path": pathValue, "query": query, "cursor": codeCursorSchema(), "live": codeLiveSchema()}, "worktree")
}

func codeReadInputSchema() map[string]any {
	pathValue := str("Repository-relative regular-file path.")
	pathValue["maxLength"] = 4096
	startLine := integer("Optional one-based semantic range start.", 1, int(^uint(0)>>1))
	lineCount := integer("Optional bounded number of semantic lines to request.", 1, int(^uint(0)>>1))
	return obj(map[string]any{"worktree": codeSelectorSchema(), "path": pathValue, "start_line": startLine, "line_count": lineCount, "cursor": codeCursorSchema(), "live": codeLiveSchema()}, "worktree", "path")
}

func codeSearchInputSchema() map[string]any {
	query := str("Bounded literal query: unescaped | separates literal alternatives, \\| matches a literal pipe, and \\\\ matches a literal backslash. No regular expressions.")
	query["minLength"], query["maxLength"] = 1, service.LocalCodeMaxQueryBytes
	paths := array(str("Optional repository-relative path."))
	paths["maxItems"] = service.LocalCodeMaxPaths
	patterns := array(str("Optional repository-relative path glob."))
	patterns["maxItems"] = service.LocalCodeMaxPatterns
	contextLines := integer("Optional number of surrounding lines per match.", 0, 3)
	caseInsensitive := map[string]any{"type": "boolean", "default": false, "description": "Match every literal alternative without case sensitivity."}
	return obj(map[string]any{"worktree": codeSelectorSchema(), "query": query, "paths": paths, "include": patterns, "exclude": patterns, "context_lines": contextLines, "case_insensitive": caseInsensitive, "cursor": codeCursorSchema(), "live": codeLiveSchema()}, "worktree", "query")
}

func codeDiffInputSchema() map[string]any {
	paths := array(str("Optional repository-relative diff path."))
	paths["maxItems"] = service.LocalCodeMaxPaths
	head := taskExecutionPublicHeadSchema()
	head["description"] = "Optional authoritative recorded Task head sha8; diffs base to that exact head. Rejected with live=true."
	return obj(map[string]any{"worktree": codeSelectorSchema(), "paths": paths, "base": taskExecutionPublicHeadSchema(), "head": head, "cursor": codeCursorSchema(), "live": codeLiveSchema()}, "worktree")
}

func codeWorktreeOutputSchema() map[string]any {
	item := closedOutput(map[string]any{"selector": outputString(), "kind": outputString(), "dirty": outputBoolean(), "head": publicGitFingerprintOutputSchema(), "label": outputString()}, "selector", "kind", "dirty", "head")
	return closedOutput(map[string]any{"items": outputArray(item)}, "items")
}

func codeIdentityOutputSchema() map[string]any {
	return map[string]any{"worktree": outputString(), "dirty": outputBoolean(), "live": outputBoolean(), "head": publicGitFingerprintOutputSchema()}
}

func codeTreeOutputSchema() map[string]any {
	properties := codeIdentityOutputSchema()
	properties["paths"] = outputArray(outputString())
	return closedOutput(properties, "worktree", "dirty", "live", "head", "paths")
}

func codeReadOutputSchema() map[string]any {
	properties := codeIdentityOutputSchema()
	properties["path"] = outputString()
	properties["start_line"] = outputInteger()
	properties["end_line"] = outputInteger()
	properties["total_lines"] = outputInteger()
	properties["content"] = outputString()
	properties["file_hash"] = outputString()
	return closedOutput(properties, "worktree", "dirty", "live", "head", "path", "start_line", "end_line", "total_lines", "content", "file_hash")
}

func codeSearchOutputSchema() map[string]any {
	match := closedOutput(map[string]any{"path": outputString(), "line": outputInteger(), "snippet": outputString()}, "path", "line", "snippet")
	properties := codeIdentityOutputSchema()
	properties["paths_scanned"] = outputInteger()
	properties["matches"] = outputArray(match)
	return closedOutput(properties, "worktree", "dirty", "live", "head", "paths_scanned", "matches")
}

func codeDiffOutputSchema() map[string]any {
	properties := codeIdentityOutputSchema()
	properties["base"] = taskExecutionPublicHeadSchema()
	properties["paths"] = outputArray(outputString())
	properties["diff"] = outputString()
	return closedOutput(properties, "worktree", "dirty", "live", "head", "base", "paths", "diff")
}
