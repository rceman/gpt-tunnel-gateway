package model

import (
	"fmt"
	"strings"
)

const (
	MaxAgentFeedbackSummaryBytes   = 1024
	MaxAgentFeedbackEntryBytes     = 512
	MaxAgentFeedbackCandidateBytes = 512
	MaxAgentFeedbackCandidates     = 3
	MaxAgentFeedbackFriction       = 3
	MaxAgentFeedbackImprovements   = 3
)

// AgentFeedback is optional advisory evidence attached to an immutable Agent
// Run report. It never participates in gates, Delivery authority, or lifecycle
// decisions.
type AgentFeedback struct {
	Summary        string                       `json:"summary,omitempty"`
	Friction       []string                     `json:"friction"`
	Improvements   []string                     `json:"improvements"`
	ToolCandidates []AgentFeedbackToolCandidate `json:"tool_candidates"`
	NoneObserved   bool                         `json:"none_observed"`
}

type AgentFeedbackToolCandidate struct {
	Problem        string `json:"problem"`
	ProposedTool   string `json:"proposed_tool"`
	ExpectedReuse  string `json:"expected_reuse"`
	ExpectedSaving string `json:"expected_saving"`
	SafetyBoundary string `json:"safety_boundary"`
}

func validateAgentFeedbackText(value, field string, max int) error {
	if err := utf8Bounded(value, max, field); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must be non-empty", field)
	}
	return nil
}

func ValidateAgentFeedback(value AgentFeedback) error {
	if len(value.Friction) > MaxAgentFeedbackFriction || len(value.Improvements) > MaxAgentFeedbackImprovements || len(value.ToolCandidates) > MaxAgentFeedbackCandidates {
		return fmt.Errorf("agent_feedback bounds exceeded")
	}
	for i, item := range value.Friction {
		if err := validateAgentFeedbackText(item, fmt.Sprintf("agent_feedback.friction[%d]", i), MaxAgentFeedbackEntryBytes); err != nil {
			return err
		}
	}
	for i, item := range value.Improvements {
		if err := validateAgentFeedbackText(item, fmt.Sprintf("agent_feedback.improvements[%d]", i), MaxAgentFeedbackEntryBytes); err != nil {
			return err
		}
	}
	for i, candidate := range value.ToolCandidates {
		prefix := fmt.Sprintf("agent_feedback.tool_candidates[%d]", i)
		for field, text := range map[string]string{
			"problem": candidate.Problem, "proposed_tool": candidate.ProposedTool,
			"expected_saving": candidate.ExpectedSaving, "safety_boundary": candidate.SafetyBoundary,
		} {
			if err := validateAgentFeedbackText(text, prefix+"."+field, MaxAgentFeedbackCandidateBytes); err != nil {
				return err
			}
		}
		if candidate.ExpectedReuse != "one_off" && candidate.ExpectedReuse != "occasional" && candidate.ExpectedReuse != "recurring" {
			return fmt.Errorf("%s.expected_reuse is invalid", prefix)
		}
	}
	if value.NoneObserved && (len(value.Friction) != 0 || len(value.Improvements) != 0 || len(value.ToolCandidates) != 0) {
		return fmt.Errorf("agent_feedback.none_observed contradicts feedback entries")
	}
	return nil
}

func agentFeedbackString(obj map[string]any, name string) (string, error) {
	value, ok := obj[name].(string)
	if !ok {
		return "", fmt.Errorf("agent_feedback.%s must be a string", name)
	}
	return value, nil
}

func agentFeedbackStringArray(obj map[string]any, name string) ([]string, error) {
	items, ok := obj[name].([]any)
	if !ok {
		return nil, fmt.Errorf("agent_feedback.%s must be an array", name)
	}
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("agent_feedback.%s must contain strings", name)
		}
		values = append(values, value)
	}
	return values, nil
}

