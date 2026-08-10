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
	Gates             []string            `json:"gates,omitempty"`
	UpdatedBy         string              `json:"updated_by"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

type EffectiveWorkflowPolicy struct {
	WorkflowPolicyRevision int      `json:"workflow_policy_revision"`
	OperationClass         string   `json:"operation_class"`
	EffectiveCIField       string   `json:"effective_ci_field"`
	EffectiveCIMode        string   `json:"effective_ci_mode"`
	WaitForCI              bool     `json:"wait_for_ci"`
	CIBlocking             bool     `json:"ci_blocking"`
	AgentMayWait           bool     `json:"agent_may_wait"`
	Gates                  []string `json:"gates"`
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
		if result.ID != WorkflowGateFormat && result.ID != WorkflowGateCheck && result.ID != WorkflowGateTest {
			return fmt.Errorf("invalid server gate evidence %q", result.ID)
		}
		if seen[result.ID] {
			return fmt.Errorf("duplicate server gate evidence %q", result.ID)
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
	if err := ValidateWorkflowGates(v.Gates); err != nil {
		return err
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
			Gates:                  EffectiveProjectWorkflowGates(policy.Gates),
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
		Gates:                  EffectiveProjectWorkflowGates(policy.Gates),
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
