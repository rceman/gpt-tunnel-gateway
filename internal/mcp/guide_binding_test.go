package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/authority"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK532GuideActionsExposeApplicableSubjectsAndPlannerBinding(t *testing.T) {
	server := newSessionTestServer(t)
	registry := server.genericActionRegistry(server.tools())
	for _, subject := range model.GuideSubjects() {
		action, ok := registry[subject+"/guide"]
		wantRole := actionRoleWorkflow
		if subject == "agent" {
			wantRole = actionRolePlannerOrLead
		}
		if !ok || action.AuthorityRole != wantRole || !action.SessionBound || !action.SessionRequired || !action.LocalReadOnly {
			t.Fatalf("guide action %s/guide=%#v present=%t", subject, action, ok)
		}
	}
	for _, item := range model.GuideApplicabilityMatrix() {
		if item.Required {
			continue
		}
		if _, ok := registry[item.Subject+"/guide"]; ok {
			t.Fatalf("intentionally omitted guide action %s/guide was registered", item.Subject)
		}
	}
	bind, ok := registry["project/guide_bind"]
	if !ok || bind.AuthorityRole != durableSession.RolePlanner || !bind.SessionBound || !bind.SessionRequired || !bind.LocalReceiptOnly || !bind.Annotations.IdempotentHint {
		t.Fatalf("Planner binding action=%#v present=%t", bind, ok)
	}
	properties := schemaProperties(bind.InputSchema)
	if bind.InputSchema["additionalProperties"] != false || len(properties) != 3 ||
		!reflectStringList(stringList(bind.InputSchema["required"]), []string{"reason", "rule", "subject"}) {
		t.Fatalf("project/guide_bind input=%#v", bind.InputSchema)
	}
	if _, exposed := properties["project_id"]; exposed {
		t.Fatal("project/guide_bind must inherit project identity from the authenticated Session")
	}
	ruleIDSchema := properties["rule"].(map[string]any)
	reasonSchema := properties["reason"].(map[string]any)
	subjectSchema := properties["subject"].(map[string]any)
	if ruleIDSchema["maxLength"] != model.MaxRuleIDLength || ruleIDSchema["pattern"] != model.RuleIDPattern || reasonSchema["maxLength"] != model.MaxDeferredReasonBytes ||
		!reflectStringList(stringList(subjectSchema["enum"]), model.GuideSubjects()) {
		t.Fatalf("binding input bounds/enums: rule=%#v reason=%#v subject=%#v", ruleIDSchema, reasonSchema, subjectSchema)
	}
	if bind.OutputSchema["additionalProperties"] != false || bind.ExecutionInputSchema["additionalProperties"] != false || len(schemaProperties(bind.ExecutionInputSchema)) != 4 {
		t.Fatalf("binding schema projection: output=%#v execution=%#v", bind.OutputSchema, bind.ExecutionInputSchema)
	}
}

