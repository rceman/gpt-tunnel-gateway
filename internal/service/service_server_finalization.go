package service

import (
	"fmt"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func serverOwnedCompletion(run model.Run, task model.Task, summary string, feedback *model.AgentFeedback, serverGates []model.CompletionGateResult, risks []string) (model.Completion, error) {
	if len(task.RequiredGates) > 0 && len(serverGates) != len(task.RequiredGates) {
		return model.Completion{}, fmt.Errorf("server gate evidence does not match task gate count")
	}
	gates := make([]model.CompletionGateResult, 0, len(task.RequiredGates))
	for i := range task.RequiredGates {
		gate := serverGates[i]
		gates = append(gates, model.CompletionGateResult{ID: fmt.Sprintf("G%d", i+1), ExitCode: gate.ExitCode, Execution: gate.Execution, TreeID: gate.TreeID, ContractDigest: gate.ContractDigest, ReceiptDigest: gate.ReceiptDigest})
	}
	acceptance := make([]string, 0, len(task.AcceptanceCriteria))
	for i := range task.AcceptanceCriteria {
		acceptance = append(acceptance, fmt.Sprintf("AC%d", i+1))
	}
	completion := model.CanonicalCompletion(model.Completion{
		SchemaVersion: model.SchemaVersion, RunID: run.ID, TaskSHA256: task.SHA256,
		TaskRevision: run.TaskRevision, TaskRevisionSHA256: run.TaskRevisionSHA256, TaskRunNumber: run.TaskRunNumber,
		Status: "succeeded", Summary: summary, GateResults: gates, AcceptanceCoverage: acceptance,
		Deviations: []string{}, RemainingRisks: append([]string{}, risks...), AgentFeedback: feedback,
	})
	if err := model.ValidateCompletion(completion, task); err != nil {
		return model.Completion{}, fmt.Errorf("server-owned completion is invalid: %w", err)
	}
	return completion, nil
}
