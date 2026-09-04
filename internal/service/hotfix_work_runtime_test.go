package service

import (
	"os"
	"testing"
)

func TestHotfixExecutionReceiptRequiresExplicitState(t *testing.T) {
	path := t.TempDir() + "/receipt.json"
	payload := `{"project_id":"example","hotfix_ref":"refs/heads/hotfix/repair","task_id":"EXM-TSK1","task_revision":1,"head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","agent_id":"agent","session_key":"session","profile":"coding","worktree_path":"/tmp/repair","message":"execute","delivered":true,"created_at":"2026-09-04T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readHotfixExecutionReceipt(path); err == nil {
		t.Fatal("receipt without explicit state was accepted")
	}
}