func ParseAgentFeedback(data []byte) (AgentFeedback, error) {
	obj, err := strictJSONObject(data)
	if err != nil {
		return AgentFeedback{}, err
	}
	allowed := map[string]bool{"summary": true, "friction": true, "improvements": true, "tool_candidates": true, "none_observed": true}
	for key := range obj {
		if !allowed[key] {
			return AgentFeedback{}, fmt.Errorf("unknown agent_feedback field %q", key)
		}
	}
	for _, key := range []string{"friction", "improvements", "tool_candidates", "none_observed"} {
		if _, ok := obj[key]; !ok {
			return AgentFeedback{}, fmt.Errorf("missing agent_feedback field %q", key)
		}
	}
	var summary string
	if _, present := obj["summary"]; present {
		summary, err = agentFeedbackString(obj, "summary")
		if err != nil {
			return AgentFeedback{}, err
		}
		if err := validateAgentFeedbackText(summary, "agent_feedback.summary", MaxAgentFeedbackSummaryBytes); err != nil {
			return AgentFeedback{}, err
		}
	}
	friction, err := agentFeedbackStringArray(obj, "friction")
	if err != nil {
		return AgentFeedback{}, err
	}
	improvements, err := agentFeedbackStringArray(obj, "improvements")
	if err != nil {
		return AgentFeedback{}, err
	}
	noneObserved, ok := obj["none_observed"].(bool)
	if !ok {
		return AgentFeedback{}, fmt.Errorf("agent_feedback.none_observed must be a boolean")
	}
	items, ok := obj["tool_candidates"].([]any)
	if !ok {
		return AgentFeedback{}, fmt.Errorf("agent_feedback.tool_candidates must be an array")
	}
	candidates := make([]AgentFeedbackToolCandidate, 0, len(items))
	for i, item := range items {
		candidate, ok := item.(map[string]any)
		if !ok {
			return AgentFeedback{}, fmt.Errorf("agent_feedback.tool_candidates[%d] must be an object", i)
		}
		if len(candidate) != 5 {
			return AgentFeedback{}, fmt.Errorf("agent_feedback.tool_candidates[%d] has unknown or missing fields", i)
		}
		for _, key := range []string{"problem", "proposed_tool", "expected_reuse", "expected_saving", "safety_boundary"} {
			if _, ok := candidate[key]; !ok {
				return AgentFeedback{}, fmt.Errorf("agent_feedback.tool_candidates[%d] missing field %q", i, key)
			}
		}
		problem, err := agentFeedbackString(candidate, "problem")
		if err != nil {
			return AgentFeedback{}, err
		}
		proposedTool, err := agentFeedbackString(candidate, "proposed_tool")
		if err != nil {
			return AgentFeedback{}, err
		}
		expectedReuse, err := agentFeedbackString(candidate, "expected_reuse")
		if err != nil {
			return AgentFeedback{}, err
		}
		expectedSaving, err := agentFeedbackString(candidate, "expected_saving")
		if err != nil {
			return AgentFeedback{}, err
		}
		safetyBoundary, err := agentFeedbackString(candidate, "safety_boundary")
		if err != nil {
			return AgentFeedback{}, err
		}
		candidates = append(candidates, AgentFeedbackToolCandidate{
			Problem:        problem,
			ProposedTool:   proposedTool,
			ExpectedReuse:  expectedReuse,
			ExpectedSaving: expectedSaving,
			SafetyBoundary: safetyBoundary,
		})
	}
	value := AgentFeedback{
		Summary:        summary,
		Friction:       friction,
		Improvements:   improvements,
		ToolCandidates: candidates,
		NoneObserved:   noneObserved,
	}
	if err := ValidateAgentFeedback(value); err != nil {
		return AgentFeedback{}, err
	}
	return value, nil
}

func (v *AgentFeedback) UnmarshalJSON(data []byte) error {
	value, err := ParseAgentFeedback(data)
	if err != nil {
		return err
	}
	*v = value
	return nil
}
