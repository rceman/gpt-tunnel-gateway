package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func addGenericTransportTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server, legacy map[string]Tool) {
	add("session_start", "Create an unbound durable session and return workflow bootstrap guidance.", sessionStartPublicInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.sessionStartPublic(ctx, raw)
	})
	add("call", "Dispatch one server-owned action through its authoritative schema and handler.", genericCallInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.genericCall(ctx, legacy, raw)
	})
	add("schema", "Discover generic action domains, action contracts, and the server-side schema revision.", genericSchemaInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.genericSchema(legacy, raw)
	})
	add("batch", "Dispatch ordered generic actions and return one result for every item, including failures.", genericBatchInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return s.genericBatch(ctx, legacy, raw)
	})
}

func (s *Server) genericCallWithEntries(ctx context.Context, entries map[string]genericActionEntry, raw json.RawMessage) (map[string]any, error) {
	var input genericCallInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	return s.genericDispatch(ctx, entries, durableSession.Record{}, input.Action, input.Input)
}

func (s *Server) genericDispatch(ctx context.Context, entries map[string]genericActionEntry, record durableSession.Record, action string, raw json.RawMessage) (result map[string]any, returnErr error) {
	if runtime_log.RequestID(ctx) == "" {
		ctx = runtime_log.WithRequestID(ctx, runtime_log.NewRequestID())
	}
	if operationID := operationIDFromRaw(raw); operationID != "" {
		ctx = runtime_log.WithOperationID(ctx, operationID)
	}
	started := false
	defer func() {
		if !started {
			return
		}
		event, level := "action_finish", "info"
		if returnErr != nil || (result != nil && result["is_error"] == true) {
			event, level = "action_failure", "warn"
		}
		s.recordRuntimeAction(ctx, record, event, level, action, returnErr, result)
	}()
	if action == "" {
		return genericActionError(action, "action is required; inspect schema with path=\"\""), nil
	}
	entry, ok := entries[action]
	if !ok {
		return genericActionError(action, fmt.Sprintf("unknown action %q; inspect schema with path=\"\"", action)), nil
	}
	if entry.SessionRequired && record.ID == "" {
		return genericActionError(action, "SESSION_REQUIRED: provide the public session field"), nil
	}
	if record.ID != "" && record.ProjectID == "" && !unboundActionAllowed(action) {
		return genericActionError(action, "PROJECT_BINDING_REQUIRED: bind the session before project work"), nil
	}
	if record.ID != "" {
		bootstrapContext := ctx
		if elevated, err := authority.BootstrapSessionAuthority(ctx); err == nil {
			bootstrapContext = elevated
		}
		if err := requireSessionRole(bootstrapContext, record.Role); err != nil {
			return genericActionError(action, err.Error()), nil
		}
		resolved, err := s.resolveSessionAuthority(bootstrapContext, record, actionAuthorityContract{
			Role:                   entry.AuthorityRole,
			RequiresWorkflowPolicy: entry.RequiresWorkflowPolicy,
		})
		if err != nil {
			return genericActionError(action, err.Error()), nil
		}
		ctx = resolved
		ctx = service.WithAgentSessionID(ctx, record.ID)
		if entry.SessionBound && action != "session/bind" {
			if err := validateGenericActionInput(entry.InputSchema, raw); err != nil {
				return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
			}
			raw, err = inheritSessionProject(entry.ExecutionInputSchema, record.ProjectID, raw)
			if err != nil {
				return genericActionError(action, err.Error()), nil
			}
		}
		if err := s.validateSessionRules(ctx, record, action); err != nil {
			return genericActionError(action, err.Error()), nil
		}
	}
	if entry.SessionBound {
		validationSchema := entry.InputSchema
		if action != "session/bind" && entry.ExecutionInputSchema != nil {
			validationSchema = entry.ExecutionInputSchema
		}
		if err := validateGenericActionInput(validationSchema, raw); err != nil {
			return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
		}
	}
	if entry.RouteLegacyByProjectModel && entry.LegacyExecute != nil {
		projectID := ""
		if record.ID != "" {
			projectID = record.ProjectID
		} else {
			var args map[string]json.RawMessage
			if err := json.Unmarshal(raw, &args); err == nil {
				_ = json.Unmarshal(args["project_id"], &projectID)
			}
		}
		enabled, err := s.Service.TrainV2Enabled(ctx, projectID)
		if err != nil {
			return genericActionError(action, err.Error()), nil
		}
		if !enabled {
			if err := requireToolAuthority(ctx, entry.LegacyTool); err != nil {
				return genericActionError(action, err.Error()), nil
			}
			if err := validateGenericActionInput(entry.LegacyInputSchema, raw); err != nil {
				return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
			}
			value, err := entry.LegacyExecute(ctx, raw)
			if err != nil {
				return genericActionError(action, err.Error()), nil
			}
			result := normalizeObject(value)
			if err := validateOutputValue(entry.LegacyOutputSchema, result); err != nil {
				return genericActionError(action, "action output contract violation: "+err.Error()), nil
			}
			return genericActionSuccess(result), nil
		}
	}
	if entry.Authority != nil {
		if err := entry.Authority(ctx); err != nil {
			return genericActionError(action, err.Error()), nil
		}
	}
	started = true
	s.recordRuntimeAction(ctx, record, "action_start", "info", action, nil, nil)
	executionSchema := entry.InputSchema
	if entry.ExecutionInputSchema != nil {
		executionSchema = entry.ExecutionInputSchema
	}
	if err := validateGenericActionInput(executionSchema, raw); err != nil {
		return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
	}
	value, err := entry.Execute(ctx, raw)
	if err != nil {
		return genericActionError(action, err), nil
	}
	result = normalizeObject(value)
	if err := validateOutputValue(entry.OutputSchema, result); err != nil {
		return genericActionError(action, "action output contract violation: "+err.Error()), nil
	}
	return genericActionSuccess(result), nil
}

