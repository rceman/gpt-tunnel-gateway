package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type Server struct {
	Service                *service.Service
	AuthorityContext       context.Context
	genericActionMu        sync.RWMutex
	genericActions         map[string]GenericAction
	watcherActions         sync.Once
	watcherActionErr       error
	agentActions           sync.Once
	agentActionErr         error
	projectActions         sync.Once
	projectActionErr       error
	taskAuthoringActions   sync.Once
	taskAuthoringActionErr error
	trainV2Actions         sync.Once
	trainV2ActionErr       error
	runtimeLogActions      sync.Once
	runtimeLogActionErr    error
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}
type ToolAnnotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	IdempotentHint  bool `json:"idempotentHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}
type Tool struct {
	Name         string                                              `json:"name"`
	Description  string                                              `json:"description"`
	InputSchema  map[string]any                                      `json:"inputSchema"`
	OutputSchema map[string]any                                      `json:"outputSchema"`
	Annotations  ToolAnnotations                                     `json:"annotations"`
	Execute      func(context.Context, json.RawMessage) (any, error) `json:"-"`
}

func (t Tool) MarshalJSON() ([]byte, error) {
	type publicTool struct {
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		InputSchema  map[string]any  `json:"inputSchema"`
		OutputSchema map[string]any  `json:"outputSchema,omitempty"`
		Annotations  ToolAnnotations `json:"annotations"`
	}
	return json.Marshal(publicTool{
		Name:         t.Name,
		Description:  t.Description,
		InputSchema:  t.InputSchema,
		OutputSchema: t.OutputSchema,
		Annotations:  t.Annotations,
	})
}

func (s *Server) tools() map[string]Tool {
	s.ensureWatcherActions()
	s.ensureAgentActions()
	s.ensureProjectActions()
	s.ensureTaskAuthoringActions()
	s.ensureTrainV2Actions()
	s.ensureRuntimeLogActions()
	t := map[string]Tool{}
	add := toolAdder(func(name, description string, schema map[string]any, fn func(context.Context, json.RawMessage) (any, error)) {
		output, outputOK := toolOutputSchemas[name]
		annotations, annotationsOK := toolAnnotations[name]
		if !outputOK || !annotationsOK {
			panic("missing MCP contract for tool " + name)
		}
		t[name] = Tool{
			Name:         name,
			Description:  description,
			InputSchema:  schema,
			OutputSchema: output,
			Annotations:  annotations,
			Execute:      fn,
		}
	})
	s.addCoreTools(add)
	s.addPlanTools(add)
	s.addTaskTools(add)
	s.addTaskTrainTools(add)
	s.addRunTools(add)
	addOperatorJournalTools(add, s)
	addGitTools(add, s)
	legacyTools := make(map[string]Tool, len(t))
	for name, tool := range t {
		legacyTools[name] = tool
	}
	addGenericTransportTools(add, s, legacyTools)
	for name, tool := range t {
		if _, required := typedSessionAuthorityContract(name); required {
			tool.InputSchema = typedSessionInputSchema(tool.InputSchema)
			t[name] = tool
		}
	}
	return t
}

func (s *Server) publicTools() map[string]Tool {
	all := s.tools()
	public := make(map[string]Tool, len(canonicalToolManifest))
	for _, name := range canonicalToolManifest {
		if tool, ok := all[name]; ok {
			public[name] = tool
		}
	}
	if err := validateCanonicalToolManifest(public); err != nil {
		panic(err)
	}
	return public
}

func validateCanonicalToolManifest(tools map[string]Tool) error {
	want := map[string]bool{}
	for _, name := range canonicalToolManifest {
		if want[name] {
			return fmt.Errorf("duplicate canonical MCP tool %s", name)
		}
		want[name] = true
	}
	if len(tools) != len(want) {
		return fmt.Errorf("MCP manifest registration mismatch: registered=%d manifest=%d", len(tools), len(want))
	}
	for name := range want {
		if _, ok := tools[name]; !ok {
			return fmt.Errorf("MCP manifest tool is not registered: %s", name)
		}
		if _, ok := toolOutputSchemas[name]; !ok {
			return fmt.Errorf("MCP manifest output schema is missing: %s", name)
		}
		if _, ok := toolAnnotations[name]; !ok {
			return fmt.Errorf("MCP manifest annotations are missing: %s", name)
		}
	}
	return nil
}
