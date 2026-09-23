package mcp

import (
	"testing"

	durableSession "github.com/rceman/gpt-tunnel-gateway/internal/session"
)

func TestTSK660TrackAndMilestoneActionContracts(t *testing.T) {
	server := newSessionTestServer(t)
	entries := server.genericActionRegistry(server.tools())
	for _, path := range []string{"track/create", "track/read", "track/update", "track/append_task", "track/remove_task", "track/cancel", "track/submit", "track/accept", "track/archive", "track/list", "track/query", "track/history", "milestone/append_task", "milestone/remove_task"} {
		if _, ok := entries[path]; !ok {
			t.Fatalf("missing action %q", path)
		}
	}
	if entries["track/submit"].AuthorityRole != durableSession.RoleLead || entries["track/accept"].AuthorityRole != durableSession.RolePlanner {
		t.Fatalf("Track review authority=%#v/%#v", entries["track/submit"], entries["track/accept"])
	}
	for _, path := range []string{"track/create", "track/update", "track/append_task", "track/remove_task", "track/cancel", "track/submit", "track/accept", "track/archive", "milestone/append_task", "milestone/remove_task"} {
		entry := entries[path]
		if entry.AuthorityRole == actionRoleWorkflow || !entry.SessionBound || !entry.LocalReceiptOnly {
			t.Fatalf("mutation authority binding for %s=%#v", path, entry)
		}
		if entry.InputSchema["additionalProperties"] != false || entry.OutputSchema["additionalProperties"] != false {
			t.Fatalf("%s schema is not closed", path)
		}
	}
	updateProperties := schemaProperties(entries["milestone/update"].InputSchema)
	if _, ok := updateProperties["tasks"]; ok {
		t.Fatal("milestone/update exposes forbidden tasks membership field")
	}
	createRequired := stringList(entries["track/create"].InputSchema["required"])
	if len(createRequired) != 3 || createRequired[0] != "milestone" || createRequired[1] != "tasks" || createRequired[2] != "title" {
		t.Fatalf("track/create required=%v", createRequired)
	}
	readProperties := schemaProperties(entries["track/read"].OutputSchema)
	for _, field := range []string{"key", "revision", "milestone", "title", "status", "tasks", "dispatched_tasks", "review", "cancelled_at", "cancelled_by", "cancelled_reason"} {
		if _, ok := readProperties[field]; !ok {
			t.Fatalf("track/read output missing %q", field)
		}
	}
}
