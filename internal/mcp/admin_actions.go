package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureAdminActions() {
	if s.Service == nil {
		return
	}
	s.adminActions.Do(func() {
		s.adminActionErr = s.RegisterGenericAction(GenericAction{
			Path:            "admin/project/onboard",
			Description:     "Onboard one allowlisted GitHub repository and ensure project readiness.",
			InputSchema:     adminProjectOnboardInputSchema(),
			OutputSchema:    adminProjectOnboardOutputSchema(),
			AuthorityRole:   durableSession.RoleAdmin,
			SessionRequired: true,
			SessionBound:    false,
			Annotations: ToolAnnotations{
				ReadOnlyHint:    false,
				DestructiveHint: false,
				IdempotentHint:  true,
				OpenWorldHint:   true,
			},
			Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var in service.AdminProjectOnboardInput
				if err := decode(raw, &in); err != nil {
					return nil, err
				}
				return s.Service.AdminProjectOnboard(ctx, in)
			},
		})
	})
	if s.adminActionErr != nil {
		panic(s.adminActionErr)
	}
}

func adminProjectOnboardInputSchema() map[string]any {
	repository := str("Canonical GitHub owner/name repository identity.")
	repository["pattern"] = `^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?/[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`
	code := str("Immutable three-letter project code.")
	code["pattern"] = `^[A-Z]{3}$`
	harness := str("Optional configured Airelay Worker profile; defaults to codex.")
	harness["pattern"] = `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`
	return obj(map[string]any{"repository": repository, "code": code, "harness": harness}, "repository", "code")
}

func adminProjectOnboardOutputSchema() map[string]any {
	worker := closedOutput(map[string]any{
		"key":    outputString(),
		"status": outputEnum("ready", "not_ready"),
	}, "key", "status")
	return closedOutput(map[string]any{
		"project_id": outputString(),
		"status":     outputEnum("ready", "partial"),
		"worker":     worker,
	}, "project_id", "status", "worker")
}
