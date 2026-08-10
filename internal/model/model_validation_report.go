package model

import (
	"fmt"
	"strings"
)

func ValidateReport(v Report, task Task, run Run, limits ...int) error {
	limit := 10000
	if len(limits) > 0 && limits[0] > 0 {
		limit = limits[0]
	}
	if v.SchemaVersion != SchemaVersion || v.TaskID != task.ID || v.RunID != run.ID || v.ProjectID != run.ProjectID || v.FinishedAt.IsZero() {
		return fmt.Errorf("report identity mismatch")
	}
	if run.TaskRevision != 0 && (v.TaskRevision != run.TaskRevision || v.TaskRevisionSHA256 != run.TaskRevisionSHA256 || v.TaskRunNumber != run.TaskRunNumber) {
		return fmt.Errorf("report revision-aware binding mismatch")
	}
	if v.Status != "succeeded" && v.Status != "failed" && v.Status != "needs_gpt_revision" {
		return fmt.Errorf("invalid report status")
	}
	if v.AgentFeedback != nil {
		if err := ValidateAgentFeedback(*v.AgentFeedback); err != nil {
			return err
		}
	}
	if err := utf8Bounded(v.Summary, 4096, "report summary"); err != nil {
		return err
	}
	if strings.TrimSpace(v.Summary) == "" {
		return fmt.Errorf("report summary must be non-empty")
	}
	if len(v.GateResults) > 128 || len(v.AcceptanceCoverage) > 128 || len(v.Deviations) > 64 || len(v.RemainingRisks) > 64 {
		return fmt.Errorf("report bounds exceeded")
	}
	if len(v.ServerGateResults) > 0 {
		if err := ValidateServerGateEvidence(v.ServerGateResults); err != nil {
			return err
		}
	}
	for i, gate := range v.GateResults {
		if gate.ID != fmt.Sprintf("G%d", i+1) {
			return fmt.Errorf("report gate results are not positional")
		}
	}
	if v.Status == "succeeded" {
		if len(v.GateResults) != len(task.RequiredGates) || len(v.AcceptanceCoverage) != len(task.AcceptanceCriteria) {
			return fmt.Errorf("report success receipts are incomplete")
		}
		for _, gate := range v.GateResults {
			if gate.ExitCode != 0 {
				return fmt.Errorf("report success contains failed gate")
			}
		}
		for i, id := range v.AcceptanceCoverage {
			if id != fmt.Sprintf("AC%d", i+1) {
				return fmt.Errorf("report success acceptance is not positional")
			}
		}
	} else {
		if len(v.GateResults) > len(task.RequiredGates) || len(v.AcceptanceCoverage) > len(task.AcceptanceCriteria) {
			return fmt.Errorf("report receipts exceed task bounds")
		}
		last := 0
		seen := map[string]bool{}
		for _, id := range v.AcceptanceCoverage {
			if !completionACRE.MatchString(id) || seen[id] {
				return fmt.Errorf("report acceptance is not ordered and unique")
			}
			var n int
			_, _ = fmt.Sscanf(id, "AC%d", &n)
			if n <= last || n > len(task.AcceptanceCriteria) {
				return fmt.Errorf("report acceptance is out of bounds")
			}
			seen[id] = true
			last = n
		}
	}
	for _, entry := range append(append([]string{}, v.Deviations...), v.RemainingRisks...) {
		if err := utf8Bounded(entry, 2048, "report entry"); err != nil {
			return err
		}
		if strings.TrimSpace(entry) == "" {
			return fmt.Errorf("report entry must be non-empty")
		}
	}
	if ValidateBranch(v.Repository.Branch) != nil || !shaRE.MatchString(v.Repository.Head) || v.Repository.DiffScope != run.BaseRevision+".."+v.Repository.Head {
		return fmt.Errorf("invalid repository proof")
	}
	if len(v.Repository.Commits) > limit || len(v.Repository.ChangedFiles) > limit {
		return fmt.Errorf("repository proof exceeds configured limit")
	}
	seenCommits := map[string]bool{}
	for _, sha := range v.Repository.Commits {
		if !shaRE.MatchString(sha) || seenCommits[sha] {
			return fmt.Errorf("invalid or duplicate repository commit")
		}
		seenCommits[sha] = true
	}
	if len(v.Repository.Commits) > 0 && v.Repository.Commits[len(v.Repository.Commits)-1] != v.Repository.Head {
		return fmt.Errorf("repository commit order does not end at HEAD")
	}
	canonicalFiles := CanonicalStrings(v.Repository.ChangedFiles)
	if strings.Join(canonicalFiles, "\x00") != strings.Join(v.Repository.ChangedFiles, "\x00") {
		return fmt.Errorf("repository changed files are not canonical")
	}
	seenFiles := map[string]bool{}
	for _, path := range v.Repository.ChangedFiles {
		if seenFiles[path] {
			return fmt.Errorf("repository changed files contain duplicates")
		}
		seenFiles[path] = true
	}
	if v.Status == "succeeded" && (!v.Repository.BaseAncestor || !v.Repository.WorktreeClean || v.Repository.Branch != run.Branch) {
		return fmt.Errorf("successful report lacks base ancestry proof")
	}
	for _, path := range v.Repository.ChangedFiles {
		if err := ValidateRelativePath(path); err != nil {
			return err
		}
	}
	for _, sha := range v.Repository.Commits {
		if !shaRE.MatchString(sha) {
			return fmt.Errorf("invalid repository commit")
		}
	}
	return nil
}
