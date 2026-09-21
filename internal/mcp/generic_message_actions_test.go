package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func tsk589MessageCall(t *testing.T, server *Server, sessionID, action string, input map[string]any) (map[string]any, bool) {
	t.Helper()
	response := callMCP(t, server, mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      action,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "call",
			"arguments": map[string]any{
				"session": sessionID,
				"action":  action,
				"input":   input,
			},
		},
	}))
	structured := genericStructured(t, response)
	if structured["is_error"] == true {
		return nil, false
	}
	result, ok := structured["result"].(map[string]any)
	if !ok {
		t.Fatalf("message action result is not an object: %#v", structured)
	}
	return result, true
}

func TestTSK589MessageActionsAreClosedAndSessionBound(t *testing.T) {
	server := newSessionTestServer(t)
	server.tools()
	server.Service.SetMessageNotifier(func(_ context.Context, _ service.MessageNotification) error { return nil })
	for _, action := range []string{"message/create", "message/read", "message/list", "message/cancel"} {
		if _, ok := server.genericActions[action]; !ok {
			t.Fatalf("message action %q was not registered", action)
		}
		if server.genericActions[action].InputSchema["additionalProperties"] != false || server.genericActions[action].OutputSchema["additionalProperties"] != false {
			t.Fatalf("message action %q is not closed: %#v", action, server.genericActions[action])
		}
	}
	readProperties := schemaProperties(server.genericActions["message/read"].OutputSchema)
	if _, ok := readProperties["cancelled_by"]; !ok {
		t.Fatal("message/read omitted optional cancelled_by")
	}
	for _, required := range stringList(server.genericActions["message/read"].OutputSchema["required"]) {
		if required == "cancelled_by" {
			t.Fatal("message/read made cancelled_by required")
		}
	}
	if got := messageReadValue(model.Message{CancelledBy: "HOM_EXM_W_abcde"})["cancelled_by"]; got != "HOM_EXM_W_abcde" {
		t.Fatalf("message/read value omitted cancelled_by: %#v", got)
	}
	createProperties := schemaProperties(server.genericActions["message/create"].InputSchema)
	for _, key := range []string{"to_role", "body", "title", "in_reply_to"} {
		if _, ok := createProperties[key]; !ok {
			t.Fatalf("message/create missing %q: %#v", key, createProperties)
		}
	}
	for _, forbidden := range []string{"project_id", "from_role", "from_session", "sender"} {
		if _, ok := createProperties[forbidden]; ok {
			t.Fatalf("message/create exposes forgeable %q", forbidden)
		}
	}

	roles := []string{durableSession.RolePlanner, durableSession.RoleLead, durableSession.RoleAdvisor, durableSession.RoleWorker}
	sessions := make(map[string]string, len(roles))
	for _, role := range roles {
		sessions[role] = genericSessionWithRole(t, server.Service, "example", role)
	}
	var created []string
	for _, fromRole := range roles {
		toRole := durableSession.RoleWorker
		if fromRole == durableSession.RoleWorker {
			toRole = durableSession.RolePlanner
		}
		result, ok := tsk589MessageCall(t, server, sessions[fromRole], "message/create", map[string]any{"to_role": toRole, "body": fromRole + " route"})
		if !ok || len(result) != 2 || result["message"] == nil || result["status"] != "unread" {
			t.Fatalf("authenticated %s create result=%#v ok=%v", fromRole, result, ok)
		}
		created = append(created, result["message"].(string))
	}
	for _, messageID := range created {
		result, ok := tsk589MessageCall(t, server, sessions[durableSession.RoleWorker], "message/read", map[string]any{"message": messageID})
		if messageID == created[len(created)-1] {
			if ok {
				t.Fatal("worker read a Planner-targeted message")
			}
			continue
		}
		if !ok {
			t.Fatalf("worker could not read worker-target message %s: %#v", messageID, result)
		}
		for _, key := range []string{"message", "from_role", "from_session", "to_role", "body", "status", "created_at", "read_at", "read_session"} {
			if _, present := result[key]; !present {
				t.Fatalf("full message/read record omitted %q: %#v", key, result)
			}
		}
		if _, present := result["state"]; present {
			t.Fatalf("message/read leaked internal state field: %#v", result)
		}
	}
	inbox, ok := tsk589MessageCall(t, server, sessions[durableSession.RoleWorker], "message/list", map[string]any{})
	if !ok {
		t.Fatalf("worker message/list failed: %#v", inbox)
	}
	if _, present := inbox["messages"]; present {
		t.Fatalf("message/list used non-contract messages field: %#v", inbox)
	}
	items, ok := inbox["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("worker message/list items=%#v", inbox)
	}
	for _, item := range items {
		row := item.(map[string]any)
		for _, key := range []string{"message", "from_role", "to_role", "status", "created_at"} {
			if _, present := row[key]; !present {
				t.Fatalf("compact message/list row omitted %q: %#v", key, row)
			}
		}
		if _, present := row["state"]; present {
			t.Fatalf("message/list leaked internal state field: %#v", row)
		}
	}
	if _, ok := tsk589MessageCall(t, server, sessions[durableSession.RolePlanner], "message/create", map[string]any{"to_role": durableSession.RoleWorker, "body": "forged", "from_session": sessions[durableSession.RoleWorker]}); ok {
		t.Fatal("forged sender field was accepted")
	}
	if _, ok := tsk589MessageCall(t, server, sessions[durableSession.RolePlanner], "message/create", map[string]any{"to_role": durableSession.RoleWorker, "body": "forged", "project_id": "other"}); ok {
		t.Fatal("caller-selected project field was accepted")
	}

	other := genericSessionWithRole(t, server.Service, "other", durableSession.RoleWorker)
	if _, ok := tsk589MessageCall(t, server, other, "message/read", map[string]any{"message": created[0]}); ok {
		t.Fatal("cross-project message/read succeeded")
	}
	if result, ok := tsk589MessageCall(t, server, other, "message/list", map[string]any{}); !ok || len(result["items"].([]any)) != 0 {
		t.Fatalf("cross-project message/list exposed data: %#v ok=%v", result, ok)
	}

	admin, err := mcpSQLiteSessionStore(t, server.Service).CreateAdmin(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tsk589MessageCall(t, server, admin.ID, "message/list", map[string]any{}); ok {
		t.Fatal("Admin Session reached ordinary message action")
	}

	encoded, err := json.Marshal(server.genericActions["message/create"].OutputSchema)
	if err != nil || len(encoded) == 0 {
		t.Fatalf("message output schema unavailable: %v", err)
	}
}
