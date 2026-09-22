package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTSK585TaskCompleteMCPSchema(t *testing.T) {
	public := taskCompleteSchema()
	execution := adrExecutionSchema(taskCompleteSchema())
	out := taskCompleteOutputSchema()

	tsk585AssertClosedObject(t, public, "taskComplete")
	tsk585AssertClosedObject(t, execution, "taskCompleteExecution")
	tsk585AssertClosedObject(t, out, "taskCompleteOutput")

	required := map[string]bool{}
	for _, r := range public["required"].([]string) {
		required[r] = true
	}
	for _, want := range []string{"key", "mode", "reason", "review"} {
		if !required[want] {
			t.Fatalf("public schema missing required %q", want)
		}
	}
	if required["project_id"] {
		t.Fatal("public schema must not require project_id")
	}
	execRequired := map[string]bool{}
	for _, r := range execution["required"].([]string) {
		execRequired[r] = true
	}
	if !execRequired["project_id"] {
		t.Fatal("execution schema must require session-derived project_id")
	}

	props := public["properties"].(map[string]any)
	mode := props["mode"].(map[string]any)
	if _, has := mode["default"]; has {
		t.Fatal("mode must not carry a default")
	}
	enum := mode["enum"].([]any)
	if len(enum) != 3 || enum[0] != "integrated" || enum[1] != "non_code" || enum[2] != "historical" {
		t.Fatalf("mode enum=%v", enum)
	}
	reason := props["reason"].(map[string]any)
	if reason["minLength"] != 1 || reason["maxLength"] != 1024 {
		t.Fatalf("reason bounds=%v", reason)
	}
	review := props["review"].(map[string]any)
	if review["minLength"] != 8 || review["maxLength"] != 23 || review["pattern"] != model.JournalIDPattern {
		t.Fatalf("review JRN bounds=%v", review)
	}

	outProps := out["properties"].(map[string]any)
	status := outProps["status"].(map[string]any)
	if status["const"] != model.TaskAuthoringDone || status["enum"] != nil {
		t.Fatalf("output status schema=%v", status)
	}
	revision := outProps["revision"].(map[string]any)
	if revision["minimum"] != float64(1) {
		t.Fatalf("output revision schema=%v", revision)
	}
	outRequired := map[string]bool{}
	for _, r := range out["required"].([]string) {
		outRequired[r] = true
	}
	if len(outRequired) != 3 || !outRequired["key"] || !outRequired["status"] || !outRequired["revision"] {
		t.Fatalf("output required=%v", outRequired)
	}
	if _, has := outProps["operation_id"]; has {
		t.Fatal("output must not expose operation fields")
	}
}
func TestTSK585TaskCompleteMCPRegistration(t *testing.T) {
	server := &Server{}
	if err := server.registerTaskCompleteAction(); err != nil {
		t.Fatal(err)
	}
	action, ok := server.genericActions["task/complete"]
	if !ok {
		t.Fatal("task/complete not registered")
	}
	if action.AuthorityRole != "planner" || !action.SessionBound || !action.LocalReceiptOnly {
		t.Fatalf("registration=%#v", action)
	}
	if !action.Annotations.DestructiveHint || !action.Annotations.IdempotentHint {
		t.Fatalf("annotations=%#v", action.Annotations)
	}
}
func TestTSK585TaskCompleteMCPValidation(t *testing.T) {
	public := taskCompleteSchema()
	jrn := `"EXM-JRN1"`
	valid := `{"key":"EXM-TSK1","mode":"non_code","reason":"done","review":` + jrn + `}`
	if err := tsk585IntegrateSchemaCheck(t, public, valid); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	rejected := map[string]string{
		"missing mode":      `{"key":"EXM-TSK1","reason":"x","review":` + jrn + `}`,
		"missing reason":    `{"key":"EXM-TSK1","mode":"non_code","review":` + jrn + `}`,
		"missing review":    `{"key":"EXM-TSK1","mode":"non_code","reason":"x"}`,
		"bad mode":          `{"key":"EXM-TSK1","mode":"verified","reason":"x","review":` + jrn + `}`,
		"empty reason":      `{"key":"EXM-TSK1","mode":"non_code","reason":"","review":` + jrn + `}`,
		"bad jrn":           `{"key":"EXM-TSK1","mode":"non_code","reason":"x","review":"bogus"}`,
		"legacy acceptance": `{"key":"EXM-TSK1","mode":"non_code","reason":"x","acceptance":[{"criterion":1,"evidence":[` + jrn + `]}]}`,
		"unknown property":  `{"key":"EXM-TSK1","mode":"non_code","reason":"x","review":` + jrn + `,"extra":1}`,
	}
	for name, raw := range rejected {
		if err := tsk585IntegrateSchemaCheck(t, public, raw); err == nil {
			t.Fatalf("%s must reject", name)
		}
	}
}
func tsk585BrowseFixture(t *testing.T) *Server {
	t.Helper()
	_, projectRoot, _ := testutil.RepoWithBareRemote(t)
	stateDir := t.TempDir()
	c := config.Config{
		SchemaVersion: 1, GatewayID: "HOM", ListenAddr: "127.0.0.1:8875",
		StateDir: stateDir, MaxReadBytes: 1 << 20, MaxDiffBytes: 1 << 20, MaxListItems: 1000,
		Hub:      config.HubConfig{RepositoryURL: filepath.Join(stateDir, "hub.git"), Branch: "main", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"},
		Projects: map[string]config.ProjectConfig{"example": {Root: projectRoot, Mirror: filepath.Join(stateDir, "mirror.git"), Remote: "origin", DefaultBranch: "main", AirelaySessionKey: "example_master"}},
	}
	svc := service.New(c)
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc.Durability = db
	statuses := []string{model.TaskAuthoringPlanned, model.TaskAuthoringDone, model.TaskAuthoringReady, model.TaskAuthoringArchived}
	now := time.Now().UTC()
	for i := 0; i < 8; i++ {
		task, err := model.NewTask("example", fmt.Sprintf("EXM-TSK9%03d", i+1), model.AuthoringDraft{
			Title: fmt.Sprintf("Browse %02d", i), Summary: "Browse summary.", Objective: "Browse objective.", Priority: model.TaskPriorityP2, ADRRelation: model.TaskADRNoRequired,
		}, "planner", now)
		if err != nil {
			t.Fatal(err)
		}
		task.Status = statuses[i%len(statuses)]
		task.UpdatedAt = now
		if task.Status == model.TaskAuthoringReady {
			task.ReadySeal = &model.TaskReadySeal{Revision: task.Revision, RevisionSHA256: task.RevisionSHA256, ReadyBy: "planner", ReadyAt: now}
		}
		payload, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Shared.Exec(context.Background(), `INSERT INTO shared_tasks(id,revision,payload,updated_at) VALUES(?,?,?,?)`, task.ID, int64(task.Revision), payload, now.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{Service: svc}
}
func tsk585ExecuteGeneric(t *testing.T, server *Server, path string, arguments map[string]any) (any, error) {
	t.Helper()
	server.ensureTaskAuthoringActions()
	server.genericActionMu.RLock()
	action, ok := server.genericActions[path]
	server.genericActionMu.RUnlock()
	if !ok {
		t.Fatalf("generic action %q is not registered", path)
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	value, err := action.Execute(context.Background(), raw)
	if continuation, ok := value.(genericActionContinuation); ok {
		return continuation.Result, err
	}
	return value, err
}
func tsk585BrowseStatuses(t *testing.T, value any) []string {
	t.Helper()
	doc, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, item := range decoded.Items {
		out = append(out, item.Status)
	}
	return out
}
func TestTSK585TaskBrowseMCPOmitsDone(t *testing.T) {
	server := tsk585BrowseFixture(t)

	listed, err := tsk585ExecuteGeneric(t, server, "task/list", map[string]any{"project_id": "example"})
	if err != nil {
		t.Fatal(err)
	}
	statuses := tsk585BrowseStatuses(t, listed)
	if len(statuses) != 4 {
		t.Fatalf("task/list statuses=%v", statuses)
	}
	for _, status := range statuses {
		if status == "done" || status == "archived" {
			t.Fatalf("task/list returned hidden status %q", status)
		}
	}
	withArchived, err := tsk585ExecuteGeneric(t, server, "task/list", map[string]any{"project_id": "example", "include_archived": true})
	if err != nil {
		t.Fatal(err)
	}
	seenArchived := false
	for _, status := range tsk585BrowseStatuses(t, withArchived) {
		if status == "done" {
			t.Fatal("task/list include_archived returned done")
		}
		seenArchived = seenArchived || status == "archived"
	}
	if !seenArchived {
		t.Fatal("include_archived must include archived Tasks")
	}
	done, err := tsk585ExecuteGeneric(t, server, "task/query", map[string]any{"project_id": "example", "status": "done"})
	if err != nil {
		t.Fatal(err)
	}
	doneStatuses := tsk585BrowseStatuses(t, done)
	if len(doneStatuses) != 2 || doneStatuses[0] != "done" || doneStatuses[1] != "done" {
		t.Fatalf("task/query status=done=%v", doneStatuses)
	}
	archived, err := tsk585ExecuteGeneric(t, server, "task/query", map[string]any{"project_id": "example", "status": "archived"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range tsk585BrowseStatuses(t, archived) {
		if status != "archived" {
			t.Fatalf("task/query status=archived returned %q", status)
		}
	}
	defaultQuery, err := tsk585ExecuteGeneric(t, server, "task/query", map[string]any{"project_id": "example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range tsk585BrowseStatuses(t, defaultQuery) {
		if status == "done" || status == "archived" {
			t.Fatalf("task/query default returned hidden status %q", status)
		}
	}
}
func TestTSK585TaskGuideMCP(t *testing.T) {
	public := taskGuideSchema()
	out := taskGuideOutputSchema()
	tsk585AssertClosedObject(t, public, "taskGuide")
	tsk585AssertClosedObject(t, out, "taskGuideOutput")
	if required, has := public["required"].([]string); has && len(required) != 0 {
		t.Fatalf("task/guide public input required=%v", required)
	}
	if len(public["properties"].(map[string]any)) != 0 {
		t.Fatal("task/guide public input must have no properties")
	}
	props := out["properties"].(map[string]any)
	source := props["source"].(map[string]any)
	if source["const"] != "builtin" {
		t.Fatalf("source=%v", source)
	}
	for _, field := range []string{"workflow", "review", "verification", "completion", "boundaries"} {
		schema := props[field].(map[string]any)
		if schema["minLength"] != 1 || schema["maxLength"] != 768 {
			t.Fatalf("%s bounds=%v", field, schema)
		}
	}
	reviewOut := taskExecutionReviewOutputSchema()
	reviewProps := reviewOut["properties"].(map[string]any)
	if reviewProps["base"].(map[string]any)["pattern"] != "^[a-f0-9]{8}$" {
		t.Fatal("task/review output must require sha8 base")
	}
	diffIn := codeDiffInputSchema()
	if diffIn["properties"].(map[string]any)["base"].(map[string]any)["pattern"] != "^[a-f0-9]{8}$" {
		t.Fatal("code/diff input must carry optional sha8 base")
	}
	diffOut := codeDiffOutputSchema()
	if diffOut["properties"].(map[string]any)["base"].(map[string]any)["pattern"] != "^[a-f0-9]{8}$" {
		t.Fatal("code/diff output must require sha8 base")
	}
	if _, has := codeReadOutputSchema()["properties"].(map[string]any)["base"]; has {
		t.Fatal("code/read output must not gain base")
	}
	server := tsk585BrowseFixture(t)
	server.ensureTaskAuthoringActions()
	server.genericActionMu.RLock()
	action, ok := server.genericActions["task/guide"]
	server.genericActionMu.RUnlock()
	if !ok {
		t.Fatal("task/guide is not registered")
	}
	if action.AuthorityRole != actionRoleWorkflow || !action.SessionBound || !action.LocalReadOnly || action.LocalReceiptOnly ||
		!action.Annotations.ReadOnlyHint || !action.Annotations.IdempotentHint {
		t.Fatalf("task/guide flags=%#v", action)
	}
	value, err := tsk585ExecuteGeneric(t, server, "task/guide", map[string]any{"project_id": "example"})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(value)
	var decoded map[string]string
	if err := json.Unmarshal(doc, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["source"] != "builtin" {
		t.Fatalf("source=%q", decoded["source"])
	}
	for _, field := range []string{"workflow", "review", "verification", "completion", "boundaries"} {
		if n := utf8.RuneCountInString(decoded[field]); n < 1 || n > 768 {
			t.Fatalf("%s runes=%d", field, n)
		}
	}
	if decoded["workflow"] != taskGuideWorkflow || decoded["review"] != taskGuideReview ||
		decoded["verification"] != taskGuideVerification || decoded["completion"] != taskGuideCompletion ||
		decoded["boundaries"] != taskGuideBoundaries {
		t.Fatal("guide output must be the exact builtin strings")
	}
}
func tsk585TrustedTool(t *testing.T, server *Server, name string, arguments map[string]any) (any, error) {
	t.Helper()
	tools := server.tools()
	tool, ok := tools[name]
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(authority.Attach(context.Background(), server.AuthorityContext), raw)
}
func TestTSK585TaskGuideMCPTransport(t *testing.T) {
	svc, revision := newWorkflowPolicyStatusService(t)
	seedMCPTestCodingAgent(t, svc, revision)
	server := &Server{
		Service:          svc,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	grant, err := svc.EnsureProjectSessionBootstrapGrant(context.Background(), "example", "EXM")
	if err != nil {
		t.Fatal(err)
	}
	start := func(role string) string {
		t.Helper()
		if role != "planner" {
			t.Fatalf("token bootstrap only mints Planner sessions, got %q", role)
		}
		value, err := tsk585TrustedTool(t, server, "session_start", map[string]any{"token": grant.Token})
		if err != nil {
			t.Fatal(err)
		}
		doc, _ := json.Marshal(value)
		var decoded struct {
			Session string `json:"session"`
		}
		if err := json.Unmarshal(doc, &decoded); err != nil || decoded.Session == "" {
			t.Fatalf("session_start=%#v", value)
		}
		return decoded.Session
	}
	call := func(session string, input map[string]any) (map[string]any, bool) {
		t.Helper()
		value, err := tsk585TrustedTool(t, server, "call", map[string]any{"session": session, "action": "task/guide", "input": input})
		if err != nil {
			return nil, false
		}
		doc, _ := json.Marshal(value)
		var decoded map[string]any
		if err := json.Unmarshal(doc, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded, true
	}
	planner := start("planner")
	for name, session := range map[string]string{"planner": planner} {
		result, ok := call(session, map[string]any{})
		if !ok || result["is_error"] == true {
			t.Fatalf("%s task/guide failed: %#v", name, result)
		}
		guide, _ := result["result"].(map[string]any)
		if guide["source"] != "builtin" || guide["workflow"] != taskGuideWorkflow || guide["boundaries"] != taskGuideBoundaries {
			t.Fatalf("%s guide=%#v", name, guide)
		}
		if _, ok := result["result"].(map[string]any); !ok {
			t.Fatalf("%s guide result shape=%#v", name, result)
		}
	}
	result, _ := call(planner, map[string]any{"project_id": "example"})
	if result["is_error"] != true && result["ok"] != false {
		t.Fatalf("caller project_id must be rejected by the closed public schema: %#v", result)
	}
	result, _ = call(planner, map[string]any{"extra": "x"})
	if result["is_error"] != true && result["ok"] != false {
		t.Fatalf("extra public property must be rejected: %#v", result)
	}
	result, _ = func() (map[string]any, bool) {
		value, err := tsk585TrustedTool(t, server, "call", map[string]any{"session": planner, "action": "task/review", "input": map[string]any{"key": "EXM-TSK1", "stage": "code"}})
		if err != nil {
			return nil, false
		}
		doc, _ := json.Marshal(value)
		var decoded map[string]any
		_ = json.Unmarshal(doc, &decoded)
		return decoded, true
	}()
	if result == nil || (result["is_error"] != true && result["ok"] != false) {
		t.Fatalf("Agent must not reach task/review: %#v", result)
	}
}
func tsk585SchemaValue(t *testing.T, raw string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func tsk585IntegrateSchemaCheck(t *testing.T, schema map[string]any, raw string) error {
	t.Helper()
	return validateSchemaValue(schema, tsk585SchemaValue(t, raw), "input")
}
func tsk585AssertClosedObject(t *testing.T, schema map[string]any, path string) {
	t.Helper()
	if schema["type"] == "object" {
		if props, ok := schema["properties"].(map[string]any); ok {
			if schema["additionalProperties"] != false {
				t.Fatalf("%s: object schema is not closed", path)
			}
			for name, child := range props {
				if childSchema, ok := child.(map[string]any); ok {
					tsk585AssertClosedObject(t, childSchema, path+"."+name)
				}
			}
			return
		}
	}
	if options, ok := schema["oneOf"].([]any); ok {
		for _, option := range options {
			if branch, ok := option.(map[string]any); ok {
				tsk585AssertClosedObject(t, branch, path+".oneOf")
			}
		}
	}
}
func TestTSK585HistoricalIntegrateMCPSchema(t *testing.T) {
	public := taskExecutionIntegrateSchema()
	execution := taskExecutionIntegrateExecutionSchema(taskExecutionIntegrateSchema())
	sha := strings.Repeat("a", 40)
	jrn := `"EXM-JRN1"`

	acceptedPublic := map[string]string{
		"omitted/default verified": `{"key":"EXM-TSK1"}`,
		"explicit verified":        `{"key":"EXM-TSK1","mode":"verified","comment":"ok"}`,
		"legacy":                   `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":` + jrn + `}}`,
		"bootstrap_full":           `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"bootstrap_full","evidence":` + jrn + `,"candidate_head":"` + sha + `","main_base":"` + sha + `"}}`,
	}
	for name, raw := range acceptedPublic {
		if err := tsk585IntegrateSchemaCheck(t, public, raw); err != nil {
			t.Fatalf("public schema must accept %s: %v", name, err)
		}
		execRaw := strings.Replace(raw, `{"key"`, `{"project_id":"example","key"`, 1)
		if err := tsk585IntegrateSchemaCheck(t, execution, execRaw); err != nil {
			t.Fatalf("execution schema must accept %s: %v", name, err)
		}
	}

	rejectedPublic := map[string]string{
		"verified+historical":         `{"key":"EXM-TSK1","mode":"verified","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":` + jrn + `}}`,
		"missing historical":          `{"key":"EXM-TSK1","mode":"historical"}`,
		"historical without mode":     `{"key":"EXM-TSK1","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":` + jrn + `}}`,
		"legacy with candidate":       `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":` + jrn + `,"candidate_head":"` + sha + `"}}`,
		"legacy with base":            `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":` + jrn + `,"main_base":"` + sha + `"}}`,
		"bootstrap missing candidate": `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"bootstrap_full","evidence":` + jrn + `,"main_base":"` + sha + `"}}`,
		"bootstrap missing base":      `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"bootstrap_full","evidence":` + jrn + `,"candidate_head":"` + sha + `"}}`,
		"bad sha":                     `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"ABC","profile":"legacy","evidence":` + jrn + `}}`,
		"bad profile":                 `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"partial","evidence":` + jrn + `}}`,
		"oversized comment":           `{"key":"EXM-TSK1","comment":"` + strings.Repeat("x", 1025) + `"}`,
		"unknown top-level field":     `{"key":"EXM-TSK1","bogus":1}`,
		"unknown historical field":    `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":` + jrn + `,"bogus":1}}`,
		"project_id in public schema": `{"project_id":"example","key":"EXM-TSK1"}`,
		"evidence OPR key":            `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":"EXM-OPR1"}}`,
		"evidence historical O key":   `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":"EXM-O1"}}`,
		"evidence malformed":          `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":"bogus"}}`,
		"evidence lowercase jrn":      `{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":"exm-jrn1"}}`,
	}
	for name, raw := range rejectedPublic {
		if err := tsk585IntegrateSchemaCheck(t, public, raw); err == nil {
			t.Fatalf("public schema must reject %s", name)
		}
	}

	tsk585AssertClosedObject(t, public, "public")
	tsk585AssertClosedObject(t, execution, "execution")
	if err := tsk585IntegrateSchemaCheck(t, execution, `{"key":"EXM-TSK1"}`); err == nil {
		t.Fatal("execution schema must require project_id")
	}
	if err := tsk585IntegrateSchemaCheck(t, execution, `{"project_id":"example","key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"`+sha+`","profile":"legacy","evidence":`+jrn+`}}`); err != nil {
		t.Fatalf("execution schema must accept historical with project_id: %v", err)
	}
	branches, ok := execution["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("execution schema must keep two oneOf branches: %#v", execution)
	}

	publicBranches, ok := public["oneOf"].([]any)
	if !ok || len(publicBranches) != 2 {
		t.Fatalf("public schema must have two oneOf branches: %#v", public)
	}
	verifiedMode := publicBranches[0].(map[string]any)["properties"].(map[string]any)["mode"].(map[string]any)
	if verifiedMode["default"] != "verified" {
		t.Fatalf("verified mode must default to verified: %#v", verifiedMode)
	}
	historicalMode := publicBranches[1].(map[string]any)["properties"].(map[string]any)["mode"].(map[string]any)
	if _, hasDefault := historicalMode["default"]; hasDefault {
		t.Fatalf("historical mode must not carry a default: %#v", historicalMode)
	}
	if enum, ok := historicalMode["enum"].([]any); !ok || len(enum) != 1 || enum[0] != "historical" {
		t.Fatalf("historical mode enum: %#v", historicalMode)
	}
}
func TestTSK585TaskIntegrateMCPDispatch(t *testing.T) {
	svc, _ := newWorkflowPolicyStatusService(t)
	server := &Server{
		Service:          svc,
		AuthorityContext: authority.WithPlanner(context.Background()),
	}
	planner := genericSession(t, svc, "example")
	call := func(input map[string]any) map[string]any {
		t.Helper()
		value, err := tsk585TrustedTool(t, server, "call", map[string]any{"session": planner, "action": "task/integrate", "input": input})
		if err != nil {
			t.Fatalf("task/integrate transport failed: %v", err)
		}
		doc, _ := json.Marshal(value)
		var decoded map[string]any
		if err := json.Unmarshal(doc, &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	sha := strings.Repeat("a", 40)
	for name, input := range map[string]map[string]any{
		"verified default":  {"key": "EXM-TSK1"},
		"verified explicit": {"key": "EXM-TSK1", "mode": "verified", "comment": "bounded"},
		"historical legacy": {"key": "EXM-TSK1", "mode": "historical", "historical": map[string]any{
			"integration_head": sha, "profile": "legacy", "evidence": "EXM-JRN1"}},
		"historical bootstrap_full": {"key": "EXM-TSK1", "mode": "historical", "historical": map[string]any{
			"integration_head": sha, "profile": "bootstrap_full", "evidence": "EXM-JRN1", "candidate_head": sha, "main_base": sha}},
	} {
		result := call(input)
		if result["ok"] != true {
			t.Fatalf("%s task/integrate must pass schema and reach the service: %#v", name, result)
		}
		payload, ok := result["result"].(map[string]any)
		if !ok || payload["operation_id"] == "" {
			t.Fatalf("%s task/integrate did not enqueue a mutation receipt: %#v", name, result)
		}
	}
	if rejected := call(map[string]any{"key": "EXM-TSK1", "project_id": "other"}); rejected["ok"] != false {
		t.Fatalf("caller-supplied foreign project_id must be rejected: %#v", rejected)
	}
	if rejected := call(map[string]any{"key": "EXM-TSK1", "project_id": "example"}); rejected["ok"] != false {
		t.Fatalf("caller-supplied project_id stays closed out of the public schema: %#v", rejected)
	}
	if rejected := call(map[string]any{"key": "EXM-TSK1", "bogus": 1}); rejected["ok"] != false {
		t.Fatalf("unknown property must remain rejected: %#v", rejected)
	}

	execution := taskExecutionIntegrateExecutionSchema(taskExecutionIntegrateSchema())
	bind := func(raw string) (map[string]any, error) {
		t.Helper()
		bound, err := inheritSessionProject(execution, "example", json.RawMessage(raw))
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if err := json.Unmarshal(bound, &value); err != nil {
			t.Fatal(err)
		}
		return value, nil
	}
	bound, err := bind(`{"key":"EXM-TSK1"}`)
	if err != nil || bound["project_id"] != "example" {
		t.Fatalf("oneOf binder must inject session project for the verified branch: %v %#v", err, bound)
	}
	bound, err = bind(`{"key":"EXM-TSK1","mode":"historical","historical":{"integration_head":"` + sha + `","profile":"legacy","evidence":"EXM-JRN1"}}`)
	if err != nil || bound["project_id"] != "example" {
		t.Fatalf("oneOf binder must inject session project for the historical branch: %v %#v", err, bound)
	}
	if nested, ok := bound["historical"].(map[string]any); !ok || nested["project_id"] != nil {
		t.Fatalf("nested oneOf object must not receive project_id: %#v", bound["historical"])
	}
	if bound, err = bind(`{"key":"EXM-TSK1","project_id":"example"}`); err != nil || bound["project_id"] != "example" {
		t.Fatalf("matching caller project_id must survive binding: %v %#v", err, bound)
	}
	if _, err = bind(`{"key":"EXM-TSK1","project_id":"other"}`); err == nil || !strings.Contains(err.Error(), "project_id does not match session project") {
		t.Fatalf("foreign caller project_id must fail binding: %v", err)
	}
}