func TestTSK532GuideBindingProvenanceFailClosedAndRoleAuthority(t *testing.T) {
	server := newSessionTestServer(t)
	planner := genericSession(t, server.Service, "example")
	worker := genericSessionWithRole(t, server.Service, "example", durableSession.RoleWorker)
	value := taskGuideRuleValue("approved task guide")
	rule := tsk532CreateAcceptedGuideRule(t, server.Service, "guide.task", value)
	proposed := tsk532CreateProposedGuideRule(t, server.Service, "guide.task.proposed", value)
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "example", Subject: "task", RuleID: proposed.ID, Reason: "proposed Rules are not authorities", UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("proposed Rule was bound")
	}
	mismatched := tsk532CreateAcceptedGuideRule(t, server.Service, "guide.task.mismatch", map[string]string{"guidance": "wrong domain"})
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "example", Subject: "task", RuleID: mismatched.ID, Reason: "reject mismatched projection", UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("render-mismatched Rule was bound")
	}
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "example", Subject: "task", RuleID: rule.ID, UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("binding without a reason was accepted")
	}
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "example", Subject: "task", RuleID: "EXM-RUL999999", Reason: "wrong-project or missing Rule", UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("missing Rule was bound")
	}
	workerResponse := tsk532Call(t, server, worker, "project/guide_bind", map[string]any{
		"subject": "task", "rule": rule.ID, "reason": "Worker cannot bind policy",
	})
	workerStructured, ok := workerResponse["structuredContent"].(map[string]any)
	if !ok || workerStructured["ok"] != false {
		t.Fatalf("Worker project/guide_bind response=%#v", workerResponse)
	}
	before, err := server.Service.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := tsk532CallResult(t, server, planner, "project/guide_bind", map[string]any{
		"subject": "task", "rule": rule.ID, "reason": "Bind the accepted Task workflow guide",
	})
	if !ok || bound["subject"] != "task" || bound["rule"] != rule.ID {
		t.Fatalf("Planner guide binding result=%#v ok=%t", bound, ok)
	}
	after, err := server.Service.ProjectConfigurationRead(context.Background(), "example")
	if err != nil || after.Revision != before.Revision+1 || after.GuideBindings["task"] != rule.ID {
		t.Fatalf("binding configuration=%#v err=%v before=%d", after, err, before.Revision)
	}
	if _, ok := tsk532CallResult(t, server, planner, "project/guide_bind", map[string]any{
		"subject": "task", "rule": rule.ID, "reason": "Retry the identical binding",
	}); !ok {
		t.Fatal("identical binding retry was rejected")
	}
	retried, err := server.Service.ProjectConfigurationRead(context.Background(), "example")
	if err != nil || retried.Revision != after.Revision {
		t.Fatalf("identical binding retry changed revision: before=%d after=%#v err=%v", after.Revision, retried, err)
	}
	guide, ok := tsk532CallResult(t, server, planner, "task/guide", map[string]any{})
	if !ok || guide["source"] != "rule" || guide["rule"] != rule.ID || guide["rule_revision"] != float64(rule.Revision) || guide["workflow"] != value["workflow"] {
		t.Fatalf("RUL-backed guide result=%#v ok=%t expected revision=%d", guide, ok, rule.Revision)
	}
	if _, exposed := guide["value"]; exposed {
		t.Fatalf("guide copied the full Rule value: %#v", guide)
	}
	if _, exposed := guide["description"]; exposed {
		t.Fatalf("guide copied the Rule description: %#v", guide)
	}
	adrRule := tsk532CreateAcceptedGuideRule(t, server.Service, "guide.adr", map[string]string{"guidance": "ADR projection guidance."})
	if _, ok := tsk532CallResult(t, server, planner, "project/guide_bind", map[string]any{
		"subject": "adr", "rule": adrRule.ID, "reason": "Bind the accepted ADR guide",
	}); !ok {
		t.Fatal("ADR guide binding was rejected")
	}
	adrGuide, ok := tsk532CallResult(t, server, planner, "adr/guide", map[string]any{})
	if !ok || adrGuide["source"] != "rule" || adrGuide["rule"] != adrRule.ID || adrGuide["rule_revision"] != float64(adrRule.Revision) || adrGuide["guidance"] != "ADR projection guidance." {
		t.Fatalf("ADR RUL-backed guide=%#v ok=%t", adrGuide, ok)
	}
	if _, _, _, err := server.Service.ProjectGuideRead(context.Background(), "example", "journal"); err == nil {
		t.Fatal("service allowed an unbound cut-over guide")
	}
	if result, ok := tsk532CallResult(t, server, planner, "journal/guide", map[string]any{}); ok {
		t.Fatalf("unbound cut-over guide returned builtin: %#v", result)
	}
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "other", Subject: "task", RuleID: rule.ID, Reason: "reject cross-project binding", UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("cross-project Rule binding was accepted")
	}
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "example", Subject: "project_configuration", RuleID: rule.ID, Reason: "not applicable", UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("intentionally omitted subject binding was accepted")
	}
	if _, err := server.Service.ProjectGuideBind(context.Background(), service.ProjectGuideBindingInput{
		ProjectID: "example", Subject: "task", RuleID: "EXM-RUL999999", Reason: "rebind to missing Rule", UpdatedBy: "planner",
	}); err == nil {
		t.Fatal("rebind to missing Rule was accepted")
	}
	other := tsk532CreateAcceptedGuideRule(t, server.Service, "guide.task.v2", taskGuideRuleValue("rebound task guide"))
	beforeRebind, err := server.Service.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tsk532CallResult(t, server, planner, "project/guide_bind", map[string]any{
		"subject": "task", "rule": other.ID, "reason": "Rebind after the guide revision was updated",
	}); !ok {
		t.Fatal("reasoned rebind was rejected")
	}
	afterRebind, err := server.Service.ProjectConfigurationRead(context.Background(), "example")
	if err != nil || afterRebind.Revision != beforeRebind.Revision+1 || afterRebind.GuideBindings["task"] != other.ID {
		t.Fatalf("rebind configuration=%#v err=%v before=%d", afterRebind, err, beforeRebind.Revision)
	}
	guide, ok = tsk532CallResult(t, server, planner, "task/guide", map[string]any{})
	if !ok || guide["source"] != "rule" || guide["rule"] != other.ID || guide["workflow"] != "rebound task guide" {
		t.Fatalf("rebound guide=%#v ok=%t", guide, ok)
	}
	invalidValueBytes, _ := json.Marshal(map[string]string{"guidance": "wrong projection"})
	invalidValue := json.RawMessage(invalidValueBytes)
	if _, err := server.Service.RuleUpdateCurrent(context.Background(), service.RuleUpdateInput{
		ProjectID: "example", RuleID: other.ID, Value: &invalidValue, Reason: "test render mismatch", UpdatedBy: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	if result, ok := tsk532CallResult(t, server, planner, "task/guide", map[string]any{}); ok {
		t.Fatalf("projection mismatch fell back to builtin: %#v", result)
	}
	archivedRule := tsk532CreateAcceptedGuideRule(t, server.Service, "guide.task.archived", taskGuideRuleValue("accepted before archival"))
	if _, ok := tsk532CallResult(t, server, planner, "project/guide_bind", map[string]any{
		"subject": "task", "rule": archivedRule.ID, "reason": "test rebind to accepted Rule",
	}); !ok {
		t.Fatal("rebind before archival was rejected")
	}
	if _, err := server.Service.RuleArchiveCurrent(context.Background(), service.RuleArchiveInput{
		ProjectID: "example", RuleID: archivedRule.ID, Reason: "test fail-closed archival", ArchivedBy: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	if result, ok := tsk532CallResult(t, server, planner, "task/guide", map[string]any{}); ok {
		t.Fatalf("archived bound Rule fell back to builtin: %#v", result)
	}
	configuration, err := server.Service.ProjectConfigurationRead(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string]string{"task": "EXM-RUL999999"}
	if _, _, err := server.Service.ProjectConfigurationUpdate(authority.WithPlanner(context.Background()), service.ProjectConfigurationUpdateInput{
		ProjectID: "example", ExpectedRevision: configuration.Revision,
		Patch: service.ProjectConfigurationPatch{GuideBindings: &missing}, UpdatedBy: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	if result, ok := tsk532CallResult(t, server, planner, "task/guide", map[string]any{}); ok {
		t.Fatalf("missing bound Rule fell back to builtin: %#v", result)
	}
}

func taskGuideRuleValue(workflow string) map[string]string {
	return map[string]string{
		"workflow":     workflow,
		"review":       "Lead reviews and reworks the candidate.",
		"verification": "Lead verifies and integrates the candidate.",
		"completion":   "Planner accepts the submitted Track.",
		"boundaries":   "Worker implements, checks, and submits once.",
	}
}

func tsk532CreateProposedGuideRule(t *testing.T, svc *service.Service, name string, value map[string]string) model.Rule {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.RuleCreate(context.Background(), service.RuleCreateInput{Rule: model.Rule{
		ProjectID: "example", Title: "Guide " + name, Summary: "Proposed guide projection fixture.",
		Name: name, Value: raw, CreatedBy: "planner", UpdatedBy: "planner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := svc.RuleRead(context.Background(), "example", created.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func tsk532CreateAcceptedGuideRule(t *testing.T, svc *service.Service, name string, value map[string]string) model.Rule {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.RuleCreate(context.Background(), service.RuleCreateInput{Rule: model.Rule{
		ProjectID: "example", Title: "Guide " + name, Summary: "Accepted guide projection fixture.",
		Name: name, Value: raw, CreatedBy: "planner", UpdatedBy: "planner",
	}})
	if err != nil {
		t.Fatal(err)
	}
	status := model.RuleStatusAccepted
	if _, err := svc.RuleUpdateCurrent(context.Background(), service.RuleUpdateInput{
		ProjectID: "example", RuleID: created.EntityKey, Status: &status,
		ExpectedRevision: 1, Reason: "accept test guide", UpdatedBy: "planner",
	}); err != nil {
		t.Fatal(err)
	}
	rule, err := svc.RuleRead(context.Background(), "example", created.EntityKey)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func tsk532Call(t *testing.T, server *Server, sessionID, action string, input map[string]any) map[string]any {
	t.Helper()
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0", "id": 532, "method": "tools/call",
		"params": map[string]any{"name": "call", "arguments": map[string]any{
			"session": sessionID, "action": action, "input": input,
		}},
	}))
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP call result=%#v", response)
	}
	return result
}

func tsk532CallResult(t *testing.T, server *Server, sessionID, action string, input map[string]any) (map[string]any, bool) {
	t.Helper()
	result := tsk532Call(t, server, sessionID, action, input)
	if result["isError"] == true {
		return nil, false
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("MCP call lacks structuredContent: %#v", result)
	}
	if structured["ok"] == false {
		return nil, false
	}
	if value, ok := structured["result"].(map[string]any); ok {
		return value, true
	}
	return structured, true
}

func reflectStringList(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
