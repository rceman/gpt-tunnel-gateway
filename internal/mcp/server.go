package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type Server struct{ Service *service.Service }
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
	return json.Marshal(publicTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, Annotations: t.Annotations})
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("/mcp", s.handle)
	return s.security(mux)
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "non-loopback Host rejected", http.StatusForbidden)
			return
		}
		if r.RemoteAddr != "" {
			remote, _, e := net.SplitHostPort(r.RemoteAddr)
			if e == nil {
				rip := net.ParseIP(remote)
				if rip == nil || !rip.IsLoopback() {
					http.Error(w, "remote caller rejected", http.StatusForbidden)
					return
				}
			}
		}
		if origin := r.Header.Get("Origin"); origin != "" && !isLoopbackOrigin(origin) {
			http.Error(w, "Origin rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func isLoopbackOrigin(v string) bool {
	return strings.HasPrefix(v, "http://127.0.0.1:") || strings.HasPrefix(v, "http://localhost:") || strings.HasPrefix(v, "https://127.0.0.1:") || strings.HasPrefix(v, "https://localhost:")
}
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	var req request
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.write(w, response{JSONRPC: "2.0", ID: nil, Error: &rpcError{Code: -32700, Message: "parse error", Data: err.Error()}})
		return
	}
	switch req.Method {
	case "initialize":
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "gpt-tunnel-gatewayd", "version": "0.6.2"}}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		tools := s.tools()
		names := make([]string, 0, len(tools))
		for name := range tools {
			names = append(names, name)
		}
		sort.Strings(names)
		list := make([]Tool, 0, len(names))
		for _, name := range names {
			tool := tools[name]
			tool.Execute = nil
			list = append(list, tool)
		}
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": list}})
	case "tools/call":
		var call toolCall
		if err := decode(req.Params, &call); err != nil {
			s.write(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}})
			return
		}
		if err := validateToolCallMeta(call.Meta); err != nil {
			s.write(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}})
			return
		}
		tool, ok := s.tools()[call.Name]
		if !ok {
			s.write(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "unknown tool"}})
			return
		}
		if err := validateToolArguments(tool.InputSchema, call.Arguments); err != nil {
			s.write(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}})
			return
		}
		value, err := tool.Execute(r.Context(), call.Arguments)
		if err != nil {
			s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(tool, map[string]any{"error": err.Error()}, true)})
			return
		}
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(tool, value, false)})
	default:
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found"}})
	}
}
func validateToolArguments(schema map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var args map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return fmt.Errorf("arguments must be an object: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	properties, _ := schema["properties"].(map[string]any)
	for key := range args {
		if _, ok := properties[key]; !ok {
			return fmt.Errorf("unknown argument %q", key)
		}
	}
	if required, ok := schema["required"].([]string); ok {
		for _, key := range required {
			if _, exists := args[key]; !exists {
				return fmt.Errorf("missing required argument %q", key)
			}
		}
	} else if requiredAny, ok := schema["required"].([]any); ok {
		for _, value := range requiredAny {
			key, _ := value.(string)
			if key != "" {
				if _, exists := args[key]; !exists {
					return fmt.Errorf("missing required argument %q", key)
				}
			}
		}
	}
	return nil
}

const maxToolCallMetaBytes = 64 << 10

func validateToolCallMeta(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxToolCallMetaBytes {
		return fmt.Errorf("_meta exceeds %d bytes", maxToolCallMetaBytes)
	}
	var value map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&value); err != nil || value == nil {
		return fmt.Errorf("_meta must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("_meta has trailing JSON content")
	}
	return nil
}

func toolResult(tool Tool, value any, isError bool) map[string]any {
	if !isError && tool.OutputSchema == nil {
		text, ok := value.(string)
		if !ok {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": "tool output contract violation: expected plain text"}}, "isError": true}
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": false}
	}
	obj := normalizeObject(value)
	text, _ := json.MarshalIndent(obj, "", "  ")
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": isError}
	if isError {
		return result
	}
	if err := validateOutputValue(tool.OutputSchema, obj); err != nil {
		failure := map[string]any{"error": "tool output contract violation: " + err.Error()}
		failureText, _ := json.MarshalIndent(failure, "", "  ")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(failureText)}}, "isError": true}
	}
	result["structuredContent"] = obj
	return result
}
func normalizeObject(v any) map[string]any {
	if v == nil {
		return map[string]any{"ok": true}
	}
	data, _ := json.Marshal(v)
	var obj map[string]any
	if json.Unmarshal(data, &obj) == nil && obj != nil {
		return obj
	}
	var arr []any
	if json.Unmarshal(data, &arr) == nil {
		return map[string]any{"items": arr}
	}
	return map[string]any{"value": v}
}
func (s *Server) write(w http.ResponseWriter, v response) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func obj(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func integer(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}
func array(items any) map[string]any { return map[string]any{"type": "array", "items": items} }
func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
func getString(raw json.RawMessage, key string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	v, ok := m[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}
func optionalString(raw json.RawMessage, key string) string {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	v, _ := m[key].(string)
	return v
}
func intArg(raw json.RawMessage, key string, def int) int {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return def
}

func optionalInteger(raw json.RawMessage, key string) (int, bool, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return 0, false, err
	}
	value, ok := values[key]
	if !ok {
		return 0, false, nil
	}
	if string(value) == "null" {
		return 0, true, fmt.Errorf("%s must be an integer", key)
	}
	var integerValue int
	if err := json.Unmarshal(value, &integerValue); err != nil {
		return 0, true, fmt.Errorf("%s must be an integer", key)
	}
	return integerValue, true, nil
}

func (s *Server) tools() map[string]Tool {
	t := map[string]Tool{}
	add := func(name, description string, schema map[string]any, fn func(context.Context, json.RawMessage) (any, error)) {
		output, outputOK := toolOutputSchemas[name]
		annotations, annotationsOK := toolAnnotations[name]
		if !outputOK || !annotationsOK {
			panic("missing MCP contract for tool " + name)
		}
		t[name] = Tool{Name: name, Description: description, InputSchema: schema, OutputSchema: output, Annotations: annotations, Execute: fn}
	}
	add("system_ping", "Return gateway identity and time.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.6.2", "gateway_id": s.Service.Config.GatewayID, "time": time.Now().UTC()}, nil
	})
	add("gateway_capabilities", "Describe configured limits, projects, and transport.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		ids, err := s.Service.EffectiveProjectIDs()
		if err != nil {
			return nil, err
		}
		return map[string]any{"gateway_id": s.Service.Config.GatewayID, "listen_addr": s.Service.Config.ListenAddr, "projects": ids, "hub_protocol_root": hub.ProtocolRoot, "hub_repository_url": s.Service.Config.Hub.RepositoryURL, "hub_branch": s.Service.Config.Hub.Branch, "hub_managed_root": hub.ManagedRoot(s.Service.Config), "airelay_control_only": true, "generic_shell_available": false}, nil
	})
	add("project_list", "List durable hub projects.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		v, e := s.Service.ProjectList(ctx)
		return map[string]any{"projects": v}, e
	})
	add("project_read", "Read one durable project.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ProjectRead(ctx, id)
	})
	add("project_identifiers_read", "Read the immutable compact-ID allocation record for a durable project.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ProjectIdentifiersRead(ctx, id)
	})
	projectCode := str("Three-letter uppercase project code")
	projectCode["pattern"] = "^[A-Z]{3}$"
	add("project_identifiers_adopt", "Atomically adopt a unique immutable compact-ID project code and initialize its counters; this does not switch task, ADR, or run creation to compact IDs.", obj(map[string]any{"project_id": str("Project identifier"), "project_code": projectCode, "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "project_code"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.ProjectIdentifiersAdoptInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		identifiers, operation, e := s.Service.ProjectIdentifiersAdopt(ctx, in)
		if e != nil {
			return nil, e
		}
		return map[string]any{"identifiers": identifiers, "operation": operation}, nil
	})
	add("project_status", "Read durable project, local mapping, worktree, and hub revision.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ProjectStatus(ctx, id)
	})
	add("project_register", "Register a durable project from a JSON object.", obj(map[string]any{"project": map[string]any{"type": "object"}, "expected_hub_revision": str("Optimistic hub revision")}, "project"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.ProjectRegisterInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.ProjectRegister(ctx, in)
	})
	add("plan_read", "Read current global plan.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.PlanRead(ctx, id)
	})
	add("plan_cutover", "Owner-invoked one-time conversion of the known schema-v1 plan to schema-v2.", obj(map[string]any{"project_id": str("Project identifier"), "updated_by": str("Owner identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanCutoverInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanCutover(ctx, in)
	})
	add("plan_update", "Partially update the compact plan manifest.", obj(map[string]any{"project_id": str("Project identifier"), "title": str("Plan title"), "summary": str("Plan summary"), "current_objective": str("Current objective"), "queue": map[string]any{"type": "array", "items": str("Ordered section identifiers")}, "active_task_id": str("Active task"), "active_run_id": str("Active run"), "updated_by": str("Author identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanUpdateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanUpdate(ctx, in)
	})
	add("plan_section_read", "Read one complete plan section by exact identifier.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier")}, "project_id", "section_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		project, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		section, e := getString(raw, "section_id")
		if e != nil {
			return nil, e
		}
		return s.Service.PlanSectionRead(ctx, project, section)
	})
	add("plan_section_create", "Create one independently versioned plan section.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier"), "title": str("Section title"), "short_description": str("One-line section description"), "description": str("Full section description"), "updated_by": str("Author identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "section_id", "title", "short_description", "description", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanSectionCreateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanSectionCreate(ctx, in)
	})
	add("plan_section_update", "Partially update one plan section with its independent revision.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier"), "title": str("Section title"), "short_description": str("One-line section description"), "description": str("Full section description"), "updated_by": str("Author identity"), "expected_section_revision": integer("Expected section revision", 1, 1000000), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "section_id", "updated_by", "expected_section_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanSectionUpdateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanSectionUpdate(ctx, in)
	})
	add("plan_section_delete", "Delete one plan section from current state while retaining Git history.", obj(map[string]any{"project_id": str("Project identifier"), "section_id": str("Plan section identifier"), "updated_by": str("Author identity"), "expected_section_revision": integer("Expected section revision", 1, 1000000), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "section_id", "updated_by", "expected_section_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanSectionDeleteInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanSectionDelete(ctx, in)
	})
	add("plan_render", "Render the complete plan and all sections explicitly.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		return s.Service.PlanRender(ctx, id)
	})
	add("plan_history", "List plan Git history.", obj(map[string]any{"project_id": str("Project identifier"), "limit": integer("Maximum commits", 1, 1000)}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.PlanHistory(ctx, id, intArg(raw, "limit", 50))
		return map[string]any{"history": v}, e
	})
	add("adr_list", "List accepted ADRs.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.ADRList(ctx, id)
		return map[string]any{"adrs": v}, e
	})
	add("adr_read", "Read an ADR.", obj(map[string]any{"project_id": str("Project identifier"), "adr_id": str("ADR identifier")}, "project_id", "adr_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		p, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		id, e := getString(raw, "adr_id")
		if e != nil {
			return nil, e
		}
		return s.Service.ADRRead(ctx, p, id)
	})
	add("adr_create", "Create immutable ADR.", obj(map[string]any{"adr": map[string]any{"type": "object"}, "expected_hub_revision": str("Optimistic hub revision")}, "adr"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.ADRCreateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.ADRCreate(ctx, in)
	})
	add("task_create", "Create immutable hashed task from a normalized slug and the refreshed project default branch.", obj(map[string]any{"project_id": str("Project identifier"), "slug": str("Lowercase task slug"), "title": str("Task title"), "objective": str("Full objective"), "acceptance_criteria": array(str("Criterion")), "constraints": array(str("Constraint")), "required_gates": array(str("Gate")), "created_by": str("Creator identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "slug", "title", "objective", "created_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskCreateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		task, res, e := s.Service.TaskCreate(ctx, in)
		return map[string]any{"task": task, "operation": res}, e
	})
	add("task_list", "List project tasks.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.TaskList(ctx, id)
		return map[string]any{"tasks": v}, e
	})
	add("task_read", "Read task record and active execution packet when a run exists.", obj(map[string]any{"task_id": str("Task identifier")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "task_id")
		if e != nil {
			return nil, e
		}
		packet, e := s.Service.TaskRead(ctx, id)
		if e == nil {
			return service.PublicTaskPacketView(packet), nil
		}
		task, e2 := s.Service.TaskReadRecord(ctx, id)
		if e2 != nil {
			return nil, e
		}
		return map[string]any{"task": task.Task, "state": task.State, "run_summaries": task.RunSummaries, "active_run": false}, nil
	})
	add("task_review_report_start", "Start the Gateway-local Delivery review draft for the completed Agent Run.", obj(map[string]any{"task_id": str("Task identifier"), "run_id": str("Same implementation Run identifier")}, "task_id", "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			TaskID string `json:"task_id"`
			RunID  string `json:"run_id"`
		}
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskReviewReportStart(ctx, in.TaskID, in.RunID)
	})
	sectionPayload := map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array"}, map[string]any{"type": "object"}}}
	add("task_review_report_section_update", "Update one bounded Gateway-local Delivery review section with an optimistic draft revision.", obj(map[string]any{
		"task_id": str("Task identifier"), "run_id": str("Same implementation Run identifier"), "section_id": str("Typed review section"),
		"expected_draft_revision": integer("Expected draft revision", 1, 1000000), "payload": sectionPayload,
	}, "task_id", "run_id", "section_id", "expected_draft_revision", "payload"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskReviewReportSectionUpdateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskReviewReportSectionUpdate(ctx, in)
	})
	add("task_review_report_validate", "Validate the complete Gateway-local Delivery review draft without publishing it.", obj(map[string]any{"task_id": str("Task identifier"), "run_id": str("Same implementation Run identifier")}, "task_id", "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			TaskID string `json:"task_id"`
			RunID  string `json:"run_id"`
		}
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskReviewReportValidate(ctx, in.TaskID, in.RunID)
	})
	add("task_review_report_finalize", "Publish the one immutable Run-bound Delivery review report in one Hub transaction.", obj(map[string]any{
		"task_id": str("Task identifier"), "run_id": str("Same implementation Run identifier"),
		"expected_draft_revision": integer("Expected draft revision", 1, 1000000), "expected_hub_revision": str("Optimistic Hub revision"),
	}, "task_id", "run_id", "expected_draft_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskReviewReportFinalizeInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		report, operation, e := s.Service.TaskReviewReportFinalize(ctx, in)
		return map[string]any{"report": report, "operation": operation}, e
	})
	add("task_report_read", "Read the latest applicable immutable Delivery review report for a Task.", obj(map[string]any{"task_id": str("Task identifier"), "run_id": str("Optional exact Run identifier")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "task_id")
		if e != nil {
			return nil, e
		}
		return s.Service.TaskReportRead(ctx, id, optionalString(raw, "run_id"))
	})
	add("task_dispatch", "Create and publish a run, prepare branch, and send short Airelay control message.", obj(map[string]any{"task_id": str("Task identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.DispatchInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		run, res, e := s.Service.TaskDispatch(ctx, in)
		return map[string]any{"run": service.PublicRunView(run), "operation": res}, e
	})
	add("task_supersede", "Create a replacement immutable task.", obj(map[string]any{"old_task_id": str("Superseded task"), "task": map[string]any{"type": "object"}}, "old_task_id", "task"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var envelope struct {
			OldTaskID string                  `json:"old_task_id"`
			Task      service.TaskCreateInput `json:"task"`
		}
		if e := decode(raw, &envelope); e != nil {
			return nil, e
		}
		task, res, e := s.Service.TaskSupersede(ctx, envelope.OldTaskID, envelope.Task)
		return map[string]any{"task": task, "operation": res}, e
	})
	add("task_cancel", "Cancel an undispatched task record.", obj(map[string]any{"task_id": str("Task identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "task_id")
		if e != nil {
			return nil, e
		}
		return s.Service.TaskCancel(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
	add("task_mark_merge_ready", "Record that a completed task's latest successful report is ready for GPT merge review; this mutates durable lifecycle state only.", obj(map[string]any{"task_id": str("Task identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskMarkMergeReadyInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskMarkMergeReady(ctx, in)
	})
	reason := str("Bounded reason for deferral")
	reason["minLength"] = 1
	reason["maxLength"] = 1024
	add("task_defer", "Defer a completed or merge-ready task with a bounded durable reason; this does not mutate a repository.", obj(map[string]any{"task_id": str("Task identifier"), "reason": reason, "expected_hub_revision": str("Optimistic hub revision")}, "task_id", "reason"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskDeferInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskDefer(ctx, in)
	})
	integrationHead := str("Exact remote develop commit SHA")
	integrationHead["pattern"] = "^[0-9a-f]{40}$"
	add("task_mark_merged", "Record a verified existing remote develop receipt for a merge-ready task; it performs no merge, push, checkout or branch deletion.", obj(map[string]any{"task_id": str("Task identifier"), "integration_head": integrationHead, "expected_hub_revision": str("Optimistic hub revision")}, "task_id", "integration_head"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.TaskMarkMergedInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.TaskMarkMerged(ctx, in)
	})
	add("run_list", "List project runs.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.RunList(ctx, id)
		public := make([]service.PublicRun, 0, len(v))
		for _, run := range v {
			public = append(public, service.PublicRunView(run))
		}
		return map[string]any{"runs": public}, e
	})
	add("run_read", "Read one run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		run, err := s.Service.RunRead(ctx, id)
		if err != nil {
			return nil, err
		}
		return service.PublicRunView(run), nil
	})
	add("run_status", "Alias for run_read.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		run, err := s.Service.RunRead(ctx, id)
		if err != nil {
			return nil, err
		}
		return service.PublicRunView(run), nil
	})
	add("run_report", "Read finalized report.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunReport(ctx, id)
	})
	add("run_review_snapshot", "Prepare one bounded structural review snapshot for a run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunReviewSnapshot(ctx, id)
	})
	add("run_agent_tail", "Read the bounded tail of the current run's Airelay session.", obj(map[string]any{"run_id": str("Run identifier"), "lines": integer("Number of lines", 1, 200)}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		lines, _, e := optionalInteger(raw, "lines")
		if e != nil {
			return nil, e
		}
		text, err := s.Service.RunAgentTail(ctx, id, lines)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	})
	add("run_resume", "Perform one canonical context-compaction recovery for an owned active run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunResume(ctx, id)
	})
	message := str("Bounded message to the registered project session")
	message["minLength"] = 1
	message["maxLength"] = 256
	add("agent_send", "Send one bounded message to the configured project Airelay session.", obj(map[string]any{"project_id": str("Registered project identifier"), "message": message}, "project_id", "message"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		projectID, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		text, err := getString(raw, "message")
		if err != nil {
			return nil, err
		}
		return s.Service.AgentSend(ctx, projectID, text)
	})
	add("agent_tail", "Read a bounded window from the configured project Airelay session.", obj(map[string]any{"project_id": str("Registered project identifier"), "lines": integer("Number of lines", 1, 200), "skip": integer("Newest lines to skip", 0, 196)}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		projectID, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		lines, _, err := optionalInteger(raw, "lines")
		if err != nil {
			return nil, err
		}
		skip, _, err := optionalInteger(raw, "skip")
		if err != nil {
			return nil, err
		}
		return s.Service.AgentTail(ctx, projectID, lines, skip)
	})
	add("agent_status", "Read bounded status and capacity warnings from the configured project Airelay session.", obj(map[string]any{"project_id": str("Registered project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		projectID, err := getString(raw, "project_id")
		if err != nil {
			return nil, err
		}
		return s.Service.AgentStatus(ctx, projectID)
	})
	add("run_sweep", "Reprompt or terminalize overdue active runs.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) { return s.Service.RunSweep(ctx) })
	add("run_cancel", "Request cooperative cancellation through Airelay.", obj(map[string]any{"run_id": str("Run identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunCancel(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
	add("run_cancel_acknowledge_no_mutation", "Acknowledge delivered cancellation and terminalize only when the configured task worktree is clean at its immutable base; this does not send a cancellation or hard-interrupt Airelay.", obj(map[string]any{"run_id": str("Run identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunCancelAcknowledgeNoMutation(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
	addOperatorJournalTools(add, s)
	addGitTools(add, s)
	if err := validateCanonicalToolManifest(t); err != nil {
		panic(err)
	}
	return t
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

func addGitTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	projectConfig := func(raw json.RawMessage) (string, config.ProjectConfig, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return "", config.ProjectConfig{}, e
		}
		p, err := s.Service.EffectiveProjectConfig(id)
		if err != nil {
			return "", config.ProjectConfig{}, err
		}
		return id, p, nil
	}
	add("git_refresh", "Refresh managed read-only mirror from project remote.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		e = s.Service.Git.Refresh(ctx, p)
		return map[string]any{"project_id": id, "refreshed": e == nil}, e
	})
	add("git_refs", "List local, remote, and tag refs from managed mirror.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Refs(ctx, p)
		return map[string]any{"refs": v}, e
	})
	add("git_log", "Read bounded commit history at a revision.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref"), "limit": integer("Maximum commits", 1, 1000)}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		rev, e := getString(raw, "revision")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Log(ctx, p, rev, intArg(raw, "limit", 50))
		return map[string]any{"commits": v}, e
	})
	add("git_show", "Show bounded commit metadata, summary, and stat.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref")}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		rev, e := getString(raw, "revision")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Show(ctx, p, rev)
		return map[string]any{"text": v}, e
	})
	add("git_tree", "List files at any revision.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref"), "path": str("Optional relative path")}, "project_id", "revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		rev, e := getString(raw, "revision")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Tree(ctx, p, rev, optionalString(raw, "path"))
		return map[string]any{"paths": v}, e
	})
	add("git_read_file", "Read a UTF-8 file at any revision.", obj(map[string]any{"project_id": str("Project identifier"), "revision": str("Revision or ref"), "path": str("Relative file path")}, "project_id", "revision", "path"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		rev, e := getString(raw, "revision")
		if e != nil {
			return nil, e
		}
		path, e := getString(raw, "path")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.ReadFile(ctx, p, rev, path)
		return map[string]any{"path": path, "revision": rev, "content": v}, e
	})
	add("git_diff", "Read bounded diff between two revisions.", obj(map[string]any{"project_id": str("Project identifier"), "from_revision": str("Base revision"), "to_revision": str("Target revision"), "paths": array(str("Optional relative path"))}, "project_id", "from_revision", "to_revision"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		var in struct {
			ProjectID string   `json:"project_id"`
			From      string   `json:"from_revision"`
			To        string   `json:"to_revision"`
			Paths     []string `json:"paths"`
		}
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		v, e := s.Service.Git.Diff(ctx, p, in.From, in.To, in.Paths)
		return map[string]any{"diff": v}, e
	})
	add("git_compare", "Compare divergence and merge base.", obj(map[string]any{"project_id": str("Project identifier"), "left": str("Left revision"), "right": str("Right revision")}, "project_id", "left", "right"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		left, e := getString(raw, "left")
		if e != nil {
			return nil, e
		}
		right, e := getString(raw, "right")
		if e != nil {
			return nil, e
		}
		return s.Service.Git.Compare(ctx, p, left, right)
	})
	add("git_merge_base", "Find merge base.", obj(map[string]any{"project_id": str("Project identifier"), "left": str("Left revision"), "right": str("Right revision")}, "project_id", "left", "right"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		left, e := getString(raw, "left")
		if e != nil {
			return nil, e
		}
		right, e := getString(raw, "right")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.Git.MergeBase(ctx, p, left, right)
		return map[string]any{"merge_base": v}, e
	})
	add("git_worktree_status", "Read current local worktree state.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		return s.Service.Git.WorktreeStatus(ctx, p)
	})
	add("git_worktree_diff", "Read unstaged or staged local worktree diff.", obj(map[string]any{"project_id": str("Project identifier"), "staged": map[string]any{"type": "boolean"}}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		_, p, e := projectConfig(raw)
		if e != nil {
			return nil, e
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		staged, _ := m["staged"].(bool)
		v, e := s.Service.Git.WorktreeDiff(ctx, p, staged)
		return map[string]any{"diff": v, "staged": staged}, e
	})
}
