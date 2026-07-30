package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
}
type Tool struct {
	Name        string                                              `json:"name"`
	Description string                                              `json:"description"`
	InputSchema map[string]any                                      `json:"inputSchema"`
	Execute     func(context.Context, json.RawMessage) (any, error) `json:"-"`
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
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "gpt-tunnel-gatewayd", "version": "0.2.1"}}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		list := []Tool{}
		for _, t := range s.tools() {
			t.Execute = nil
			list = append(list, t)
		}
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": list}})
	case "tools/call":
		var call toolCall
		if err := decode(req.Params, &call); err != nil {
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
			s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(map[string]any{"error": err.Error()}, true)})
			return
		}
		s.write(w, response{JSONRPC: "2.0", ID: req.ID, Result: toolResult(value, false)})
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

func toolResult(value any, isError bool) map[string]any {
	obj := normalizeObject(value)
	text, _ := json.MarshalIndent(obj, "", "  ")
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "structuredContent": obj, "isError": isError}
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

func (s *Server) tools() map[string]Tool {
	t := map[string]Tool{}
	add := func(name, description string, schema map[string]any, fn func(context.Context, json.RawMessage) (any, error)) {
		t[name] = Tool{Name: name, Description: description, InputSchema: schema, Execute: fn}
	}
	add("system_ping", "Return gateway identity and time.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		return map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.2.1", "gateway_id": s.Service.Config.GatewayID, "time": time.Now().UTC()}, nil
	})
	add("gateway_capabilities", "Describe configured limits, projects, and transport.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) {
		ids := []string{}
		for id := range s.Service.Config.Projects {
			ids = append(ids, id)
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
	add("plan_update", "Update global plan in a verified hub transaction.", obj(map[string]any{"project_id": str("Project identifier"), "summary": str("Plan summary"), "body": str("Plan body"), "active_task_id": str("Active task"), "active_run_id": str("Active run"), "updated_by": str("Author identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "summary", "body", "updated_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.PlanUpdateInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		return s.Service.PlanUpdate(ctx, in)
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
	add("task_create", "Create immutable hashed task.", obj(map[string]any{"project_id": str("Project identifier"), "title": str("Task title"), "objective": str("Full objective"), "branch": str("Required branch"), "base_revision": str("Exact base SHA"), "acceptance_criteria": array(str("Criterion")), "constraints": array(str("Constraint")), "required_gates": array(str("Gate")), "created_by": str("Creator identity"), "expected_hub_revision": str("Optimistic hub revision")}, "project_id", "title", "objective", "branch", "base_revision", "created_by"), func(ctx context.Context, raw json.RawMessage) (any, error) {
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
			return packet, nil
		}
		task, e2 := s.Service.TaskReadRecord(ctx, id)
		if e2 != nil {
			return nil, e
		}
		return map[string]any{"task": task.Task, "state": task.State, "active_run": false}, nil
	})
	add("task_dispatch", "Create and publish a run, prepare branch, and send short Airelay control message.", obj(map[string]any{"task_id": str("Task identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "task_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in service.DispatchInput
		if e := decode(raw, &in); e != nil {
			return nil, e
		}
		run, res, e := s.Service.TaskDispatch(ctx, in)
		return map[string]any{"run": run, "operation": res}, e
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
	add("run_list", "List project runs.", obj(map[string]any{"project_id": str("Project identifier")}, "project_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return nil, e
		}
		v, e := s.Service.RunList(ctx, id)
		return map[string]any{"runs": v}, e
	})
	add("run_read", "Read one run.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunRead(ctx, id)
	})
	add("run_status", "Alias for run_read.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunRead(ctx, id)
	})
	add("run_report", "Read finalized report.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunReport(ctx, id)
	})
	add("run_evidence", "Read finalized evidence.", obj(map[string]any{"run_id": str("Run identifier")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunEvidence(ctx, id)
	})
	add("run_sweep", "Reprompt or terminalize overdue active runs.", obj(map[string]any{}), func(ctx context.Context, raw json.RawMessage) (any, error) { return s.Service.RunSweep(ctx) })
	add("run_cancel", "Request cooperative cancellation through Airelay.", obj(map[string]any{"run_id": str("Run identifier"), "expected_hub_revision": str("Optimistic hub revision")}, "run_id"), func(ctx context.Context, raw json.RawMessage) (any, error) {
		id, e := getString(raw, "run_id")
		if e != nil {
			return nil, e
		}
		return s.Service.RunCancel(ctx, id, optionalString(raw, "expected_hub_revision"))
	})
	addGitTools(add, s)
	return t
}

func addGitTools(add func(string, string, map[string]any, func(context.Context, json.RawMessage) (any, error)), s *Server) {
	projectConfig := func(raw json.RawMessage) (string, config.ProjectConfig, error) {
		id, e := getString(raw, "project_id")
		if e != nil {
			return "", config.ProjectConfig{}, e
		}
		p, ok := s.Service.Config.Projects[id]
		if !ok {
			return "", config.ProjectConfig{}, fmt.Errorf("unknown project %q", id)
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
