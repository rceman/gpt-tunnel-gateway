package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestHotfixExecutionReceiptPreparedDeliveredAndExactBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	expected := hotfixExecutionReceipt{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/repair", TaskID: "EXM-TSK1",
		TaskRevision: 2, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AgentID: "agent",
		SessionKey: "session", Profile: "coding", WorktreePath: "/srv/repair",
		Message: "execute", CreatedAt: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC),
	}
	prepared := expected
	prepared.State = "prepared"
	if err := writeHotfixExecutionReceipt(path, prepared); err != nil {
		t.Fatal(err)
	}
	read, err := readHotfixExecutionReceipt(path)
	if err != nil || read.State != "prepared" || read.Delivered || !read.matches(expected) {
		t.Fatalf("prepared receipt=%#v err=%v", read, err)
	}
	delivered := prepared
	delivered.State, delivered.Delivered = "delivered", true
	if err := writeHotfixExecutionReceipt(path, delivered); err != nil {
		t.Fatal(err)
	}
	read, err = readHotfixExecutionReceipt(path)
	if err != nil || read.State != "delivered" || !read.Delivered || !read.matches(expected) {
		t.Fatalf("delivered receipt=%#v err=%v", read, err)
	}
	changed := expected
	changed.Head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if read.matches(changed) {
		t.Fatal("receipt accepted a different generation binding")
	}
}

func TestHotfixExecutionGenerationIsStableAndSeparated(t *testing.T) {
	args := []string{"example", "refs/heads/hotfix/repair", "EXM-TSK1", "2", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "agent", "session", "coding", "/srv/repair", "execute"}
	one := hotfixExecutionGeneration(args[0], args[1], args[2], 2, args[4], args[5], args[6], args[7], args[8], args[9])
	if one != hotfixExecutionGeneration(args[0], args[1], args[2], 2, args[4], args[5], args[6], args[7], args[8], args[9]) {
		t.Fatal("identical dispatch binding generated different generations")
	}
	for index := 3; index < 10; index++ {
		changed := append([]string{}, args...)
		changed[index] = fmt.Sprintf("changed-%d", index)
		revision := 2
		if index == 3 {
			revision = 3
		}
		if one == hotfixExecutionGeneration(changed[0], changed[1], changed[2], revision, changed[4], changed[5], changed[6], changed[7], changed[8], changed[9]) {
			t.Fatalf("binding field %d did not create a new generation", index)
		}
	}
}

func TestHotfixExecutionLockSerializesDispatchGeneration(t *testing.T) {
	dir := t.TempDir()
	first, err := acquireHotfixExecutionLock(dir, "generation")
	if err != nil {
		t.Fatal(err)
	}
	if second, secondErr := acquireHotfixExecutionLock(dir, "generation"); secondErr == nil {
		_ = second.Release()
		t.Fatal("concurrent dispatch acquired the same generation lock")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if second, secondErr := acquireHotfixExecutionLock(dir, "generation"); secondErr != nil {
		t.Fatalf("released generation lock was not reusable: %v", secondErr)
	} else if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestHotfixExecutionReceiptKeepsExactLaneWorktree(t *testing.T) {
	receipt := hotfixExecutionReceipt{WorktreePath: "/srv/hotfix/repair"}
	expected := hotfixExecutionReceipt{WorktreePath: "/srv/hotfix/repair"}
	if receipt.WorktreePath != expected.WorktreePath {
		t.Fatal("exact managed hotfix worktree binding was not preserved")
	}
	wrong := expected
	wrong.WorktreePath = "/home/user/gpt-tunnel-gateway"
	if receipt.WorktreePath == wrong.WorktreePath {
		t.Fatal("receipt accepted canonical main as the hotfix execution worktree")
	}
}

func TestHotfixExecutionDeliveredReceiptIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	receipt := hotfixExecutionReceipt{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/repair", TaskID: "EXM-TSK1",
		TaskRevision: 2, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AgentID: "agent",
		SessionKey: "session", Profile: "coding", WorktreePath: "/srv/repair",
		Message: "execute", State: "delivered", Delivered: true,
		CreatedAt: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC),
	}
	if err := writeHotfixExecutionReceipt(path, receipt); err != nil {
		t.Fatal(err)
	}
	first, err := readHotfixExecutionReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := readHotfixExecutionReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !first.matches(receipt) || !second.matches(first) || second.State != "delivered" || !second.Delivered {
		t.Fatalf("delivered receipt was not stable/idempotent: first=%#v second=%#v", first, second)
	}
}

func TestHotfixExecutionPreparedReceiptFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	receipt := hotfixExecutionReceipt{
		ProjectID: "example", HotfixRef: "refs/heads/hotfix/repair", TaskID: "EXM-TSK1",
		TaskRevision: 2, Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AgentID: "agent",
		SessionKey: "session", Profile: "coding", WorktreePath: "/srv/repair",
		Message: "execute", State: "prepared", CreatedAt: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC),
	}
	if err := writeHotfixExecutionReceipt(path, receipt); err != nil {
		t.Fatal(err)
	}
	read, err := readHotfixExecutionReceipt(path)
	if err != nil || read.State != "prepared" || read.Delivered || !read.matches(receipt) {
		t.Fatalf("prepared receipt=%#v err=%v", read, err)
	}
	if read.State == "delivered" || read.Delivered {
		t.Fatal("prepared receipt was treated as delivered")
	}
}
