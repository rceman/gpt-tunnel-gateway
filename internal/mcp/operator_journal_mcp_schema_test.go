package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func operatorMCPOutputFixture(t *testing.T, event model.OperatorJournalEvent) map[string]any {
	t.Helper()
	return normalizeObject(map[string]any{"event": event, "operation": map[string]any{"operation_id": "journal-op", "project_id": "example", "status": "recorded"}})
}

func TestOperatorJournalMCPSchemaParityRejectsInvalidOutputsAndInputs(t *testing.T) {
	server := &Server{Service: service.New(config.Config{})}
	tools := server.tools()
	now := time.Now().UTC()
	session := "session"
	baseEvent := model.OperatorJournalEvent{SchemaVersion: model.OperatorJournalSchemaVersion, ID: "EXM-OPR1", ProjectID: "example", SessionID: &session, Kind: model.OperatorUserTalk, Summary: "context", Content: model.OperatorJournalContent{Facts: []string{"fact"}}, References: model.OperatorJournalReferences{}, Actor: "owner", OccurredAt: now, RecordedAt: now}
	valid := operatorMCPOutputFixture(t, baseEvent)
	if err := validateOutputValue(tools["operator_record"].OutputSchema, valid); err != nil {
		t.Fatalf("valid operator output rejected: %v", err)
	}
	correction := cloneOperatorMCPValue(t, valid)
	event := correction["event"].(map[string]any)
	event["id"] = "EXM-OPR2"
	event["kind"] = "correction"
	event["supersedes_event_id"] = "EXM-OPR1"
	if err := validateOutputValue(tools["operator_record"].OutputSchema, correction); err != nil {
		t.Fatalf("valid correction output rejected: %v", err)
	}
	invalidOutputs := []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong_session_type", func(value map[string]any) { value["event"].(map[string]any)["session_id"] = float64(1) }},
		{"empty_session", func(value map[string]any) { value["event"].(map[string]any)["session_id"] = "" }},
		{"overflow_event_id", func(value map[string]any) { value["event"].(map[string]any)["id"] = "EXM-OPR9007199254740992" }},
		{"overflow_adr", func(value map[string]any) {
			value["event"].(map[string]any)["references"].(map[string]any)["adrs"] = []any{"EXM-A9007199254740992"}
		}},
		{"correction_without_supersedes", func(value map[string]any) { value["event"].(map[string]any)["kind"] = "correction" }},
		{"non_correction_with_supersedes", func(value map[string]any) { value["event"].(map[string]any)["supersedes_event_id"] = "EXM-OPR1" }},
	}
	for _, test := range invalidOutputs {
		value := cloneOperatorMCPValue(t, valid)
		test.edit(value)
		if err := validateOutputValue(tools["operator_record"].OutputSchema, value); err == nil {
			t.Errorf("invalid output %s accepted", test.name)
		}
	}
	validInput := normalizeObject(operatorMCPArguments("example", "user_talk", "context"))
	if err := validateSchemaValue(tools["operator_record"].InputSchema, validInput, "$input"); err != nil {
		t.Fatalf("valid operator input rejected: %v", err)
	}
	invalidInputs := []struct {
		name string
		edit func(map[string]any)
	}{
		{"summary_too_long", func(value map[string]any) { value["summary"] = strings.Repeat("x", model.MaxOperatorSummaryBytes+1) }},
		{"actor_too_long", func(value map[string]any) { value["actor"] = strings.Repeat("x", model.MaxOperatorActorBytes+1) }},
		{"session_empty", func(value map[string]any) { value["session_id"] = "" }},
		{"overflow_supersedes", func(value map[string]any) { value["supersedes_event_id"] = "EXM-OPR9007199254740992" }},
		{"too_many_facts", func(value map[string]any) {
			value["content"].(map[string]any)["facts"] = make([]any, model.MaxOperatorContentItems+1)
		}},
		{"overflow_adr", func(value map[string]any) {
			value["references"].(map[string]any)["adrs"] = []any{"EXM-A9007199254740992"}
		}},
	}
	for _, test := range invalidInputs {
		value := cloneOperatorMCPValue(t, validInput)
		test.edit(value)
		if err := validateSchemaValue(tools["operator_record"].InputSchema, value, "$input"); err == nil {
			t.Errorf("invalid input %s accepted", test.name)
		}
	}
	historyInput := normalizeObject(map[string]any{"project_id": "example", "after_event_id": "EXM-OPR9007199254740991", "limit": float64(model.MaxOperatorHistoryLimit)})
	if err := validateSchemaValue(tools["operator_history"].InputSchema, historyInput, "$input"); err != nil {
		t.Fatalf("valid history input rejected: %v", err)
	}
	historyInput["limit"] = float64(model.MaxOperatorHistoryLimit + 1)
	if err := validateSchemaValue(tools["operator_history"].InputSchema, historyInput, "$input"); err == nil {
		t.Fatal("history limit above model maximum accepted")
	}
}
