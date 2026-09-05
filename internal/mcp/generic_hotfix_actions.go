package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func (s *Server) ensureHotfixActions() {
	if s.Service == nil {
		return
	}
	s.hotfixActions.Do(func() {
		s.hotfixActionErr = s.registerHotfixActions()
	})
	if s.hotfixActionErr != nil {
		panic(s.hotfixActionErr)
	}
}

func (s *Server) registerHotfixActions() error {
	if err := s.RegisterGenericAction(GenericAction{
		Path: "hotfix/list", Description: "List bounded server-owned hotfix lanes and execution identity.",
		InputSchema: hotfixListInputSchema(), OutputSchema: hotfixListOutputSchema(),
		Annotations: readOnlyAnnotations(), LocalReadOnly: true, SessionBound: true, SessionRequired: true,
		AuthorityRole: "planner",
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.HotfixListInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := boundHotfixProject(s, ctx)
			if err != nil {
				return nil, err
			}
			in.ProjectID = projectID
			return s.Service.HotfixList(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path: "hotfix/read", Description: "Read one server-owned hotfix lane and execution identity.",
		InputSchema: hotfixReadInputSchema(), OutputSchema: hotfixReadOutputSchema(),
		Annotations: readOnlyAnnotations(), LocalReadOnly: true, SessionBound: true, SessionRequired: true,
		AuthorityRole: "planner",
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.HotfixReadInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := boundHotfixProject(s, ctx)
			if err != nil {
				return nil, err
			}
			in.ProjectID = projectID
			return s.Service.HotfixRead(ctx, in)
		},
	}); err != nil {
		return err
	}
	if err := s.RegisterGenericAction(GenericAction{
		Path:            "hotfix/create",
		Description:     "Create one isolated hotfix lane from the exact refreshed canonical main.",
		InputSchema:     hotfixCreateInputSchema(),
		OutputSchema:    hotfixCreateOutputSchema(),
		Annotations:     ToolAnnotations{DestructiveHint: true, IdempotentHint: false},
		AuthorityRole:   "planner",
		SessionBound:    true,
		SessionRequired: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.HotfixCreateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := boundHotfixProject(s, ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.HotfixCreate(ctx, projectID, in)
		},
	}); err != nil {
		return err
	}
	return s.RegisterGenericAction(GenericAction{
		Path:            "hotfix/integrate",
		Description:     "Integrate one exact reviewed hotfix commit by strict non-force fast-forward.",
		InputSchema:     hotfixIntegrateInputSchema(),
		OutputSchema:    hotfixIntegrateOutputSchema(),
		Annotations:     ToolAnnotations{DestructiveHint: true, IdempotentHint: true},
		AuthorityRole:   "planner",
		SessionBound:    true,
		SessionRequired: true,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.HotfixIntegrateInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			projectID, err := boundHotfixProject(s, ctx)
			if err != nil {
				return nil, err
			}
			return s.Service.HotfixIntegrate(ctx, projectID, in)
		},
	})
}

func hotfixListInputSchema() map[string]any {
	limit := integer("Maximum hotfix lanes.", 1, service.MaxPublicCollectionLimit)
	limit["default"] = service.DefaultPublicCollectionLimit
	return obj(map[string]any{"limit": limit, "cursor": str("Server-owned continuation cursor.")})
}

func hotfixReadInputSchema() map[string]any {
	ref := str("Canonical hotfix/<slug> or refs/heads/hotfix/<slug>.")
	return obj(map[string]any{"hotfix": ref}, "hotfix")
}

func hotfixListOutputSchema() map[string]any {
	item := closedOutput(map[string]any{"task": outputString(), "hotfix": outputString(), "head": outputString(), "subject": outputString()}, "task", "hotfix", "head", "subject")
	return closedOutput(map[string]any{"main_head": outputString(), "hotfixes": outputArray(item), "next_cursor": outputString(), "has_more": outputBoolean()}, "main_head", "hotfixes")
}

func hotfixReadOutputSchema() map[string]any {
	return closedOutput(map[string]any{"project_id": outputString(), "hotfix_ref": outputString(), "task_id": outputString(), "base_sha": outputString(), "head_sha": outputString(), "materialized": outputBoolean()}, "project_id", "hotfix_ref", "task_id", "base_sha", "head_sha", "materialized")
}

func boundHotfixProject(s *Server, ctx context.Context) (string, error) {
	return s.boundCodeProject(ctx)
}

func hotfixCreateInputSchema() map[string]any {
	slug := str("Bounded lowercase hotfix slug.")
	slug["minLength"], slug["maxLength"] = 1, 80
	slug["pattern"] = "^[a-z0-9]+(?:-[a-z0-9]+)*$"
	task := str("Existing durable Task to bind as the sole hotfix execution.")
	return obj(map[string]any{"slug": slug, "task": task}, "slug", "task")
}

func hotfixIntegrateInputSchema() map[string]any {
	ref := str("Exact server-owned hotfix branch ref.")
	const hotfixRefPrefix = "refs/heads/hotfix/"
	ref["minLength"] = len(hotfixRefPrefix) + 1
	ref["maxLength"] = len(hotfixRefPrefix) + 80
	ref["pattern"] = "^refs/heads/hotfix/[a-z0-9]+(?:-[a-z0-9]+)*$"
	sha := func(description string) map[string]any {
		value := str(description)
		value["minLength"], value["maxLength"] = 40, 40
		value["pattern"] = "^[0-9a-f]{40}$"
		return value
	}
	return obj(map[string]any{
		"hotfix_ref":   ref,
		"reviewed_sha": sha("Exact reviewed hotfix branch HEAD."),
	}, "hotfix_ref", "reviewed_sha")
}

func hotfixCreateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "hotfix_ref": outputString(), "task_id": outputString(), "base_sha": outputString(), "head_sha": outputString(),
	}, "project_id", "hotfix_ref", "task_id", "base_sha", "head_sha")
}

func hotfixIntegrateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"project_id": outputString(), "hotfix_ref": outputString(), "task_id": outputString(), "base_sha": outputString(), "reviewed_sha": outputString(),
		"main_before": outputString(), "main_after": outputString(),
	}, "project_id", "hotfix_ref", "task_id", "base_sha", "reviewed_sha", "main_before", "main_after")
}
