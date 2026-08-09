package model

import (
	"fmt"
	"strings"
)

func ValidateCompletion(c Completion, task Task) error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("invalid completion identity")
	}
	if ValidateCanonicalRunID(c.RunID) != nil {
		revisionID, runNumber, err := ParseTaskRevisionRunID(c.RunID)
		if err != nil {
			return fmt.Errorf("invalid completion identity")
		}
		taskID, revision, err := ParseTaskRevisionID(revisionID)
		if err != nil || taskID != task.ID || c.TaskRevision != revision || c.TaskRunNumber != runNumber || c.TaskRevisionSHA256 == "" || !completionHashRE.MatchString(c.TaskRevisionSHA256) {
			return fmt.Errorf("invalid completion revision identity")
		}
	} else if c.TaskRevision != 0 || c.TaskRevisionSHA256 != "" || c.TaskRunNumber != 0 {
		return fmt.Errorf("legacy completion cannot contain revision binding")
	}
	if !completionHashRE.MatchString(c.TaskSHA256) || strings.ToLower(c.TaskSHA256) != c.TaskSHA256 || c.TaskSHA256 != task.SHA256 {
		return fmt.Errorf("completion task hash mismatch")
	}
	switch c.Status {
	case "succeeded", "failed", "needs_gpt_revision":
	default:
		return fmt.Errorf("invalid completion status")
	}
	if err := utf8Bounded(c.Summary, 4096, "summary"); err != nil {
		return err
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("summary must be non-empty")
	}
	if len(c.GateResults) > 128 || len(c.AcceptanceCoverage) > 128 || len(c.Deviations) > 64 || len(c.RemainingRisks) > 64 {
		return fmt.Errorf("completion bounds exceeded")
	}
	for i, gate := range c.GateResults {
		want := fmt.Sprintf("G%d", i+1)
		if gate.ID != want || !completionGateRE.MatchString(gate.ID) {
			return fmt.Errorf("gate results must be the ordered positional sequence")
		}
	}
	seen := map[string]bool{}
	for _, id := range c.AcceptanceCoverage {
		if !completionACRE.MatchString(id) || seen[id] {
			return fmt.Errorf("acceptance coverage must be ordered and unique")
		}
		seen[id] = true
	}
	if c.Status == "succeeded" {
		if len(c.GateResults) != len(task.RequiredGates) || len(c.AcceptanceCoverage) != len(task.AcceptanceCriteria) {
			return fmt.Errorf("successful completion must report every gate and criterion")
		}
		for _, gate := range c.GateResults {
			if gate.ExitCode != 0 {
				return fmt.Errorf("successful completion contains failed gate %s", gate.ID)
			}
		}
		for i := range c.AcceptanceCoverage {
			if c.AcceptanceCoverage[i] != fmt.Sprintf("AC%d", i+1) {
				return fmt.Errorf("successful acceptance coverage must be positional")
			}
		}
	} else {
		if len(c.GateResults) > len(task.RequiredGates) || len(c.AcceptanceCoverage) > len(task.AcceptanceCriteria) {
			return fmt.Errorf("completion exceeds task bounds")
		}
		for i, gate := range c.GateResults {
			if gate.ID != fmt.Sprintf("G%d", i+1) {
				return fmt.Errorf("gate prefix is not positional")
			}
		}
		last := 0
		for _, id := range c.AcceptanceCoverage {
			var n int
			_, _ = fmt.Sscanf(id, "AC%d", &n)
			if n <= last || n < 1 || n > len(task.AcceptanceCriteria) {
				return fmt.Errorf("acceptance coverage is not an ordered bounded subset")
			}
			last = n
		}
	}
	for _, v := range append(append([]string{}, c.Deviations...), c.RemainingRisks...) {
		if err := utf8Bounded(v, 2048, "completion entry"); err != nil {
			return err
		}
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("completion entry must be non-empty")
		}
	}
	if c.AgentFeedback != nil {
		if err := ValidateAgentFeedback(*c.AgentFeedback); err != nil {
			return err
		}
	}
	return nil
}
