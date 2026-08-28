package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/runtime_log"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func addGenericTransportTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server, legacy map[string]Tool) {
	add("session_start", "Create a fresh unbound durable session for an authorized role.", sessionStartPublicInputSchema(), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
	if input.SessionID == "" {
		return nil, fmt.Errorf("session is required")
	}
	record, err := s.activeSession(input.SessionID)
	if err != nil {
		return nil, err
	}
	ctx = withSession(ctx, record)
	return s.genericDispatch(ctx, entries, record, input.Action, input.Input)
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
	detail := false
	if projectionDetailAction(action) {
		var err error
		raw, detail, err = stripProjectionDetail(raw)
		if err != nil {
			return genericActionError(action, err.Error()), nil
		}
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
			LocalReceiptOnly:       entry.LocalReceiptOnly,
		})
		if err != nil {
			return genericActionError(action, err.Error()), nil
		}
		ctx = resolved
		ctx = service.WithAgentSessionID(ctx, record.ID)
		if entry.SessionBound && action != "session/bind" && action != "session/update" {
			if err := validateGenericActionInput(entry.InputSchema, raw); err != nil {
				return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
			}
			raw, err = inheritSessionProject(entry.ExecutionInputSchema, record.ProjectID, raw)
			if err != nil {
				return genericActionError(action, err.Error()), nil
			}
		}
		if !entry.LocalReceiptOnly {
			if err := s.validateSessionRules(ctx, record, action); err != nil {
				return genericActionError(action, err.Error()), nil
			}
		}
	}
	if entry.SessionBound {
		validationSchema := entry.InputSchema
		if action != "session/bind" && action != "session/update" && entry.ExecutionInputSchema != nil {
			validationSchema = entry.ExecutionInputSchema
		}
		if err := validateGenericActionInput(validationSchema, raw); err != nil {
			return genericActionError(action, err.Error()+"; inspect schema with path=\""+action+"\""), nil
		}
	}
	if entry.RouteLegacyByProjectModel && entry.LegacyExecute != nil && !entry.LocalReceiptOnly {
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
			result := compactActionResult(action, normalizeObject(value), detail)
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
	executeCtx := ctx
	var readSnapshot *hub.ReadSnapshot
	if entry.Annotations.ReadOnlyHint && !entry.LocalReceiptOnly && !entry.LocalReadOnly && !strings.HasPrefix(action, "runtime/") {
		var snapshotErr error
		readSnapshot, snapshotErr = s.Service.Hub.ReadSnapshot(ctx)
		if snapshotErr != nil {
			return genericActionError(action, snapshotErr), nil
		}
		executeCtx = hub.WithReadSnapshot(ctx, readSnapshot)
		defer readSnapshot.Close()
	}
	value, err := entry.Execute(executeCtx, raw)
	if err != nil {
		return genericActionError(action, err), nil
	}
	result = compactActionResult(action, normalizeObject(value), detail)
	if err := enforceCodeOutputTokenBudget(action, result); err != nil {
		return genericActionError(action, err), nil
	}
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
