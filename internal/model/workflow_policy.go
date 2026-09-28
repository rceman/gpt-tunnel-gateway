package model

import (
	"fmt"
	"strings"
	"time"
)

const (
	WorkflowStageTransitionalMain = "transitional_main"
	WorkflowStageDevelopActive    = "develop_active"

	WorkflowCIModeDisabled = "disabled"
	WorkflowCIModeObserve  = "observe"
	WorkflowCIModeRequire  = "require"
)

const (
	WorkflowGateFormat = "format"
	WorkflowGateCheck  = "check"
	WorkflowGateTest   = "test"
)

type ProjectGateCommand struct {
	Command []string `json:"command"`
}

type ProjectGateTestCommands struct {
	Task ProjectGateCommand `json:"task"`
}

type ProjectGateCommands struct {
	Format ProjectGateCommand      `json:"format"`
	Check  ProjectGateCommand      `json:"check"`
	Test   ProjectGateTestCommands `json:"test"`
}

func DefaultProjectGateCommands() ProjectGateCommands {
	return ProjectGateCommands{
		Format: ProjectGateCommand{
			Command: []string{"./scripts/check-go-format.sh"},
		},
		Check: ProjectGateCommand{
			Command: []string{"python3", "scripts/static-check.py"},
		},
		Test: ProjectGateTestCommands{
			Task: ProjectGateCommand{
				Command: []string{"./scripts/test-full.sh"},
			},
		},
	}
}

func (v ProjectGateCommands) IsZero() bool {
	return len(v.Format.Command) == 0 && len(v.Check.Command) == 0 && len(v.Test.Task.Command) == 0
}

func (v ProjectGateCommands) Validate() error {
	for name, command := range map[string]ProjectGateCommand{"format": v.Format, "check": v.Check, "test.task": v.Test.Task} {
		if len(command.Command) == 0 || len(command.Command[0]) == 0 {
			return fmt.Errorf("invalid %s gate command", name)
		}
		for _, part := range command.Command {
			if part == "" || strings.ContainsAny(part, "\r\n\x00") {
				return fmt.Errorf("invalid %s gate command", name)
			}
		}
	}
	return nil
}

var workflowOperationClasses = map[string]string{
	"implementation": "task",
	"correction":     "task",
	"integration":    "task_merge",
	"release":        "release",
	"activation":     "activation",
}

type WorkflowPolicyAgent struct {
	WaitForCI bool `json:"wait_for_ci"`
}

type WorkflowPolicyCI struct {
	Task      string `json:"task"`
	TaskMerge string `json:"task_merge"`
	Release   string `json:"release"`
}

