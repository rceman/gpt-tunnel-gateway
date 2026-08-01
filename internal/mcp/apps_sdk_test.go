package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/gitx"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func callMCP(t *testing.T, srv *Server, body []byte) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:1/mcp", bytes.NewReader(body))
	req.Host = "127.0.0.1:1"
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	return response
}

func TestToolCallAcceptsBoundedProtocolMeta(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_ping","arguments":{},"_meta":{"openai/locale":"en","nested":{"request":"value"}}}}`))
	if response["error"] != nil {
		t.Fatalf("protocol _meta was rejected: %#v", response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("unexpected result: %#v", response)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || structured["version"] != "0.5.1" {
		t.Fatalf("unexpected structured result: %#v", result)
	}
}

func TestPlainTextToolResultRemainsUnstructured(t *testing.T) {
	tool := Tool{}
	result := toolResult(tool, "Warning: Controller\n⚠ Selected model is at capacity.\n", false)
	if result["isError"] != false {
		t.Fatalf("plain text result marked error: %#v", result)
	}
	content := result["content"].([]map[string]any)
	if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != "Warning: Controller\n⚠ Selected model is at capacity.\n" {
		t.Fatalf("unexpected plain text content: %#v", result)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("plain text result has structuredContent: %#v", result)
	}
}

func TestRunAgentTailToolCallUsesLiveServiceAndPlainTextTransport(t *testing.T) {
	hubBare, _, hubHead := testutil.RepoWithBareRemote(t)
	_, projectRoot, projectHead := testutil.RepoWithBareRemote(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := config.Config{SchemaVersion: 1, GatewayID: "test_gateway", ListenAddr: "127.0.0.1:8875", StateDir: filepath.Join(dir, "state"), MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000, DispatchTimeoutSeconds: 5, RunTimeoutSeconds: 60, AirelayCommand: script, Hub: config.HubConfig{RepositoryURL: hubBare, Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}, Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(dir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}}}
	s := service.New(c)
	project := model.Project{SchemaVersion: 1, ID: "example", RepositoryURL: "git@example.invalid:example.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active"}
	registered, err := s.ProjectRegister(context.Background(), service.ProjectRegisterInput{Project: project, WriteOptions: service.WriteOptions{ExpectedHubRevision: hubHead}})
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.TaskCreate(context.Background(), service.TaskCreateInput{ProjectID: "example", Title: "Tail", Objective: "Inspect tail", Branch: "feature/tail", BaseRevision: projectHead, AcceptanceCriteria: []string{"tail"}, CreatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: registered.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	title, summary, objective, activeTask := "Tail", "Tail", "Tail", task.ID
	plan, err := s.PlanUpdate(context.Background(), service.PlanUpdateInput{ProjectID: "example", Title: &title, Summary: &summary, CurrentObjective: &objective, ActiveTaskID: &activeTask, UpdatedBy: "test", WriteOptions: service.WriteOptions{ExpectedHubRevision: created.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.TaskDispatch(context.Background(), service.DispatchInput{TaskID: task.ID, WriteOptions: service.WriteOptions{ExpectedHubRevision: plan.Hub.After}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '⚠ Selected model is at capacity.\\nworkspace status\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	response := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`"}}}`))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != false {
		t.Fatalf("unexpected tail result: %#v", response)
	}
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content=%#v", content)
	}
	item := content[0].(map[string]any)
	if item["type"] != "text" || !strings.Contains(item["text"].(string), "Selected model is at capacity") {
		t.Fatalf("tail text=%#v", item)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok || !strings.Contains(structured["text"].(string), "Selected model is at capacity") {
		t.Fatalf("structured tail=%#v", result)
	}
	explicit := callMCP(t, &Server{Service: s}, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`","lines":9}}}`))
	explicitResult, ok := explicit["result"].(map[string]any)
	if !ok || explicitResult["isError"] != false || !strings.Contains(explicitResult["content"].([]any)[0].(map[string]any)["text"].(string), "workspace status") {
		t.Fatalf("unexpected explicit tail result: %#v", explicit)
	}
	if structured, ok := explicitResult["structuredContent"].(map[string]any); !ok || !strings.Contains(structured["text"].(string), "workspace status") {
		t.Fatalf("explicit structured tail=%#v", explicitResult)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'tail failure output\\n'\nprintf 'example_master CONTROL_PLANE_API_KEY=secret-marker\\n' >&2\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	assertToolError := func(body []byte, want string) {
		t.Helper()
		response := callMCP(t, &Server{Service: s}, body)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("unexpected error result: %#v", response)
		}
		content := result["content"].([]any)
		text := content[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, want) {
			t.Fatalf("error text=%q want=%q", text, want)
		}
		if _, ok := result["structuredContent"]; ok {
			t.Fatalf("tool error exposed structured content: %#v", result)
		}
		if len(text) > 512 {
			t.Fatalf("error text is not bounded: %d", len(text))
		}
		for _, forbidden := range []string{"example_master", "CONTROL_PLANE_API_KEY", "secret-marker"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("MCP error leaked %q: %q", forbidden, text)
			}
		}
	}
	assertToolError([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`","lines":9}}}`), "Airelay tail failed")
	assertToolError([]byte(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"missing"}}}`), "run not found")
	assertToolError([]byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_agent_tail","arguments":{"run_id":"`+run.ID+`","lines":201}}}`), "invalid tail line count")
}

func TestRunReviewSnapshotToolCallUsesOnlyRunIDAndReturnsToolErrorForUnknownRun(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_review_snapshot","arguments":{"run_id":"missing"}}}`))
	result, ok := response["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("unexpected snapshot tool result: %#v", response)
	}
	if _, ok := result["structuredContent"]; ok {
		t.Fatalf("tool error exposed structuredContent: %#v", result)
	}
	tool := srv.tools()["run_review_snapshot"]
	if got := tool.InputSchema["required"].([]string); len(got) != 1 || got[0] != "run_id" {
		t.Fatalf("unexpected input contract: %#v", tool.InputSchema)
	}
}

func TestToolCallRejectsInvalidAndOversizedMeta(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{GatewayID: "home_pc"})}
	for _, meta := range []string{`null`, `[]`, `"value"`} {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_ping","arguments":{},"_meta":` + meta + `}}`)
		response := callMCP(t, srv, body)
		errorObject, ok := response["error"].(map[string]any)
		if !ok || errorObject["code"] != float64(-32602) {
			t.Fatalf("invalid _meta %s was accepted: %#v", meta, response)
		}
	}
	large := strings.Repeat("x", maxToolCallMetaBytes)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "system_ping", "arguments": map[string]any{}, "_meta": map[string]any{"value": large}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := callMCP(t, srv, body)
	errorObject, ok := response["error"].(map[string]any)
	if !ok || errorObject["code"] != float64(-32602) {
		t.Fatalf("oversized _meta was accepted: %#v", response)
	}
}

func TestEveryToolDeclaresOutputSchemaAndExplicitAnnotations(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	tools := srv.tools()
	if len(tools) != len(canonicalToolManifest) {
		t.Fatalf("tool count=%d want manifest count %d", len(tools), len(canonicalToolManifest))
	}
	if len(toolOutputSchemas) != len(tools) || len(toolAnnotations) != len(tools) {
		t.Fatalf("contract coverage mismatch: tools=%d outputs=%d annotations=%d", len(tools), len(toolOutputSchemas), len(toolAnnotations))
	}
	for name, tool := range tools {
		if tool.OutputSchema == nil || tool.OutputSchema["type"] != "object" {
			t.Errorf("%s has invalid output schema: %#v", name, tool.OutputSchema)
		}
		if _, ok := tool.InputSchema["additionalProperties"]; !ok {
			t.Errorf("%s input schema is not explicit", name)
		}
		if _, ok := toolOutputSchemas[name]; !ok {
			t.Errorf("%s missing output schema registry entry", name)
		}
		if _, ok := toolAnnotations[name]; !ok {
			t.Errorf("%s missing annotation registry entry", name)
		}
	}
	properties := tools["plan_update"].InputSchema["properties"].(map[string]any)
	if _, ok := properties["body"]; ok {
		t.Fatal("plan_update advertises obsolete body input")
	}
}

func TestToolAnnotationsMatchActualSideEffects(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	tools := srv.tools()
	assert := func(name string, want ToolAnnotations) {
		t.Helper()
		if got := tools[name].Annotations; got != want {
			t.Errorf("%s annotations=%+v want %+v", name, got, want)
		}
	}
	assert("system_ping", readOnlyAnnotations())
	assert("git_read_file", readOnlyAnnotations())
	assert("run_review_snapshot", ToolAnnotations{ReadOnlyHint: true, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true})
	assert("adr_create", additiveExternalAnnotations())
	assert("task_create", additiveExternalAnnotations())
	assert("plan_cutover", destructiveExternalAnnotations())
	assert("plan_update", destructiveExternalAnnotations())
	assert("plan_section_create", additiveExternalAnnotations())
	assert("plan_section_update", destructiveExternalAnnotations())
	assert("plan_section_delete", destructiveExternalAnnotations())
	assert("plan_render", readOnlyAnnotations())
	assert("task_dispatch", destructiveExternalAnnotations())
	assert("run_cancel", destructiveExternalAnnotations())
	assert("git_refresh", ToolAnnotations{ReadOnlyHint: false, DestructiveHint: false, IdempotentHint: true, OpenWorldHint: true})
}

func TestToolsListSerializesOutputSchemasAndAllHints(t *testing.T) {
	srv := &Server{Service: service.New(config.Config{})}
	response := callMCP(t, srv, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	result := response["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != len(canonicalToolManifest) {
		t.Fatalf("tool count=%d want manifest count %d", len(tools), len(canonicalToolManifest))
	}
	previous := ""
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if previous != "" && name < previous {
			t.Fatalf("tools/list is not stable: %s before %s", previous, name)
		}
		previous = name
		if _, ok := tool["outputSchema"].(map[string]any); !ok {
			t.Errorf("%s outputSchema missing", name)
		}
		annotations, ok := tool["annotations"].(map[string]any)
		if !ok {
			t.Errorf("%s annotations missing", name)
			continue
		}
		for _, key := range []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"} {
			if _, ok := annotations[key].(bool); !ok {
				t.Errorf("%s annotation %s missing", name, key)
			}
		}
	}
}

