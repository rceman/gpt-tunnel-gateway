package model

import (
	"fmt"
	"path/filepath"
	"strings"
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

// ProjectGateCommand is a repository-owned executable plus its fixed argv.
// It is deliberately an argv vector rather than a shell command so project
// configuration cannot introduce shell expansion or pipelines.
type ProjectGateCommand struct {
	Command []string `json:"command"`
}

type ProjectTestGateCommands struct {
	Task  ProjectGateCommand `json:"task"`
	Train ProjectGateCommand `json:"train"`
}

// ProjectGateCommands is the complete configurable gate surface. Gateway
// invariants are intentionally not represented here.
type ProjectGateCommands struct {
	Format ProjectGateCommand      `json:"format"`
	Check  ProjectGateCommand      `json:"check"`
	Test   ProjectTestGateCommands `json:"test"`
}

type ProjectConfigurationWorkflow struct {
	WorkflowStage     string              `json:"workflow_stage"`
	IntegrationBranch string              `json:"integration_branch"`
	CI                WorkflowPolicyCI    `json:"ci"`
	Gates             []string            `json:"gates"`
	GateCommands      ProjectGateCommands `json:"gate_commands"`
	WaitForCI         bool                `json:"wait_for_ci"`
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
			Gates:        StandardWorkflowGates(),
			GateCommands: DefaultProjectGateCommands(),
			WaitForCI:    false,
		},
		ActivationProfileRef: "default",
		UpdatedBy:            "gateway",
		UpdatedAt:            now.UTC(),
	}
}

func DefaultProjectGateCommands() ProjectGateCommands {
	return ProjectGateCommands{
		Format: ProjectGateCommand{
			Command: []string{"go", "run", "./cmd/gofmt-struct", "--check", "."},
		},
		Check: ProjectGateCommand{
			Command: []string{"python3", "scripts/static-check.py"},
		},
		Test: ProjectTestGateCommands{
			Task: ProjectGateCommand{
				Command: []string{"go", "test", "./...", "-count=1"},
			},
			Train: ProjectGateCommand{
				Command: []string{"go", "test", "./...", "-count=1"},
			},
		},
	}
}

func (v ProjectGateCommands) IsZero() bool {
	return len(v.Format.Command) == 0 && len(v.Check.Command) == 0 && len(v.Test.Task.Command) == 0 && len(v.Test.Train.Command) == 0
}

func (v ProjectGateCommands) Validate() error {
	if err := v.Format.Validate("format"); err != nil {
		return err
	}
	if err := v.Check.Validate("check"); err != nil {
		return err
	}
	if err := v.Test.Task.Validate("test.task"); err != nil {
		return err
	}
	return v.Test.Train.Validate("test.train")
}

func (v ProjectGateCommand) Validate(name string) error {
	if len(v.Command) == 0 || len(v.Command) > 32 || v.Command[0] == "" {
		return fmt.Errorf("invalid project gate command %s", name)
	}
	for _, arg := range v.Command {
		if arg == "" || containsUnsafeText(arg) || len(arg) > 512 || filepath.IsAbs(arg) || arg == ".." || strings.HasPrefix(arg, "../") || strings.Contains(arg, "/../") {
			return fmt.Errorf("invalid project gate command %s", name)
		}
	}
	if strings.ContainsAny(v.Command[0], "/\\") && !strings.HasPrefix(v.Command[0], "./") {
		return fmt.Errorf("project gate command %s must use a repository-relative executable", name)
	}
	if (v.Command[0] == "sh" || v.Command[0] == "bash" || v.Command[0] == "zsh") && len(v.Command) > 1 && v.Command[1] == "-c" {
		return fmt.Errorf("project gate command %s may not invoke a shell string", name)
	}
	return nil
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
	gateCommands := v.Workflow.GateCommands
	if gateCommands.IsZero() {
		gateCommands = DefaultProjectGateCommands()
	}
	if err := gateCommands.Validate(); err != nil {
		return fmt.Errorf("gate configuration: %w", err)
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
