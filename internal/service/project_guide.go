package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type ProjectGuideBindingInput struct {
	ProjectID string
	Subject   string
	RuleID    string
	Reason    string
	UpdatedBy string
}

type ProjectGuideBinding struct {
	Subject string `json:"subject"`
	Rule    string `json:"rule"`
}

func (s *Service) ProjectGuideRead(ctx context.Context, projectID, subject string) (model.Rule, map[string]string, bool, error) {
	if s.Durability == nil {
		return model.Rule{}, nil, false, fmt.Errorf("guide Shared durability is unavailable")
	}
	if !model.GuideRequired(subject) {
		return model.Rule{}, nil, false, fmt.Errorf("guide subject %q is not applicable", subject)
	}
	configuration, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil {
		return model.Rule{}, nil, false, err
	}
	ruleID, bound := configuration.GuideBindings[subject]
	if !bound {
		if model.GuideBuiltinUntilBound(subject) {
			return model.Rule{}, nil, false, nil
		}
		return model.Rule{}, nil, false, fmt.Errorf("guide binding is missing for %s", subject)
	}
	rule, err := s.RuleRead(ctx, projectID, ruleID)
	if err != nil {
		return model.Rule{}, nil, false, fmt.Errorf("bound guide Rule %s is unavailable: %w", ruleID, err)
	}
	if rule.ID != ruleID || rule.ProjectID != projectID {
		return model.Rule{}, nil, false, fmt.Errorf("bound guide Rule ownership mismatch")
	}
	if rule.Status != model.RuleStatusAccepted {
		return model.Rule{}, nil, false, fmt.Errorf("bound guide Rule %s is not accepted", ruleID)
	}
	values, err := model.ValidateGuideValue(subject, rule.Value)
	if err != nil {
		return model.Rule{}, nil, false, fmt.Errorf("bound guide Rule %s has an invalid %s projection: %w", ruleID, subject, err)
	}
	return rule, values, true, nil
}

func (s *Service) ProjectGuideBind(ctx context.Context, in ProjectGuideBindingInput) (ProjectGuideBinding, error) {
	if !model.GuideRequired(in.Subject) {
		return ProjectGuideBinding{}, fmt.Errorf("guide subject %q is not applicable", in.Subject)
	}
	if err := model.ValidateRuleID(in.RuleID); err != nil {
		return ProjectGuideBinding{}, err
	}
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > model.MaxDeferredReasonBytes || strings.ContainsAny(in.Reason, "\r\n\x00") {
		return ProjectGuideBinding{}, fmt.Errorf("a bounded guide binding reason is required")
	}
	if in.UpdatedBy == "" || strings.ContainsAny(in.UpdatedBy, "\r\n\x00") {
		return ProjectGuideBinding{}, fmt.Errorf("guide binding actor is required")
	}
	if s.Durability == nil {
		return ProjectGuideBinding{}, fmt.Errorf("project guide binding requires Shared durability")
	}
	rule, err := s.RuleRead(ctx, in.ProjectID, in.RuleID)
	if err != nil {
		return ProjectGuideBinding{}, err
	}
	if rule.ID != in.RuleID || rule.ProjectID != in.ProjectID {
		return ProjectGuideBinding{}, fmt.Errorf("guide Rule must belong to the bound project")
	}
	if rule.Status != model.RuleStatusAccepted {
		return ProjectGuideBinding{}, fmt.Errorf("guide Rule must be accepted")
	}
	if _, err := model.ValidateGuideValue(in.Subject, rule.Value); err != nil {
		return ProjectGuideBinding{}, fmt.Errorf("guide Rule value does not match the %s projection: %w", in.Subject, err)
	}
	configuration, err := s.ProjectConfigurationRead(ctx, in.ProjectID)
	if err != nil {
		return ProjectGuideBinding{}, err
	}
	if configuration.GuideBindings[in.Subject] == in.RuleID {
		return ProjectGuideBinding{
			Subject: in.Subject,
			Rule:    in.RuleID,
		}, nil
	}
	bindings := make(map[string]string, len(configuration.GuideBindings)+1)
	for subject, ruleID := range configuration.GuideBindings {
		bindings[subject] = ruleID
	}
	bindings[in.Subject] = in.RuleID
	if _, _, err := s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
		ProjectID:        in.ProjectID,
		ExpectedRevision: configuration.Revision,
		Patch: ProjectConfigurationPatch{
			GuideBindings: &bindings,
		},
		UpdatedBy: in.UpdatedBy,
	}); err != nil {
		latest, readErr := s.ProjectConfigurationRead(ctx, in.ProjectID)
		if readErr != nil || latest.GuideBindings[in.Subject] != in.RuleID {
			return ProjectGuideBinding{}, err
		}
	}
	return ProjectGuideBinding{
		Subject: in.Subject,
		Rule:    in.RuleID,
	}, nil
}
