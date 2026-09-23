package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rceman/gpt-tunnel-gateway/internal/actioncontract"
	"github.com/rceman/gpt-tunnel-gateway/internal/publicprojection"
)

func (s *Server) actionContractSet() *actioncontract.CompiledSet {
	s.actionContractOnce.Do(func() {
		s.actionContracts, s.actionContractErr = actioncontract.LoadCanonical()
	})
	if s.actionContractErr != nil {
		panic(fmt.Errorf("compile canonical action contracts: %w", s.actionContractErr))
	}
	if s.actionContracts == nil {
		panic("compile canonical action contracts: no compiled set")
	}
	return s.actionContracts
}

func (s *Server) applyActionContracts(entries map[string]genericActionEntry) {
	contracts := s.actionContractSet()
	debugEnabled := s.Service != nil && s.Service.Config.Debug.Enabled
	if !debugEnabled {
		for path, entry := range entries {
			if entry.Contract.Metadata.Surface == "debug" || strings.HasPrefix(path, "debug/") {
				delete(entries, path)
			}
		}
	}
	for _, path := range contracts.Paths() {
		contract, _ := contracts.Action(path)
		if contract.Metadata.Surface == "debug" && !debugEnabled {
			continue
		}
		entry, ok := entries[path]
		if !ok {
			panic(fmt.Sprintf("action contract %q has no registered handler", path))
		}
		entry.Contract = contract
		entry.Path = path
		entry.Description = contract.Description
		entry.InputSchema = contract.Input.JSONSchema()
		entry.OutputSchema = contract.Output.JSONSchema()
		entry.Annotations = ToolAnnotations{
			ReadOnlyHint:    contract.Metadata.Annotations.ReadOnly,
			DestructiveHint: contract.Metadata.Annotations.Destructive,
			IdempotentHint:  contract.Metadata.Annotations.Idempotent,
			OpenWorldHint:   contract.Metadata.Annotations.OpenWorld,
		}
		entries[path] = entry
	}
	for path := range entries {
		if _, ok := contracts.Action(path); !ok {
			panic(fmt.Sprintf("registered action %q has no compiled action contract", path))
		}
	}
}

func (s *Server) contractTransportHandler(action string, execute func(context.Context, json.RawMessage) (any, error)) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		contracts := s.actionContractSet()
		if err := contracts.ValidateInput(action, raw); err != nil {
			return nil, err
		}
		adapted, err := contracts.AdaptInput(action, raw)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(adapted)
		if err != nil {
			return nil, err
		}
		value, err := execute(ctx, encoded)
		if err != nil {
			return nil, err
		}
		result, continuation, err := genericActionValue(value)
		if err != nil {
			return nil, err
		}
		if continuation != nil {
			return nil, fmt.Errorf("non-collection transport action returned pagination")
		}
		result = compactActionResult(action, result)
		return s.projectContractOutput(action, result, "")
	}
}

func (s *Server) projectContractOutput(action string, result map[string]any, scope string) (map[string]any, error) {
	contracts := s.actionContractSet()
	adapted, err := contracts.AdaptOutput(action, result)
	if err != nil {
		return nil, err
	}
	object, ok := adapted.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("adapted action output is not an object")
	}
	projected, err := publicprojection.ProjectMapForAction(object, scope, action)
	if err != nil {
		return nil, err
	}
	contracted, err := contracts.ProjectOutput(action, projected)
	if err != nil {
		return nil, err
	}
	object, ok = contracted.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("contracted action output is not an object")
	}
	if err := contracts.ValidateOutput(action, object); err != nil {
		return nil, err
	}
	return object, nil
}
