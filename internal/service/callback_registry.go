package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

type CallbackRegisterInput struct {
	Callback model.ProjectCallback `json:"callback"`
}

type CallbackRemoveInput struct {
	Callback string `json:"callback"`
}

type CallbackRegistrationResult struct {
	Callback string `json:"callback"`
	Event    string `json:"event"`
	Status   string `json:"status"`
}

type CallbackURLSummary struct {
	Method string `json:"method"`
	URL    string `json:"url"`
}

type CallbackScriptSummary struct {
	Path string `json:"path"`
}

type CallbackSummary struct {
	Key    string                 `json:"key"`
	Event  string                 `json:"event"`
	URL    *CallbackURLSummary    `json:"url,omitempty"`
	Script *CallbackScriptSummary `json:"script,omitempty"`
}

type CallbackListResult struct {
	Callbacks []CallbackSummary `json:"callbacks"`
}

type CallbackEvent struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type CallbackEventsResult struct {
	Events []CallbackEvent `json:"events"`
}

type CallbackConflictError struct{ Callback string }

func (e *CallbackConflictError) Error() string {
	return fmt.Sprintf("callback %q is already registered with a different definition", e.Callback)
}

func (e *CallbackConflictError) StructuredActionError() map[string]any {
	return map[string]any{"code": "CALLBACK_CONFLICT", "phase": "validate", "details": map[string]any{"callback": e.Callback}}
}

type CallbackNotFoundError struct{ Callback string }

func (e *CallbackNotFoundError) Error() string {
	return fmt.Sprintf("callback %q was not found", e.Callback)
}

func (e *CallbackNotFoundError) StructuredActionError() map[string]any {
	return map[string]any{"code": "CALLBACK_NOT_FOUND", "phase": "lookup", "details": map[string]any{"callback": e.Callback}}
}

func (s *Service) CallbackRegister(ctx context.Context, projectID string, in CallbackRegisterInput) (CallbackRegistrationResult, error) {
	if err := model.ValidateProjectCallback(in.Callback); err != nil {
		return CallbackRegistrationResult{}, err
	}
	configuration, err := s.readCallbackConfiguration(ctx, projectID)
	if err != nil {
		return CallbackRegistrationResult{}, err
	}
	for _, existing := range configuration.Callbacks {
		if existing.Callback != in.Callback.Callback {
			continue
		}
		if !reflect.DeepEqual(existing, in.Callback) {
			return CallbackRegistrationResult{}, &CallbackConflictError{Callback: in.Callback.Callback}
		}
		return CallbackRegistrationResult{Callback: existing.Callback, Event: existing.Event, Status: "already_registered"}, nil
	}
	configuration.Callbacks = append(configuration.Callbacks, in.Callback)
	sortProjectCallbacks(configuration.Callbacks)
	if _, _, err := s.updateCallbackConfiguration(ctx, projectID, configuration); err != nil {
		return CallbackRegistrationResult{}, err
	}
	return CallbackRegistrationResult{Callback: in.Callback.Callback, Event: in.Callback.Event, Status: "registered"}, nil
}

func (s *Service) CallbackRemove(ctx context.Context, projectID string, in CallbackRemoveInput) (CallbackRegistrationResult, error) {
	if err := model.ValidateObjectIdentifier(in.Callback); err != nil {
		return CallbackRegistrationResult{}, &CallbackNotFoundError{Callback: in.Callback}
	}
	configuration, err := s.readCallbackConfiguration(ctx, projectID)
	if err != nil {
		return CallbackRegistrationResult{}, err
	}
	updated := make([]model.ProjectCallback, 0, len(configuration.Callbacks))
	found := false
	var event string
	for _, existing := range configuration.Callbacks {
		if existing.Callback == in.Callback {
			found = true
			event = existing.Event
			continue
		}
		updated = append(updated, existing)
	}
	if !found {
		return CallbackRegistrationResult{}, &CallbackNotFoundError{Callback: in.Callback}
	}
	configuration.Callbacks = updated
	if _, _, err := s.updateCallbackConfiguration(ctx, projectID, configuration); err != nil {
		return CallbackRegistrationResult{}, err
	}
	return CallbackRegistrationResult{Callback: in.Callback, Event: event, Status: "removed"}, nil
}

func (s *Service) CallbackList(ctx context.Context, projectID string) (CallbackListResult, error) {
	configuration, err := s.readCallbackConfiguration(ctx, projectID)
	if err != nil {
		return CallbackListResult{}, err
	}
	callbacks := append([]model.ProjectCallback(nil), configuration.Callbacks...)
	sortProjectCallbacks(callbacks)
	result := CallbackListResult{Callbacks: make([]CallbackSummary, 0, len(callbacks))}
	for _, callback := range callbacks {
		summary := CallbackSummary{Key: callback.Callback, Event: callback.Event}
		if callback.URL != nil {
			summary.URL = &CallbackURLSummary{Method: callback.URL.Method, URL: callback.URL.URL}
		}
		if callback.Script != nil {
			summary.Script = &CallbackScriptSummary{Path: callback.Script.Path}
		}
		result.Callbacks = append(result.Callbacks, summary)
	}
	return result, nil
}

func (s *Service) CallbackEvents(context.Context) (CallbackEventsResult, error) {
	return CallbackEventsResult{Events: []CallbackEvent{{Key: model.ProjectCallbackWorkFinishedEvent, Description: "Emitted once after real Agent work returns to stable idle."}}}, nil
}

func (s *Service) readCallbackConfiguration(ctx context.Context, projectID string) (model.ProjectConfiguration, error) {
	if s.Durability == nil {
		return model.ProjectConfiguration{}, fmt.Errorf("Shared callback registry is unavailable")
	}
	return s.projectConfigurationReadShared(ctx, projectID)
}

func (s *Service) updateCallbackConfiguration(ctx context.Context, projectID string, configuration model.ProjectConfiguration) (model.ProjectConfiguration, OperationResult, error) {
	callbacks := append([]model.ProjectCallback(nil), configuration.Callbacks...)
	sortProjectCallbacks(callbacks)
	return s.ProjectConfigurationUpdate(ctx, ProjectConfigurationUpdateInput{
		ProjectID:        projectID,
		ExpectedRevision: configuration.Revision,
		Patch:            ProjectConfigurationPatch{Callbacks: &callbacks},
		UpdatedBy:        "gateway-callback",
	})
}

func sortProjectCallbacks(callbacks []model.ProjectCallback) {
	sort.Slice(callbacks, func(i, j int) bool { return callbacks[i].Callback < callbacks[j].Callback })
}

func callbackEventPayload(epochID, projectID, agentID string) ([]byte, error) {
	value := map[string]any{"event": model.ProjectCallbackWorkFinishedEvent, "epoch": epochID, "project": projectID}
	if agentID != "" {
		value["agent"] = agentID
	}
	return json.Marshal(value)
}
