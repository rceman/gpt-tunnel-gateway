package model

import (
	"strings"
	"testing"
	"time"
)

func TestValidateReportUsesRunExecutionBaseWhenTaskBaseIsStale(t *testing.T) {
	task := Task{
		ID:                 "EXM-TSK1",
		ProjectID:          "example",
		BaseRevision:       strings.Repeat("a", 40),
		RequiredGates:      []string{},
		AcceptanceCriteria: []string{"proof"},
	}
	task.SHA256 = strings.Repeat("c", 64)
	run := Run{
		ID:           "EXM-TSK1-RUN1",
		TaskID:       task.ID,
		TaskSHA256:   task.SHA256,
		ProjectID:    task.ProjectID,
		Branch:       "task/EXM-TSK1-proof",
		BaseRevision: strings.Repeat("b", 40),
	}
	report := Report{
		SchemaVersion: SchemaVersion,
		TaskID:        task.ID,
		RunID:         run.ID,
		ProjectID:     task.ProjectID,
		Status:        "failed",
		Summary:       "bounded execution proof",
		FinishedAt:    time.Now().UTC(),
		Repository: RepositoryProof{
			Branch:       run.Branch,
			Head:         strings.Repeat("d", 40),
			BaseAncestor: true,
			DiffScope:    run.BaseRevision + ".." + strings.Repeat("d", 40),
		},
	}
	if err := ValidateReport(report, task, run); err != nil {
		t.Fatalf("run execution base was rejected because task base is stale: %v", err)
	}
}
