package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func (s *Server) ensureGuideActions() {
	if s.Service == nil {
		return
	}
	s.guideActions.Do(func() {
		s.guideActionErr = s.registerGuideActions()
	})
	if s.guideActionErr != nil {
		panic(s.guideActionErr)
	}
}

func (s *Server) registerGuideActions() error {
	for _, subject := range model.GuideSubjects() {
		if err := s.registerProjectGuideAction(subject); err != nil {
			return err
		}
	}
	return s.registerProjectGuideBindAction()
}

func (s *Server) registerProjectGuideAction(subject string) error {
	inputSchema := obj(map[string]any{})
	authorityRole := actionRoleWorkflow
	if subject == "agent" {
		authorityRole = actionRolePlannerOrLead
	}
	return s.RegisterGenericAction(GenericAction{
		Path:                 subject + "/guide",
		Description:          "Read the bounded project guide projection for this durable domain.",
		InputSchema:          inputSchema,
		ExecutionInputSchema: adrExecutionSchema(inputSchema),
		OutputSchema:         projectGuideOutputSchema(subject),
		Annotations: ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: true,
		},
		LocalReadOnly:   true,
		SessionBound:    true,
		SessionRequired: true,
		AuthorityRole:   authorityRole,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				ProjectID string `json:"project_id"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			rule, values, bound, err := s.Service.ProjectGuideRead(ctx, in.ProjectID, subject)
			if err != nil {
				return nil, err
			}
			result := make(map[string]any, len(values)+3)
			if bound {
				result["source"] = "rule"
				result["rule"] = rule.ID
				result["rule_revision"] = rule.Revision
			} else {
				if !model.GuideBuiltinUntilBound(subject) {
					return nil, fmt.Errorf("guide binding is missing for %s", subject)
				}
				result["source"] = "builtin"
				values = builtinProjectGuide(subject)
			}
			for field, value := range values {
				result[field] = value
			}
			return result, nil
		},
	})
}

func projectGuideOutputSchema(subject string) map[string]any {
	fields, _ := model.GuideProjectionFields(subject)
	projected := make(map[string]any, len(fields)+3)
	required := make([]string, 0, len(fields)+3)
	source := outputString()
	source["const"] = "rule"
	projected["source"] = source
	ruleID := outputString()
	ruleID["minLength"], ruleID["maxLength"], ruleID["pattern"] = 8, model.MaxRuleIDLength, model.RuleIDPattern
	projected["rule"] = ruleID
	ruleRevision := outputInteger()
	ruleRevision["minimum"] = 1
	projected["rule_revision"] = ruleRevision
	required = append(required, "source", "rule", "rule_revision")
	for _, field := range fields {
		value := outputString()
		value["minLength"], value["maxLength"] = 1, model.GuideTextMaxRunes
		projected[field] = value
		required = append(required, field)
	}
	ruleOutput := closedOutput(projected, required...)
	if !model.GuideBuiltinUntilBound(subject) {
		return ruleOutput
	}
	builtin := make(map[string]any, len(fields)+1)
	builtinSource := outputString()
	builtinSource["const"] = "builtin"
	builtin["source"] = builtinSource
	builtinRequired := []string{"source"}
	for _, field := range fields {
		value := outputString()
		value["minLength"], value["maxLength"] = 1, model.GuideTextMaxRunes
		builtin[field] = value
		builtinRequired = append(builtinRequired, field)
	}
	return map[string]any{"oneOf": []any{closedOutput(builtin, builtinRequired...), ruleOutput}}
}

func builtinProjectGuide(subject string) map[string]string {
	switch subject {
	case "agent":
		return stringGuideFields(canonicalAgentGuide())
	case "task":
		return map[string]string{
			"workflow": taskGuideWorkflow, "review": taskGuideReview,
			"verification": taskGuideVerification, "completion": taskGuideCompletion,
			"boundaries": taskGuideBoundaries,
		}
	default:
		return nil
	}
}

func stringGuideFields(value map[string]any) map[string]string {
	result := make(map[string]string, len(value))
	for field, item := range value {
		if text, ok := item.(string); ok {
			result[field] = text
		}
	}
	return result
}

func (s *Server) registerProjectGuideBindAction() error {
	subject := str("Applicable durable guide subject.")
	subject["enum"] = model.GuideSubjects()
	rule := str("Canonical same-project accepted Rule key.")
	rule["minLength"], rule["maxLength"], rule["pattern"] = 8, model.MaxRuleIDLength, model.RuleIDPattern
	reason := str("Truthful bounded reason for this binding or rebind.")
	reason["minLength"], reason["maxLength"] = 1, model.MaxDeferredReasonBytes
	inputSchema := obj(map[string]any{"subject": subject, "rule": rule, "reason": reason}, "subject", "rule", "reason")
	return s.RegisterGenericAction(GenericAction{
		Path:                 "project/guide_bind",
		Description:          "Bind one applicable project guide to a same-project accepted Rule using a reasoned Planner decision.",
		InputSchema:          inputSchema,
		ExecutionInputSchema: adrExecutionSchema(inputSchema),
		OutputSchema: closedOutput(map[string]any{
			"subject": outputEnum(model.GuideSubjects()...),
			"rule": func() map[string]any {
				value := outputString()
				value["minLength"], value["maxLength"], value["pattern"] = 8, model.MaxRuleIDLength, model.RuleIDPattern
				return value
			}(),
		}, "subject", "rule"),
		Annotations: ToolAnnotations{
			IdempotentHint: true,
		},
		LocalReceiptOnly: true,
		SessionBound:     true,
		SessionRequired:  true,
		AuthorityRole:    durableSession.RolePlanner,
		Execute: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := requireGuideBindingPlanner(ctx); err != nil {
				return nil, err
			}
			var in struct {
				ProjectID string `json:"project_id"`
				Subject   string `json:"subject"`
				RuleID    string `json:"rule"`
				Reason    string `json:"reason"`
			}
			if err := decode(raw, &in); err != nil {
				return nil, err
			}
			binding, err := s.Service.ProjectGuideBind(ctx, service.ProjectGuideBindingInput{
				ProjectID: in.ProjectID, Subject: in.Subject, RuleID: in.RuleID,
				Reason: in.Reason, UpdatedBy: service.AgentSessionID(ctx),
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"subject": binding.Subject, "rule": binding.Rule}, nil
		},
	})
}

func requireGuideBindingPlanner(ctx context.Context) error {
	if resolved, ok := resolvedSessionAuthorityFromContext(ctx); ok {
		if resolved.Session.Role != durableSession.RolePlanner {
			return fmt.Errorf("project/guide_bind requires a Planner Session")
		}
		return nil
	}
	return authority.RequirePlanner(ctx)
}
