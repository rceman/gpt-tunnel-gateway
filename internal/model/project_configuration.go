package model

import (
	"fmt"
	"time"
)

const ProjectConfigurationSchemaVersion = 3

type ProjectAgentRouting struct {
	SingletonRecommendedReasoning string `json:"singleton_recommended_reasoning"`
	GroupRecommendedReasoning     string `json:"group_recommended_reasoning"`
	Fallback                      string `json:"fallback"`
}

type ProjectIntegrationConfiguration struct {
	TargetBranch string `json:"target_branch"`
}

// ProjectCheckpointProfile selects the explicit adapter used by the neutral
// work checkpoint engine. An empty adapter is never interpreted as Go.
type ProjectCheckpointProfile struct {
	Adapter string `json:"adapter,omitempty"`
}

// ProjectConfiguration is the canonical portable project settings authority.
// Host paths, provider/model/session bindings, process IDs and secrets remain
// in config.Config and are intentionally absent from this model.
type ProjectConfiguration struct {
	SchemaVersion int                                   `json:"schema_version"`
	ProjectID     string                                `json:"project_id"`
	Revision      int                                   `json:"revision"`
	AgentRouting  ProjectAgentRouting                   `json:"agent_routing"`
	Checkpoint    ProjectCheckpointProfile              `json:"checkpoint"`
	Integration   ProjectIntegrationConfiguration       `json:"integration"`
	GuideBindings map[string]string                     `json:"guide_bindings"`
	Procedures    map[string]ProjectProcedureDefinition `json:"procedures"`
	Hooks         map[string]string                     `json:"hooks"`
	UpdatedBy     string                                `json:"updated_by"`
	UpdatedAt     time.Time                             `json:"updated_at"`
}

func DefaultProjectConfiguration(projectID string, now time.Time) ProjectConfiguration {
	return ProjectConfiguration{
		SchemaVersion: ProjectConfigurationSchemaVersion,
		ProjectID:     projectID,
		Revision:      1,
		AgentRouting: ProjectAgentRouting{
			SingletonRecommendedReasoning: ReasoningHigh,
			GroupRecommendedReasoning:     ReasoningMax,
			Fallback:                      ReasoningBestAvailable,
		},
		Integration: ProjectIntegrationConfiguration{
			TargetBranch: "main",
		},
		GuideBindings: map[string]string{},
		Procedures:    map[string]ProjectProcedureDefinition{},
		Hooks:         map[string]string{},
		UpdatedBy:     "gateway",
		UpdatedAt:     now.UTC(),
	}
}

func ValidateProjectConfiguration(v ProjectConfiguration) error {
	if v.SchemaVersion != ProjectConfigurationSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Revision < 1 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid project configuration identity")
	}
	if v.UpdatedBy == "" || containsUnsafeText(v.UpdatedBy) {
		return fmt.Errorf("invalid project configuration update metadata")
	}
	if v.GuideBindings == nil || v.Procedures == nil || v.Hooks == nil || v.Integration.TargetBranch == "" {
		return fmt.Errorf("project configuration is missing canonical fields")
	}
	if err := validateReasoningTier(v.AgentRouting.SingletonRecommendedReasoning); err != nil {
		return fmt.Errorf("singleton reasoning: %w", err)
	}
	if err := validateReasoningTier(v.AgentRouting.GroupRecommendedReasoning); err != nil {
		return fmt.Errorf("group reasoning: %w", err)
	}
	if v.AgentRouting.Fallback != ReasoningBestAvailable {
		return fmt.Errorf("project agent fallback must be best_available")
	}
	if err := ValidateBranch(v.Integration.TargetBranch); err != nil {
		return fmt.Errorf("integration target branch: %w", err)
	}
	if err := ValidateProjectProcedures(v.Procedures); err != nil {
		return err
	}
	if err := ValidateProjectHooks(v.Hooks, v.Procedures); err != nil {
		return err
	}
	if err := ValidateGuideBindings(v.GuideBindings); err != nil {
		return err
	}
	return nil
}

func validateReasoningTier(value string) error {
	switch value {
	case ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningMax, ReasoningBestAvailable:
		return nil
	default:
		return fmt.Errorf("unsupported reasoning tier")
	}
}

func containsUnsafeText(value string) bool {
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' {
			return true
		}
	}
	return false
}