// ProjectWorkflowPolicy is the sole durable authority for project workflow
// stage, integration branch and CI behavior. It is revisioned independently
// from the project record so optimistic Hub writes protect policy changes.
type ProjectWorkflowPolicy struct {
	SchemaVersion     int                 `json:"schema_version"`
	ProjectID         string              `json:"project_id"`
	Revision          int                 `json:"revision"`
	WorkflowStage     string              `json:"workflow_stage"`
	IntegrationBranch string              `json:"integration_branch"`
	Agent             WorkflowPolicyAgent `json:"agent"`
	CI                WorkflowPolicyCI    `json:"ci"`
	UpdatedBy         string              `json:"updated_by"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

type EffectiveWorkflowPolicy struct {
	WorkflowPolicyRevision int    `json:"workflow_policy_revision"`
	OperationClass         string `json:"operation_class"`
	EffectiveCIField       string `json:"effective_ci_field"`
	EffectiveCIMode        string `json:"effective_ci_mode"`
	WaitForCI              bool   `json:"wait_for_ci"`
	CIBlocking             bool   `json:"ci_blocking"`
	AgentMayWait           bool   `json:"agent_may_wait"`
}

func StandardWorkflowGates() []string {
	return []string{WorkflowGateFormat, WorkflowGateCheck, WorkflowGateTest}
}

func ValidateWorkflowGates(gates []string) error {
	seen := map[string]bool{}
	for _, gate := range gates {
		switch gate {
		case WorkflowGateFormat, WorkflowGateCheck, WorkflowGateTest:
		default:
			return fmt.Errorf("invalid workflow gate %q", gate)
		}
		if seen[gate] {
			return fmt.Errorf("duplicate workflow gate %q", gate)
		}
		seen[gate] = true
	}
	return nil
}

func EffectiveWorkflowGates(gates []string) []string {
	if len(gates) == 0 {
		return StandardWorkflowGates()
	}
	return append([]string{}, gates...)
}

func EffectiveProjectWorkflowGates(gates []string) []string {
	selected := EffectiveWorkflowGates(gates)
	seen := map[string]bool{}
	for _, gate := range selected {
		seen[gate] = true
	}
	seen[WorkflowGateCheck] = true
	resolved := make([]string, 0, len(seen))
	for _, gate := range StandardWorkflowGates() {
		if seen[gate] {
			resolved = append(resolved, gate)
		}
	}
	return resolved
}

func ValidateServerGateEvidence(results []CompletionGateResult) error {
	seen := map[string]bool{}
	for _, result := range results {
		if result.DurationMS < 0 || result.AggregateMS < 0 || len(result.Warnings) > 8 {
			return fmt.Errorf("invalid server gate timing evidence")
		}
		for _, warning := range result.Warnings {
			if len(warning) == 0 || len(warning) > 256 {
				return fmt.Errorf("invalid server gate warning evidence")
			}
		}
		if result.ID != WorkflowGateFormat && result.ID != WorkflowGateCheck && result.ID != WorkflowGateTest {
			return fmt.Errorf("invalid server gate evidence %q", result.ID)
		}
		if seen[result.ID] {
			return fmt.Errorf("duplicate server gate evidence %q", result.ID)
		}
		if result.Execution != "" && result.Execution != "executed" && result.Execution != "reused" {
			return fmt.Errorf("invalid server gate execution %q", result.Execution)
		}
		if result.Execution == "reused" && (result.TreeID == "" || result.ContractDigest == "" || result.ReceiptDigest == "") {
			return fmt.Errorf("reused server gate evidence is missing receipt identity")
		}
		seen[result.ID] = true
	}
	return nil
}

func ValidateProjectWorkflowPolicy(v ProjectWorkflowPolicy) error {
	if v.SchemaVersion != SchemaVersion || !idRE.MatchString(v.ProjectID) || v.Revision < 1 {
		return fmt.Errorf("invalid workflow policy identity")
	}
	switch v.WorkflowStage {
	case WorkflowStageTransitionalMain:
		if v.IntegrationBranch != "main" {
			return fmt.Errorf("transitional_main requires integration branch main")
		}
	case WorkflowStageDevelopActive:
		if v.IntegrationBranch != "develop" {
			return fmt.Errorf("develop_active requires integration branch develop")
		}
	default:
		return fmt.Errorf("invalid workflow stage")
	}
	for name, mode := range map[string]string{"task": v.CI.Task, "task_merge": v.CI.TaskMerge, "release": v.CI.Release} {
		if mode != WorkflowCIModeDisabled && mode != WorkflowCIModeObserve && mode != WorkflowCIModeRequire {
			return fmt.Errorf("invalid %s CI mode", name)
		}
	}
	if strings.TrimSpace(v.UpdatedBy) == "" || strings.ContainsAny(v.UpdatedBy, "\r\n\x00") || v.UpdatedAt.IsZero() {
		return fmt.Errorf("invalid workflow policy update metadata")
	}
	return nil
}

func WorkflowPolicyForOperation(policy ProjectWorkflowPolicy, operationClass string) (EffectiveWorkflowPolicy, error) {
	if err := ValidateProjectWorkflowPolicy(policy); err != nil {
		return EffectiveWorkflowPolicy{}, err
	}
	field, ok := workflowOperationClasses[operationClass]
	if !ok {
		return EffectiveWorkflowPolicy{}, fmt.Errorf("invalid operation class")
	}
	if operationClass == "activation" {
		return EffectiveWorkflowPolicy{
			WorkflowPolicyRevision: policy.Revision,
			OperationClass:         operationClass,
			EffectiveCIField:       "activation",
			EffectiveCIMode:        WorkflowCIModeDisabled,
			WaitForCI:              false,
			CIBlocking:             false,
			AgentMayWait:           false,
		}, nil
	}
	mode := policy.CI.Task
	switch field {
	case "task_merge":
		mode = policy.CI.TaskMerge
	case "release":
		mode = policy.CI.Release
	}
	blocking := mode == WorkflowCIModeRequire
	wait := policy.Agent.WaitForCI && blocking
	return EffectiveWorkflowPolicy{
		WorkflowPolicyRevision: policy.Revision,
		OperationClass:         operationClass,
		EffectiveCIField:       field,
		EffectiveCIMode:        mode,
		WaitForCI:              wait,
		CIBlocking:             blocking,
		AgentMayWait:           wait,
	}, nil
}

func ValidateEffectiveWorkflowPolicy(v EffectiveWorkflowPolicy) error {
	if v.WorkflowPolicyRevision < 1 {
		return fmt.Errorf("invalid effective workflow policy")
	}
	if _, ok := workflowOperationClasses[v.OperationClass]; !ok {
		return fmt.Errorf("invalid effective workflow policy")
	}
	if v.EffectiveCIField != workflowOperationClasses[v.OperationClass] {
		return fmt.Errorf("effective CI field does not match operation class")
	}
	if v.EffectiveCIMode != WorkflowCIModeDisabled && v.EffectiveCIMode != WorkflowCIModeObserve && v.EffectiveCIMode != WorkflowCIModeRequire {
		return fmt.Errorf("invalid effective CI mode")
	}
	if v.CIBlocking != (v.EffectiveCIMode == WorkflowCIModeRequire) || v.WaitForCI != v.AgentMayWait || (v.WaitForCI && !v.CIBlocking) {
		return fmt.Errorf("invalid effective CI wait policy")
	}
	return nil
}

func ValidateOperationClass(value string) error {
	if _, ok := workflowOperationClasses[value]; !ok {
		return fmt.Errorf("invalid operation class")
	}
	return nil
}

func WorkflowOperationClasses() []string {
	return []string{"implementation", "correction", "integration", "release", "activation"}
}
