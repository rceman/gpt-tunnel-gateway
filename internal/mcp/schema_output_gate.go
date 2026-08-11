package mcp

import "github.com/rceman/gpt-tunnel-gateway/internal/model"

// completionGateResultOutputSchema is the single output contract for the
// model.CompletionGateResult shape. The id schema is supplied by the caller
// because ordinary completion gates accept any declared gate id while
// server-owned workflow gates use a closed enum.
func completionGateResultOutputSchema(id map[string]any) map[string]any {
	return closedOutput(map[string]any{
		"id":              id,
		"exit_code":       outputInteger(),
		"execution":       outputEnum("executed", "reused"),
		"tree_id":         outputString(),
		"contract_digest": outputString(),
		"receipt_digest":  outputString(),
	}, "id", "exit_code")
}

func completionGateResultAnyIDOutputSchema() map[string]any {
	return completionGateResultOutputSchema(outputString())
}

func completionGateResultWorkflowIDOutputSchema() map[string]any {
	return completionGateResultOutputSchema(outputEnum(model.WorkflowGateFormat, model.WorkflowGateCheck, model.WorkflowGateTest))
}
