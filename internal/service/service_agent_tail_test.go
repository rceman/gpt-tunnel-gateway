package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
	"github.com/rceman/gpt-tunnel-gateway/internal/hub"
	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestHistoricalOperationalPathsAreReadOnly(t *testing.T) {
	s, hubRevision, _ := testService(t)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "historical-run-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	active := strings.Replace(string(fixture), `"status": "succeeded"`, `"status": "awaiting_result"`, 1)
	active = strings.Replace(active, `"gateway_id": "home_pc"`, `"gateway_id": "test_gateway"`, 1)
	path := s.runPath("example", "11111111-1111-4111-8111-111111111111")
	tx, err := s.Hub.Transact(context.Background(), hubRevision, "test: historical active run", func(worktree string) ([]string, error) {
		return []string{path}, hub.WriteText(worktree, path, active)
	})
	if err != nil {
		t.Fatal(err)
	}
	foreign := strings.Replace(active, `"id": "11111111-1111-4111-8111-111111111111"`, `"id": "22222222-2222-4222-8222-222222222222"`, 1)
	foreign = strings.Replace(foreign, `"gateway_id": "test_gateway"`, `"gateway_id": "other_gateway"`, 1)
	foreignPath := s.runPath("example", "22222222-2222-4222-8222-222222222222")
	foreignTx, err := s.Hub.Transact(context.Background(), tx.After, "test: foreign historical active run", func(worktree string) ([]string, error) {
		return []string{foreignPath}, hub.WriteText(worktree, foreignPath, foreign)
	})
	if err != nil {
		t.Fatal(err)
	}
	tx = foreignTx
	before, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.RunRead(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunAgentTail(context.Background(), run.ID, 4); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("historical agent tail was not rejected: %v", err)
	}
	if _, err := s.RunCancel(context.Background(), run.ID, tx.After); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("historical cancellation was not rejected: %v", err)
	}
	sweep, err := s.RunSweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sweep.Checked != 0 || len(sweep.Items) != 0 {
		t.Fatalf("historical sweep was treated as operational work: %#v", sweep)
	}
	if _, err := s.updateRun(context.Background(), run, tx.After, "test: historical update"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("historical shared update was not rejected: %v", err)
	}
	if _, err := s.failRun(context.Background(), run, model.Task{}, "failed", "test", tx.After); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("historical synthetic failure was not rejected: %v", err)
	}
	after, err := s.Hub.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("historical run bytes changed after rejected operations")
	}
	current, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current != tx.After {
		t.Fatalf("historical operation changed hub revision: got %s want %s", current, tx.After)
	}
}

func TestGatewayCompletionPathRejectsOverridesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	completion := filepath.Join(dir, "runs", "run", "completion.json")
	if err := fsutil.WriteJSONAtomic(completion, map[string]any{"ok": true}, 0o600); err != nil {
		t.Fatal(err)
	}
	run := model.Run{CompletionPath: completion}
	got, err := gatewayCompletionPath(run, filepath.Join(dir, "runs", "run", ".", "completion.json"))
	if err != nil || got != completion {
		t.Fatalf("normalized gateway path rejected: %q %v", got, err)
	}
	other := filepath.Join(dir, "source-tree-completion.json")
	if err := os.WriteFile(other, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayCompletionPath(run, other); err == nil {
		t.Fatal("arbitrary completion path accepted")
	}
	symlink := filepath.Join(dir, "runs", "symlink", "completion.json")
	if err := os.MkdirAll(filepath.Dir(symlink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := gatewayCompletionPath(model.Run{CompletionPath: symlink}, ""); err == nil {
		t.Fatal("symlink completion path accepted")
	}
}
