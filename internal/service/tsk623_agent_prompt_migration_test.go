package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestTSK623UpgradedLocalSchemaSupportsNormalAgentPromptAdmission(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiersSetup(t)
	installTSK623PreviousLocalSchemaForService(t, s.Config.StateDir)
	testServiceWithDurability(t, s)

	command := filepath.Join(t.TempDir(), "airelay")
	if err := os.WriteFile(command, []byte("#!/bin/sh\ncase \"$1\" in\nprompt) printf 'prompt delivered' ;;\n*) exit 0 ;;\nesac\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = command

	receipt, err := s.AgentPromptAsync(context.Background(), AgentPromptInput{
		ProjectID: "example",
		Message:   "TSK623 normal prompt after Local upgrade",
	})
	if err != nil {
		t.Fatalf("normal Agent prompt admission failed after upgrade: %v", err)
	}
	if receipt.OperationID == "" || receipt.Status != "accepted" {
		t.Fatalf("initial prompt receipt=%#v", receipt)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, statusErr := s.AgentIPCOperationStatus(context.Background(), receipt.OperationID, "agent-prompt")
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		prompt, ok := value.(AgentPromptReceipt)
		if ok && (prompt.Status == "completed" || prompt.Status == "failed") {
			if prompt.Status != "completed" || prompt.Result == nil || !prompt.Result.Delivered {
				t.Fatalf("normal prompt terminal receipt=%#v", prompt)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("normal prompt operation %s did not reach a terminal receipt", receipt.OperationID)
}

func installTSK623PreviousLocalSchemaForService(t *testing.T, stateDir string) {
	t.Helper()
	_, localPath := sqlitestore.Paths(stateDir)
	raw, err := upstream.Open(upstream.Config{Path: localPath})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	statements := []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE local_operation_sequences (project_id TEXT PRIMARY KEY, project_code TEXT NOT NULL, next_number INTEGER NOT NULL CHECK(next_number BETWEEN 1 AND 9007199254740991))`,
		`CREATE TABLE local_operations (operation_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, project_code TEXT NOT NULL, operation_number INTEGER NOT NULL, mutation_id TEXT NOT NULL UNIQUE, kind TEXT NOT NULL, status TEXT NOT NULL, result_payload BLOB, error TEXT NOT NULL DEFAULT '', recovery_reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE local_events (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, project_id TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE local_messages (id TEXT PRIMARY KEY, session_id TEXT, payload BLOB NOT NULL, recorded_at TEXT NOT NULL)`,
		`CREATE TABLE local_logs (id TEXT PRIMARY KEY, level TEXT NOT NULL, component TEXT NOT NULL, event TEXT NOT NULL, payload BLOB NOT NULL, recorded_at TEXT NOT NULL, project_id TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE local_retention (name TEXT PRIMARY KEY, cutoff_at TEXT NOT NULL)`,
		`CREATE TABLE local_callback_epochs (epoch_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, agent_id TEXT NOT NULL DEFAULT '', session_key TEXT NOT NULL, armed_at TEXT NOT NULL, busy_seen INTEGER NOT NULL DEFAULT 0, idle_observations INTEGER NOT NULL DEFAULT 0, emitted_at TEXT)`,
		`CREATE TABLE local_agents (project_id TEXT NOT NULL, agent_id TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(project_id,agent_id))`,
		`CREATE TABLE local_sessions (session_id TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','ended')))`,
		`CREATE TABLE local_token_usage_events (event_id TEXT PRIMARY KEY, session_id TEXT NOT NULL, input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0), output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0), total_tokens INTEGER NOT NULL CHECK(total_tokens >= 0), recorded_at TEXT NOT NULL)`,
		`CREATE TABLE local_token_usage (session_id TEXT PRIMARY KEY, request_count INTEGER NOT NULL CHECK(request_count >= 0), input_tokens INTEGER NOT NULL CHECK(input_tokens >= 0), output_tokens INTEGER NOT NULL CHECK(output_tokens >= 0), total_tokens INTEGER NOT NULL CHECK(total_tokens >= 0), updated_at TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX local_operations_mutation_idx ON local_operations(mutation_id)`,
		`CREATE INDEX local_operations_project_idx ON local_operations(project_id,operation_number)`,
		`CREATE INDEX local_operation_sequences_project_idx ON local_operation_sequences(project_id)`,
		`CREATE INDEX local_events_kind_idx ON local_events(kind,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_events_recorded_idx ON local_events(recorded_at DESC,id DESC)`,
		`CREATE INDEX local_events_project_idx ON local_events(project_id,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_logs_filter_idx ON local_logs(level,component,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_logs_recorded_idx ON local_logs(recorded_at DESC,id DESC)`,
		`CREATE INDEX local_logs_project_idx ON local_logs(project_id,recorded_at DESC,id DESC)`,
		`CREATE INDEX local_callback_epochs_pending_idx ON local_callback_epochs(emitted_at,armed_at,epoch_id)`,
		`CREATE INDEX local_agents_project_idx ON local_agents(project_id,agent_id)`,
		`CREATE INDEX local_sessions_updated_idx ON local_sessions(updated_at,session_id)`,
		`CREATE UNIQUE INDEX local_token_usage_events_session_idx ON local_token_usage_events(session_id,event_id)`,
	}
	for _, statement := range statements {
		if _, err := raw.Exec(ctx, statement); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	markers := [][2]any{
		{202609080505, "create local baseline"},
		{202609102010, "create local token usage ledger"},
		{202609141200, "create local durable operations"},
	}
	for _, marker := range markers {
		if _, err := raw.Exec(ctx, `INSERT INTO schema_migrations(version,name) VALUES(?,?)`, marker[0], marker[1]); err != nil {
			raw.Close()
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
}