func TestOutputContractViolationAndToolErrorsOmitStructuredContent(t *testing.T) {
	tool := Tool{OutputSchema: closedOutput(map[string]any{"value": outputString()}, "value")}
	result := toolResult(tool, map[string]any{"value": 42}, false)
	if result["isError"] != true {
		t.Fatalf("schema mismatch did not fail: %#v", result)
	}
	if _, exists := result["structuredContent"]; exists {
		t.Fatalf("schema mismatch exposed structuredContent: %#v", result)
	}
	result = toolResult(tool, map[string]any{"error": "failed"}, true)
	if result["isError"] != true {
		t.Fatalf("tool failure was not marked: %#v", result)
	}
	if _, exists := result["structuredContent"]; exists {
		t.Fatalf("tool failure exposed structuredContent: %#v", result)
	}
}

func TestTaskReadOutputSchemaAcceptsBothDeclaredShapes(t *testing.T) {
	inactive := map[string]any{
		"task": map[string]any{
			"schema_version": float64(1), "id": "task", "sha256": strings.Repeat("a", 64), "project_id": "project",
			"title": "title", "objective": "objective", "branch": "feature/x", "base_revision": strings.Repeat("b", 40),
			"acceptance_criteria": []any{}, "constraints": []any{}, "status": "created", "created_by": "gpt", "created_at": "2026-07-30T10:00:00Z",
		},
		"state": map[string]any{
			"schema_version": float64(1), "task_id": "task", "task_sha256": strings.Repeat("a", 64), "status": "created", "updated_at": "2026-07-30T10:00:00Z",
		},
		"active_run": false,
	}
	if err := validateOutputValue(toolOutputSchemas["task_read"], inactive); err != nil {
		t.Fatalf("inactive task shape rejected: %v", err)
	}
	active := map[string]any{
		"task": inactive["task"],
		"run": map[string]any{
			"schema_version": float64(1), "id": "run", "task_id": "task", "task_sha256": strings.Repeat("a", 64),
			"project_id": "project", "gateway_id": "home_pc", "session_key": "project_master", "branch": "feature/x",
			"base_revision": strings.Repeat("b", 40), "hub_revision": strings.Repeat("c", 40), "status": "awaiting_result",
			"completion_path": "/tmp/completion", "created_at": "2026-07-30T10:00:00Z",
		},
		"project": map[string]any{
			"schema_version": float64(1), "id": "project", "repository_url": "git@example.invalid:project.git", "default_branch": "main",
			"workflow_repository": "rceman/gpt-review-planner", "workflow_commit": strings.Repeat("d", 40), "status": "active",
			"created_at": "2026-07-30T10:00:00Z", "updated_at": "2026-07-30T10:00:00Z",
		},
		"plan": map[string]any{
			"schema_version": float64(model.PlanSchemaVersion), "project_id": "project", "revision": float64(1), "title": "title", "summary": "summary", "current_objective": "objective", "queue": []any{}, "sections": []any{},
			"updated_by": "gpt", "updated_at": "2026-07-30T10:00:00Z",
		},
		"repository_root": "/tmp/project", "completion_path": "/tmp/completion",
		"finalize_command": "gpt-tunnel run finalize run", "text": "packet",
	}
	if err := validateOutputValue(toolOutputSchemas["task_read"], active); err != nil {
		t.Fatalf("active task shape rejected: %v", err)
	}
}

