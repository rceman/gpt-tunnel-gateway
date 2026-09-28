package service

import (
	"context"
	"fmt"

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
	if _, err := s.ConfigGuideBind(ctx, ConfigGuideBindInput{
		ProjectID: in.ProjectID,
		Subject:   in.Subject,
		RuleID:    in.RuleID,
		Reason:    in.Reason,
	}); err != nil {
		return ProjectGuideBinding{}, err
	}
	return ProjectGuideBinding{
		Subject: in.Subject,
		Rule:    in.RuleID,
	}, nil
}
