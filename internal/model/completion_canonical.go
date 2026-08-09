package model

import (
	"encoding/json"
)

func CanonicalCompletion(c Completion) Completion {
	if c.GateResults == nil {
		c.GateResults = []CompletionGateResult{}
	}
	if c.AcceptanceCoverage == nil {
		c.AcceptanceCoverage = []string{}
	}
	if c.Deviations == nil {
		c.Deviations = []string{}
	}
	if c.RemainingRisks == nil {
		c.RemainingRisks = []string{}
	}
	if c.AgentFeedback != nil {
		feedback := *c.AgentFeedback
		feedback.Friction = append([]string{}, feedback.Friction...)
		feedback.Improvements = append([]string{}, feedback.Improvements...)
		feedback.ToolCandidates = append([]AgentFeedbackToolCandidate{}, feedback.ToolCandidates...)
		c.AgentFeedback = &feedback
	}
	return c
}

func CompletionJSON(c Completion) ([]byte, error) { return json.Marshal(CanonicalCompletion(c)) }