func TestCanonicalSuccessfulOutputsMatchEveryDeclaredSchema(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	project := model.Project{SchemaVersion: 1, ID: "project", RepositoryURL: "git@example.invalid:project.git", DefaultBranch: "main", WorkflowRepository: "rceman/gpt-review-planner", WorkflowCommit: strings.Repeat("a", 40), Status: "active", CreatedAt: now, UpdatedAt: now}
	plan := model.Plan{SchemaVersion: model.PlanSchemaVersion, ProjectID: "project", Revision: 1, Title: "title", Summary: "summary", CurrentObjective: "objective", Queue: []string{}, Sections: []model.PlanSectionIndex{}, UpdatedBy: "gpt", UpdatedAt: now}
	section := model.PlanSection{SchemaVersion: model.PlanSchemaVersion, ProjectID: "project", ID: "section", Revision: 1, Title: "section", ShortDescription: "short", Description: "description", UpdatedBy: "gpt", UpdatedAt: now}
	render := model.PlanRender{SchemaVersion: model.PlanSchemaVersion, ProjectID: "project", Revision: 1, Title: "title", Summary: "summary", CurrentObjective: "objective", Text: "rendered"}
	adr := model.ADR{SchemaVersion: 1, ID: "ADR-TEST", ProjectID: "project", Title: "title", Status: "accepted", Context: "context", Decision: "decision", Consequences: "consequences", CreatedAt: now}
	task := model.Task{SchemaVersion: 1, ID: "task", SHA256: strings.Repeat("b", 64), ProjectID: "project", Title: "title", Objective: "objective", Branch: "feature/x", BaseRevision: strings.Repeat("c", 40), AcceptanceCriteria: []string{}, Constraints: []string{}, Status: "created", CreatedBy: "gpt", CreatedAt: now}
	state := model.TaskState{SchemaVersion: 1, TaskID: "task", TaskSHA256: task.SHA256, Status: "created", UpdatedAt: now}
	run := model.Run{SchemaVersion: 1, ID: "run", TaskID: "task", TaskSHA256: task.SHA256, ProjectID: "project", GatewayID: "home_pc", SessionKey: "project_master", Branch: "feature/x", BaseRevision: task.BaseRevision, HubRevision: strings.Repeat("d", 40), Status: "awaiting_result", CompletionPath: "/tmp/completion", CreatedAt: now}
	transaction := hub.TransactionResult{Before: strings.Repeat("d", 40), After: strings.Repeat("e", 40), Remote: "origin", Branch: "gpt-tunnel/home_pc", Paths: []string{"gpt-tunnel/v1/test.json"}}
	operation := service.OperationResult{Hub: transaction, ProjectID: "project", TaskID: "task", RunID: "run", Status: "updated"}
	local := config.ProjectConfig{Root: "/tmp/project", Mirror: "/tmp/mirror.git", Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "project_master"}
	worktree := gitx.WorktreeStatus{Branch: "main", Head: strings.Repeat("f", 40), Ahead: 0, Behind: 0, Porcelain: "", Clean: true}
	report := model.Report{SchemaVersion: 1, TaskID: "task", RunID: "run", ProjectID: "project", Status: "succeeded", Summary: "done", GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}, Repository: model.RepositoryProof{Branch: "feature/x", Head: worktree.Head, WorktreeClean: true, BaseAncestor: true, Commits: []string{}, ChangedFiles: []string{}, DiffScope: "base..head"}, FinishedAt: now}
	packet := service.TaskPacket{Task: task, Run: run, Project: project, Plan: plan, RepositoryRoot: "/tmp/project", CompletionPath: run.CompletionPath, FinalizeCommand: "gpt-tunnel run finalize run", Text: "packet"}
	ref := gitx.Ref{Name: "refs/heads/main", ObjectType: "commit", ObjectName: worktree.Head}
	commit := gitx.Commit{SHA: worktree.Head, Parents: []string{}, AuthorName: "GPT", AuthorEmail: "gpt@example.invalid", AuthorDate: now.Format(time.RFC3339), Subject: "subject"}
	compare := gitx.Compare{MergeBase: worktree.Head, LeftOnly: 0, RightOnly: 0}
	clean := true
	snapshot := model.ReviewSnapshot{SchemaVersion: 1, Run: model.ReviewSnapshotRun{ID: "run", TaskID: "task", ProjectID: "project", Status: "succeeded", Branch: "feature/x", BaseRevision: task.BaseRevision, CreatedAt: now}, Task: model.ReviewSnapshotTask{ID: "task", SHA256: task.SHA256, Title: "title", Objective: "objective", Branch: "feature/x", BaseRevision: task.BaseRevision, AcceptanceCriteria: []string{}, Constraints: []string{}, RequiredGates: []string{}, CreatedBy: "gpt", CreatedAt: now, TaskStateStatus: "completed"}, Report: model.ReviewSnapshotReport{Available: true, Status: "succeeded", Summary: "done", Commits: []string{}, ChangedFiles: []string{}, GateResults: []model.CompletionGateResult{}, AcceptanceCoverage: []string{}, Deviations: []string{}, RemainingRisks: []string{}, FinishedAt: &now}, Evidence: model.ReviewSnapshotEvidence{Available: true, Head: worktree.Head, Branch: "feature/x", WorktreeClean: &clean, Notes: []string{}, RecordedAt: &now}, Repository: model.ReviewSnapshotRepo{RefreshAttempted: true, RefreshSucceeded: true, DefaultBranch: "main", TaskBranch: "feature/x", TaskBranchPublished: true, TaskBranchHead: worktree.Head, Worktree: model.ReviewSnapshotWorktree{Branch: "feature/x", Head: worktree.Head, Clean: true}, EvidenceHeadReachable: true, BaseToEvidence: model.ReviewSnapshotCompare{MergeBase: task.BaseRevision}, DefaultToEvidence: model.ReviewSnapshotCompare{}, ChangedFiles: []string{}}, Checks: []model.ReviewSnapshotCheck{}, ReviewState: "reviewable", NextAction: "perform_static_review"}

	samples := map[string]any{
		"system_ping":          map[string]any{"service": "gpt-tunnel-gatewayd", "version": "0.5.1", "gateway_id": "home_pc", "time": now},
		"gateway_capabilities": map[string]any{"gateway_id": "home_pc", "listen_addr": "127.0.0.1:8765", "projects": []string{"project"}, "hub_protocol_root": "gpt-tunnel/v1", "hub_repository_url": "git@github.com:rceman/typer.git", "hub_branch": "gpt-tunnel/home_pc", "hub_managed_root": "/tmp/state/hub/repository", "airelay_control_only": true, "generic_shell_available": false},
		"project_list":         map[string]any{"projects": []model.Project{project}}, "project_read": project,
		"project_status": service.ProjectStatus{Project: project, Local: local, Worktree: worktree, Plan: plan.StatusView(), HubRevision: transaction.After}, "project_register": operation,
		"plan_read": plan, "plan_cutover": operation, "plan_update": operation, "plan_section_read": section, "plan_section_create": operation, "plan_section_update": operation, "plan_section_delete": operation, "plan_render": render, "plan_history": map[string]any{"history": []map[string]string{{"sha": transaction.After, "date": now.Format(time.RFC3339), "author": "GPT", "subject": "subject"}}},
		"adr_list": map[string]any{"adrs": []model.ADR{adr}}, "adr_read": adr, "adr_create": operation,
		"task_create": map[string]any{"task": task, "operation": operation}, "task_list": map[string]any{"tasks": []service.TaskRecord{{Task: task, State: state}}}, "task_read": packet,
		"task_dispatch": map[string]any{"run": run, "operation": operation}, "task_supersede": map[string]any{"task": task, "operation": operation}, "task_cancel": operation,
		"run_list": map[string]any{"runs": []model.Run{run}}, "run_read": run, "run_status": run, "run_report": report,
		"run_review_snapshot": snapshot,
		"run_agent_tail":      map[string]any{"text": "tail text"},
		"run_sweep":           service.SweepResult{Checked: 1, Items: []service.SweepItem{{RunID: "run", Action: "reprompt", Status: "awaiting_result"}}}, "run_cancel": operation,
		"git_refresh": map[string]any{"project_id": "project", "refreshed": true}, "git_refs": map[string]any{"refs": []gitx.Ref{ref}},
		"git_log": map[string]any{"commits": []gitx.Commit{commit}}, "git_show": map[string]any{"text": "show"}, "git_tree": map[string]any{"paths": []string{"README.md"}},
		"git_read_file": map[string]any{"path": "README.md", "revision": "main", "content": "content"}, "git_diff": map[string]any{"diff": "diff"},
		"git_compare": compare, "git_merge_base": map[string]any{"merge_base": worktree.Head}, "git_worktree_status": worktree,
		"git_worktree_diff": map[string]any{"diff": "diff", "staged": false},
	}

	server := &Server{Service: service.New(config.Config{})}
	for name, tool := range server.tools() {
		sample, ok := samples[name]
		if !ok {
			t.Errorf("missing canonical sample for %s", name)
			continue
		}
		if err := validateOutputValue(tool.OutputSchema, normalizeObject(sample)); err != nil {
			t.Errorf("%s canonical output rejected: %v", name, err)
		}
	}
	if len(samples) != len(server.tools()) {
		t.Fatalf("sample coverage mismatch: samples=%d tools=%d", len(samples), len(server.tools()))
	}
}