func (s *Server) recordRuntimeAction(ctx context.Context, sessionRecord durableSession.Record, event, level, action string, cause error, result map[string]any) {
	if s.Service == nil {
		return
	}
	eventRecord := runtime_log.Event{
		Timestamp:   time.Now().UTC(),
		Level:       level,
		Component:   "mcp",
		Event:       event,
		Action:      action,
		RequestID:   runtime_log.RequestID(ctx),
		OperationID: runtime_log.OperationID(ctx),
		SessionID:   sessionRecord.ID,
		ProjectID:   sessionRecord.ProjectID,
	}
	if cause != nil {
		eventRecord.Error = fmt.Sprintf("%T", cause)
	}
	if structured := structuredActionErrorFromResult(result); structured != nil {
		eventRecord.ErrorCode = structured["code"]
		eventRecord.Phase = structured["phase"]
	}
	_ = runtime_log.New(s.Service.Config.StateDir).Append(eventRecord)
}

func structuredActionErrorFromResult(result map[string]any) map[string]string {
	if result == nil || result["is_error"] != true {
		return nil
	}
	container, _ := result["result"].(map[string]any)
	errorObject, _ := container["error"].(map[string]any)
	code, _ := errorObject["code"].(string)
	phase, _ := errorObject["phase"].(string)
	if code == "" {
		return nil
	}
	return map[string]string{"code": code, "phase": phase}
}

func operationIDFromRaw(raw json.RawMessage) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	var operationID string
	_ = json.Unmarshal(fields["operation_id"], &operationID)
	return operationID
}

func genericActionError(_ string, message any) map[string]any {
	if err, ok := message.(error); ok {
		var structured interface {
			StructuredActionError() map[string]any
		}
		if errors.As(err, &structured) {
			return map[string]any{"result": map[string]any{"error": structured.StructuredActionError()}, "is_error": true}
		}
	}
	return map[string]any{"result": map[string]any{"error": fmt.Sprint(message)}, "is_error": true}
}

