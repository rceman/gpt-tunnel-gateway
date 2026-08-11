package mcp

import (
	"context"
	"encoding/json"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureTaskCloseoutActions() {
	if s.Service == nil {
		return
	}
	s.taskCloseoutActions.Do(func() {
		s.taskCloseoutActionErr = s.registerTaskCloseoutActions()
	})
	if s.taskCloseoutActionErr != nil {
		panic(s.taskCloseoutActionErr)
	}
}

func taskReviewInputSchema() map[string]any {
	finding := obj(map[string]any{
		"id":       str("Stable finding identifier."),
		"severity": str("Finding severity."),
		"title":    str("Finding title."),
		"detail":   str("Finding detail."),
	}, "id", "severity", "title", "detail")
	coverage := obj(map[string]any{
		"surface": str("Reviewed scope surface."),
		"status":  outputEnum("covered", "inspected_no_change", "blocked"),
		"detail":  str("Concise scope evidence."),
	}, "surface", "status", "detail")
	outcome := str("Semantic Delivery outcome.")
	outcome["enum"] = []any{model.ReviewOutcomeAccepted, model.ReviewOutcomeRejected, model.ReviewOutcomeBlocked, model.ReviewOutcomeInconclusive}
	findings := array(finding)
	findings["maxItems"] = model.MaxReviewFindings
	scope := array(coverage)
	scope["maxItems"] = model.MaxReviewScopeCoverage
	return obj(map[string]any{
		"task_id":                  str("Task identifier."),
		"run_id":                   str("Terminal implementation Run identifier."),
		"outcome":                  outcome,
		"findings":                 findings,
		"scope_coverage":           scope,
		"unexpected_surfaces":      array(str("Material unexpected surface.")),
		"historical_compatibility": array(str("Material historical compatibility note.")),
		"prohibited_actions":       array(str("Observed prohibited action.")),
	}, "task_id", "run_id", "outcome", "findings", "scope_coverage")
}

func taskReviewOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"report":    runReviewReportOutputSchema(),
		"operation": operationOutputSchema(),
	}, "report", "operation")
}

func taskIntegrationReceiptOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"task_id":              outputString(),
		"reviewed_head":        outputString(),
		"integration_head":     outputString(),
		"runtime_source_head":  outputString(),
		"pre_activation":       outputString(),
		"pre_smoke":            outputString(),
		"post_activation":      outputString(),
		"post_smoke":           outputString(),
		"merged":               outputBoolean(),
		"next_action":          outputString(),
		"activation_blocker":   outputString(),
		"integration_conflict": outputString(),
	}, "task_id", "reviewed_head", "integration_head", "runtime_source_head", "pre_activation", "pre_smoke", "post_activation", "post_smoke", "merged", "next_action")
}

func taskIntegrateOutputSchema() map[string]any {
	return closedOutput(map[string]any{
		"receipt":   taskIntegrationReceiptOutputSchema(),
		"operation": operationOutputSchema(),
	}, "receipt", "operation")
}

func (s *Server) registerTaskCloseoutActions() error {
	register := func(action GenericAction) error {
		action.AuthorityRole = durableSession.RoleDelivery
		action.RequiresWorkflowPolicy = true
		action.Authority = authority.RequireDelivery
		return s.RegisterGenericAction(action)
	}
	if err := register(GenericAction{
		Path:         "task/review",
		Description:  "Publish one semantic Delivery review and atomically advance an accepted task to merge_ready.",
		InputSchema:  taskReviewInputSchema(),
		OutputSchema: taskReviewOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  false,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskReviewInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			report, operation, err := s.Service.TaskReview(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"report": report, "operation": operation}, nil
		},
	}); err != nil {
		return err
	}
	return register(GenericAction{
		Path:         "task/integrate",
		Description:  "Integrate the accepted reviewed Task head with server-owned activation, fast-forward, and receipts.",
		InputSchema:  obj(map[string]any{"task_id": str("Merge-ready Task identifier.")}, "task_id"),
		OutputSchema: taskIntegrateOutputSchema(),
		Annotations: ToolAnnotations{
			DestructiveHint: true,
			IdempotentHint:  true,
		},
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in service.TaskIntegrationInput
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			receipt, operation, err := s.Service.TaskIntegrate(ctx, in)
			if err != nil {
				return nil, err
			}
			return map[string]any{"receipt": receipt, "operation": operation}, nil
		},
	})
}
