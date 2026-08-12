package model

import (
	"fmt"
	"time"
)

const ProjectConfigurationSchemaVersion = 1

type ProjectAgentRouting struct {
	SingletonRecommendedReasoning string `json:"singleton_recommended_reasoning"`
	GroupRecommendedReasoning     string `json:"group_recommended_reasoning"`
	Fallback                      string `json:"fallback"`
}

type ProjectConfigurationWatcher struct {
	AgentID        string `json:"agent_id,omitempty"`
	Mode           string `json:"mode"`
	CadenceSeconds int    `json:"cadence_seconds"`
	TailLines      int    `json:"tail_lines"`
	SeenRetention  int    `json:"seen_retention"`
	NudgeEnabled   bool   `json:"nudge_enabled"`
	RestartEnabled bool   `json:"restart_enabled"`
}

type ProjectConfigurationWorkflow struct {
	WorkflowStage     string           `json:"workflow_stage"`
	IntegrationBranch string           `json:"integration_branch"`
	CI                WorkflowPolicyCI `json:"ci"`
	Gates             []string         `json:"gates"`
	WaitForCI         bool             `json:"wait_for_ci"`
}

// ProjectConfiguration is the canonical portable project settings authority.
// Host paths, provider/model/session bindings, process IDs and secrets remain
// in config.Config and are intentionally absent from this model.
type ProjectConfiguration struct {
	SchemaVersion        int                          `json:"schema_version"`
	ProjectID            string                       `json:"project_id"`
	Revision             int                          `json:"revision"`
	ExecutionModel       string                       `json:"execution_model,omitempty"`
	AgentRouting         ProjectAgentRouting          `json:"agent_routing"`
	Watcher              ProjectConfigurationWatcher  `json:"watcher"`
	Workflow             ProjectConfigurationWorkflow `json:"workflow"`
	ActivationProfileRef string                       `json:"activation_profile_ref,omitempty"`
	UpdatedBy            string                       `json:"updated_by"`
	UpdatedAt            time.Time                    `json:"updated_at"`
}

func DefaultProjectConfiguration(projectID string, now time.Time) ProjectConfiguration {
	return ProjectConfiguration{
		SchemaVersion:  ProjectConfigurationSchemaVersion,
		ProjectID:      projectID,
		Revision:       1,
		ExecutionModel: "legacy",
		AgentRouting: ProjectAgentRouting{
			SingletonRecommendedReasoning: ReasoningHigh,
			GroupRecommendedReasoning:     ReasoningMax,
			Fallback:                      ReasoningBestAvailable,
		},
		Watcher: ProjectConfigurationWatcher{
			Mode:           "disabled",
			CadenceSeconds: WatcherDefaultCadenceSeconds,
			TailLines:      WatcherDefaultTailLines,
			SeenRetention:  WatcherMaxSeenDigests,
		},
		Workflow: ProjectConfigurationWorkflow{
			WorkflowStage:     WorkflowStageTransitionalMain,
			IntegrationBranch: "main",
			CI: WorkflowPolicyCI{
				Task:      WorkflowCIModeDisabled,
				TaskMerge: WorkflowCIModeDisabled,
				Release:   WorkflowCIModeDisabled,
			},
			Gates:     StandardWorkflowGates(),
			WaitForCI: false,
		},
		ActivationProfileRef: "default",
		UpdatedBy:            "gateway",
		UpdatedAt:            now.UTC(),
	}
}

func ValidateProjectConfiguration(v ProjectConfiguration) error {
	if v.SchemaVersion != ProjectConfigurationSchemaVersion || ValidateProjectIdentifier(v.ProjectID) != nil || v.Revision < 1 || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid project configuration identity")
	}
	if v.ExecutionModel != "" && v.ExecutionModel != "legacy" && v.ExecutionModel != "train_v2" {
		return fmt.Errorf("invalid project execution_model")
	}
	if v.UpdatedBy == "" || containsUnsafeText(v.UpdatedBy) {
		return fmt.Errorf("invalid project configuration update metadata")
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
	if v.Watcher.AgentID != "" && ValidateObjectIdentifier(v.Watcher.AgentID) != nil {
		return fmt.Errorf("invalid project configuration watcher agent_id")
	}
	if v.Watcher.Mode != "disabled" && v.Watcher.Mode != "observe" && v.Watcher.Mode != "require" {
		return fmt.Errorf("invalid project configuration watcher mode")
	}
	if v.Watcher.CadenceSeconds < 1 || v.Watcher.CadenceSeconds > 3600 || v.Watcher.TailLines < 1 || v.Watcher.TailLines > WatcherMaxTailLines || v.Watcher.SeenRetention < 1 || v.Watcher.SeenRetention > WatcherMaxSeenDigests {
		return fmt.Errorf("invalid project configuration watcher bounds")
	}
	policy := ProjectWorkflowPolicy{
		SchemaVersion:     SchemaVersion,
		ProjectID:         v.ProjectID,
		Revision:          v.Revision,
		WorkflowStage:     v.Workflow.WorkflowStage,
		IntegrationBranch: v.Workflow.IntegrationBranch,
		Agent: WorkflowPolicyAgent{
			WaitForCI: v.Workflow.WaitForCI,
		},
		CI:        v.Workflow.CI,
		Gates:     v.Workflow.Gates,
		UpdatedBy: v.UpdatedBy,
		UpdatedAt: v.UpdatedAt,
	}
	if err := ValidateProjectWorkflowPolicy(policy); err != nil {
		return fmt.Errorf("workflow configuration: %w", err)
	}
	if v.ActivationProfileRef != "" && ValidateObjectIdentifier(v.ActivationProfileRef) != nil {
		return fmt.Errorf("invalid activation_profile_ref")
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