func genericActionSuccess(result map[string]any) map[string]any {
	return map[string]any{"result": result, "is_error": false}
}

func genericBatchResult(action string, result map[string]any) map[string]any {
	item := map[string]any{"action": action}
	for key, value := range result {
		item[key] = value
	}
	return item
}

func validateGenericActionInput(schema map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("input must be an object")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("input must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("input has trailing JSON content")
	}
	// Preserve the established generic-action diagnostics for ordinary
	// contracts. Mode-dispatched contracts such as task/create use oneOf and
	// must go through the recursive schema validator so each branch is checked
	// truthfully.
	if _, hasOneOf := schema["oneOf"]; !hasOneOf && schema["additionalProperties"] == false {
		if err := validateToolArguments(schema, raw); err != nil {
			return err
		}
	}
	if err := validateSchemaValue(schema, value, "input"); err != nil {
		return err
	}
	return nil
}

func (s *Server) genericBatch(ctx context.Context, legacy map[string]Tool, raw json.RawMessage) (any, error) {
	var input genericBatchInput
	if err := decode(raw, &input); err != nil {
		return nil, err
	}
	if input.SessionID == "" {
		return nil, fmt.Errorf("session is required")
	}
	if len(input.Calls) > genericBatchMaxItems {
		return nil, fmt.Errorf("calls exceeds maximum of %d", genericBatchMaxItems)
	}
	record := durableSession.Record{}
	if input.SessionID != "" {
		var err error
		record, err = s.activeSession(input.SessionID)
		if err != nil {
			return nil, err
		}
		ctx = withSession(ctx, record)
	}
	entries := s.genericActionRegistry(legacy)
	results := make([]map[string]any, 0, len(input.Calls))
	for _, call := range input.Calls {
		var item genericCallInput
		action := ""
		result, err := decodeBatchCall(call, &item)
		if err == nil {
			action = item.Action
		}
		if err == nil && item.SessionID != "" {
			result = nil
			err = fmt.Errorf("batch item session is not accepted; use the batch root session")
		}
		if err == nil {
			result, err = s.genericDispatch(ctx, entries, record, item.Action, item.Input)
		}
		if err != nil {
			var probe struct {
				Action string `json:"action"`
			}
			_ = json.Unmarshal(call, &probe)
			action = probe.Action
			result = genericActionError(probe.Action, err.Error())
		}
		results = append(results, genericBatchResult(action, result))
	}
	return map[string]any{"results": results}, nil
}

func decodeBatchCall(raw json.RawMessage, input *genericCallInput) (map[string]any, error) {
	if err := decode(raw, input); err != nil {
		return nil, err
	}
	return nil, nil
}

func inheritSessionProject(schema map[string]any, projectID string, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("input must be an object")
	}
	bound, err := bindProjectValue(schema, value, projectID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(bound)
}

func bindProjectValue(schema map[string]any, value any, projectID string) (any, error) {
	properties, _ := schema["properties"].(map[string]any)
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current)+1)
		for key, child := range current {
			childSchema, _ := properties[key].(map[string]any)
			bound, err := bindProjectValue(childSchema, child, projectID)
			if err != nil {
				return nil, err
			}
			result[key] = bound
		}
		if _, ok := properties["project_id"]; ok {
			if supplied, exists := current["project_id"]; exists {
				if suppliedString, ok := supplied.(string); !ok || suppliedString != projectID {
					return nil, fmt.Errorf("project_id does not match session project")
				}
			} else if containsRequired(stringList(schema["required"]), "project_id") {
				result["project_id"] = projectID
			}
		}
		return result, nil
	case []any:
		items, _ := schema["items"].(map[string]any)
		result := make([]any, len(current))
		for i, child := range current {
			bound, err := bindProjectValue(items, child, projectID)
			if err != nil {
				return nil, err
			}
			result[i] = bound
		}
		return result, nil
	default:
		return value, nil
	}
}

func containsRequired(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
