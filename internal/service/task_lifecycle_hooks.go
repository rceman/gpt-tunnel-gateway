package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/publicprojection"
)

const maxTaskLifecycleOperationResultBytes = 1 << 20

type procedureHookAttempt struct {
	Hook      string          `json:"hook"`
	Procedure string          `json:"procedure"`
	Phase     string          `json:"phase"`
	Outcome   string          `json:"outcome"`
	Reason    string          `json:"reason,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

func taskHookString(value any) string {
	text, _ := value.(string)
	return text
}

func projectProcedureHookInput(definition model.ProjectProcedureDefinition, payload map[string]any) map[string]any {
	properties, _ := definition.Input["properties"].(map[string]any)
	input := make(map[string]any, len(properties))
	for field := range properties {
		if value, exists := payload[field]; exists {
			input[field] = value
		}
	}
	return input
}

func taskLifecycleHookPayload(projectID, taskID, sessionID, operationID, stage string, taskRevision, executionRevision int, candidateHead, candidateTree, mainBase, outcome, integrationHead, status string) map[string]any {
	payload := map[string]any{
		"project": projectID, "task": taskID, "session": sessionID, "operation": operationID,
		"task_revision": taskRevision, "execution_revision": executionRevision, "candidate_head": taskHookFingerprint(projectID, candidateHead),
	}
	if stage != "" {
		payload["stage"] = stage
	}
	if candidateTree != "" {
		payload["candidate_tree"] = taskHookFingerprint(projectID, candidateTree)
	}
	if mainBase != "" {
		payload["main_base"] = taskHookFingerprint(projectID, mainBase)
	}
	if outcome != "" {
		payload["outcome"] = outcome
	}
	if integrationHead != "" {
		payload["integration_head"] = taskHookFingerprint(projectID, integrationHead)
	}
	if status != "" {
		payload["status"] = status
	}
	return payload
}

func taskHookFingerprint(projectID, commit string) string {
	if commit == "" {
		return ""
	}
	fingerprint, err := publicprojection.CompactGitFingerprint(projectID, commit)
	if err != nil {
		return ""
	}
	return fingerprint
}

func (s *Service) runTaskLifecycleProcedureHook(ctx context.Context, hook, phase string, payload map[string]any, parentStatus string, parentResult json.RawMessage, parentError string) error {
	_, err := s.runTaskLifecycleProcedureHookWithOutput(ctx, hook, phase, payload, parentStatus, parentResult, parentError, "")
	return err
}

func (s *Service) runTaskLifecycleProcedureHookWithOutput(ctx context.Context, hook, phase string, payload map[string]any, parentStatus string, parentResult json.RawMessage, parentError, executionRoot string) (json.RawMessage, error) {
	hookCtx := ctx
	if phase == "after" {
		var cancel context.CancelFunc
		hookCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), procedureExecutionTimeout)
		defer cancel()
	}
	projectID := taskHookString(payload["project"])
	parentID := taskHookString(payload["operation"])
	sessionID := taskHookString(payload["session"])
	configuration, err := s.ProjectConfigurationRead(hookCtx, projectID)
	if err != nil {
		if phase == "before" {
			return nil, fmt.Errorf("pre-transition Hook configuration is unavailable")
		}
		return nil, nil
	}
	procedureName, bound := configuration.Hooks[hook]
	if !bound {
		return nil, nil
	}
	if phase != "before" && phase != "after" || model.ValidateOperationID(parentID) != nil || sessionID == "" {
		if phase == "before" {
			return nil, fmt.Errorf("pre-transition Hook context is invalid")
		}
		return nil, nil
	}
	definition, found := configuration.Procedures[procedureName]
	if !found {
		if phase == "before" {
			return nil, fmt.Errorf("pre-transition Hook Procedure is unavailable")
		}
		return nil, nil
	}
	if phase == "after" {
		if err := s.persistTaskLifecycleParentOutcome(parentID, parentStatus, parentResult, parentError); err != nil {
			return nil, nil
		}
	}
	attempt := procedureHookAttempt{
		Hook:      hook,
		Procedure: procedureName,
		Phase:     phase,
		Outcome:   "pending",
	}
	previous, err := s.readTaskLifecycleHookAttempt(parentID, hook)
	if err == nil {
		switch previous.Outcome {
		case "completed":
			if phase == "before" && hook == model.HookPreTaskVerify {
				if !json.Valid(previous.Output) {
					return nil, fmt.Errorf("pre-transition Hook Procedure output is unavailable")
				}
				return append(json.RawMessage(nil), previous.Output...), nil
			}
			return nil, nil
		case "failed", "outcome_unknown":
			if phase == "before" {
				return nil, fmt.Errorf("pre-transition Hook Procedure did not complete")
			}
			return nil, nil
		case "pending":
			if phase == "before" {
				return nil, fmt.Errorf("pre-transition Hook Procedure outcome is unknown")
			}
			return nil, nil
		}
	}
	if err := s.writeTaskLifecycleHookAttempt(parentID, attempt); err != nil {
		if phase == "before" {
			return nil, fmt.Errorf("pre-transition Hook attempt could not be durably recorded")
		}
		return nil, nil
	}
	output, outcomeErr := s.executeProjectHookProcedure(hookCtx, projectID, sessionID, parentID, hook, phase, configuration, procedureName, definition, payload, executionRoot)
	if outcomeErr == nil {
		attempt.Outcome = "completed"
		if phase == "before" && hook == model.HookPreTaskVerify {
			attempt.Output = append(json.RawMessage(nil), output...)
		}
	} else {
		attempt.Outcome = "failed"
		if procedureErr, ok := outcomeErr.(*procedureExecutionError); ok && procedureErr.OutcomeUnknown {
			attempt.Outcome = "outcome_unknown"
		}
		attempt.Reason = "Procedure Hook execution did not complete"
	}
	if err := s.writeTaskLifecycleHookAttempt(parentID, attempt); err != nil {
		attempt.Outcome = "outcome_unknown"
		attempt.Reason = "Procedure Hook outcome could not be durably recorded"
		_ = s.writeTaskLifecycleHookAttempt(parentID, attempt)
	}
	if phase == "before" && attempt.Outcome != "completed" {
		return nil, fmt.Errorf("pre-transition Hook Procedure did not complete")
	}
	return output, nil
}

func (s *Service) executeProjectHookProcedure(ctx context.Context, projectID, sessionID, parentID, hook, phase string, configuration model.ProjectConfiguration, name string, definition model.ProjectProcedureDefinition, payload map[string]any, executionRoot string) (json.RawMessage, error) {
	compiled, err := compileProcedureContract(name, definition)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure contract is invalid"}
	}
	input := projectProcedureHookInput(definition, payload)
	inputValue, err := decodeProcedureValue(input)
	if err != nil || actioncontract.ValidateCompiledActionInput(compiled, inputValue) != nil {
		return nil, &procedureExecutionError{Reason: "Procedure Hook input is invalid"}
	}
	resolved, err := s.resolveProcedureReferences(ctx, projectID, sessionID, parentID, definition.Input, inputValue)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure Hook references are invalid"}
	}
	project, err := s.EffectiveProjectConfig(projectID)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure execution root is unavailable"}
	}
	runRoot := project.Root
	if executionRoot != "" {
		if !filepath.IsAbs(executionRoot) {
			return nil, &procedureExecutionError{Reason: "Procedure execution root is unavailable"}
		}
		runRoot = filepath.Clean(executionRoot)
	}
	if _, _, err := resolveProcedureScript(runRoot, definition.Script); err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure executable is unavailable"}
	}
	contextValue := ProcedureExecutionContext{
		Project:               projectID,
		Operation:             parentID,
		Session:               sessionID,
		Hook:                  hook,
		ConfigurationRevision: configuration.Revision,
		ResolvedReferences:    resolved,
	}
	inputJSON, err := json.Marshal(inputValue)
	if err != nil || len(inputJSON) > maxProcedureInputBytes {
		return nil, &procedureExecutionError{Reason: "Procedure Hook input exceeds its bounds"}
	}
	envelope, err := json.Marshal(ProcedureInputEnvelope{
		Input:   inputJSON,
		Context: contextValue,
	})
	if err != nil || len(envelope) > maxProcedureInputBytes+maxProcedureOutputBytes {
		return nil, &procedureExecutionError{Reason: "Procedure Hook context exceeds its bounds"}
	}
	runtimeID, err := newProcedureRuntimeID()
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure runtime identity is unavailable"}
	}
	root, script, err := resolveProcedureScript(runRoot, definition.Script)
	if err != nil {
		return nil, &procedureExecutionError{Reason: "Procedure executable is unavailable"}
	}
	current, err := s.ProjectConfigurationRead(ctx, projectID)
	if err != nil || current.Revision != configuration.Revision || !reflect.DeepEqual(current.Procedures[name], definition) || current.Hooks[hook] != name {
		return nil, &procedureExecutionError{Reason: "Procedure configuration changed before Hook launch"}
	}
	scriptParent := ctx
	if phase == "after" {
		scriptParent = context.WithoutCancel(ctx)
	}
	scriptCtx, cancel := context.WithTimeout(scriptParent, procedureExecutionTimeout)
	defer cancel()
	output, err := executeProcedureScript(scriptCtx, s.Config.StateDir, runtimeID, root, script, envelope)
	if err != nil {
		return nil, err
	}
	outputValue, err := decodeProcedureJSON(output)
	resultSchema, validResult := procedureCompletedResultSchema(compiled.Output)
	outputAction := compiled
	if validResult {
		outputAction.Output = resultSchema
	}
	if err != nil || !validResult || actioncontract.ValidateCompiledActionOutput(outputAction, outputValue) != nil {
		return nil, &procedureExecutionError{
			Reason:         "Procedure Hook output is invalid",
			OutcomeUnknown: true,
		}
	}
	if _, err := s.resolveProcedureReferences(ctx, projectID, sessionID, parentID, definition.Output, outputValue); err != nil {
		return nil, &procedureExecutionError{
			Reason:         "Procedure Hook output references are invalid",
			OutcomeUnknown: true,
		}
	}
	return append(json.RawMessage(nil), output...), nil
}

func decodeProcedureValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return decodeProcedureJSON(encoded)
}

func (s *Service) persistTaskLifecycleParentOutcome(operationID, status string, result json.RawMessage, errorText string) error {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return err
	}
	operation.Status = status
	operation.Error = errorText
	operation.UpdatedAt = s.durableNow()
	operation.Result = mergeTaskLifecycleHookAttempts(result, taskLifecycleHookAttempts(operation.Result))
	if len(operation.Result) > maxTaskLifecycleOperationResultBytes {
		return fmt.Errorf("Procedure Hook evidence exceeds its durable Operation bound")
	}
	return s.writeDurableMutation(operation)
}

func (s *Service) readTaskLifecycleHookAttempt(operationID, hook string) (procedureHookAttempt, error) {
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return procedureHookAttempt{}, err
	}
	for _, attempt := range taskLifecycleHookAttempts(operation.Result) {
		if attempt.Hook == hook {
			return attempt, nil
		}
	}
	return procedureHookAttempt{}, fmt.Errorf("Hook attempt was not recorded")
}

func (s *Service) writeTaskLifecycleHookAttempt(operationID string, attempt procedureHookAttempt) error {
	if len(attempt.Output) > maxProcedureOutputBytes || len(attempt.Output) > 0 && !json.Valid(attempt.Output) {
		return fmt.Errorf("Procedure Hook output evidence is invalid or exceeds its bound")
	}
	operation, err := s.readDurableMutation(operationID)
	if err != nil {
		return err
	}
	attempts := taskLifecycleHookAttempts(operation.Result)
	found := false
	for index := range attempts {
		if attempts[index].Hook == attempt.Hook {
			attempts[index] = attempt
			found = true
			break
		}
	}
	if !found {
		if len(attempts) >= 8 {
			return fmt.Errorf("Procedure Hook attempt evidence exceeds its bound")
		}
		attempts = append(attempts, attempt)
	}
	operation.Result = mergeTaskLifecycleHookAttempts(operation.Result, attempts)
	if len(operation.Result) > maxTaskLifecycleOperationResultBytes {
		return fmt.Errorf("Procedure Hook evidence exceeds its durable Operation bound")
	}
	operation.UpdatedAt = s.durableNow()
	return s.writeDurableMutation(operation)
}

func taskLifecycleHookAttempts(raw json.RawMessage) []procedureHookAttempt {
	var result struct {
		Attempts []procedureHookAttempt `json:"hook_attempts"`
	}
	_ = json.Unmarshal(raw, &result)
	if len(result.Attempts) > 8 {
		return nil
	}
	return result.Attempts
}

func mergeTaskLifecycleHookAttempts(raw json.RawMessage, attempts []procedureHookAttempt) json.RawMessage {
	if len(attempts) == 0 {
		return raw
	}
	var fields map[string]json.RawMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &fields)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(attempts)
	if err != nil {
		return raw
	}
	fields["hook_attempts"] = encoded
	merged, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return merged
}

func pendingTaskLifecycleHookPhase(operation durableMutationOperation) string {
	for _, attempt := range taskLifecycleHookAttempts(operation.Result) {
		if attempt.Outcome == "pending" {
			return attempt.Phase
		}
	}
	return ""
}

func reconcilePendingTaskLifecycleHook(operation durableMutationOperation) (durableMutationOperation, bool) {
	attempts := taskLifecycleHookAttempts(operation.Result)
	changed := false
	for index := range attempts {
		if attempts[index].Outcome != "pending" {
			continue
		}
		attempts[index].Outcome = "outcome_unknown"
		attempts[index].Reason = "Procedure Hook execution was not replayed after Gateway restart"
		changed = true
	}
	if changed {
		operation.Result = mergeTaskLifecycleHookAttempts(operation.Result, attempts)
		operation.UpdatedAt = time.Now().UTC()
	}
	return operation, changed
}
