package session

import (
	"testing"
	"time"
)

func TestRecordMCPTokenUsagePersistsBoundedSessionTelemetry(t *testing.T) {
	store := NewStore(t.TempDir())
	record, err := store.Create(CreateInput{ProjectID: "example", ProjectCode: "EXM", Role: RoleDelivery, SessionType: SessionTypeChatGPT})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.RecordMCPTokenUsage(record.ID, 3, 5, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.MCPInputTokens != 3 || updated.MCPOutputTokens != 5 || updated.MCPTokenTotal != 8 || updated.MCPCallCount != 1 || reloaded.MCPTokenTotal != 8 {
		t.Fatalf("telemetry updated=%#v reloaded=%#v", updated, reloaded)
	}
}
