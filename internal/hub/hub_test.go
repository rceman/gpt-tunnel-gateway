package hub

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestTransactionPushesAndVerifiesWithoutDirtyingClone(t *testing.T) {
	_, work, base := testutil.RepoWithBareRemote(t)
	c := config.Config{StateDir: t.TempDir(), MaxReadBytes: 1 << 20, MaxListItems: 100, Hub: config.HubConfig{Root: work, Remote: "origin", Branch: "main", ProtocolRoot: "protocol/v4", AuthorName: "Gateway", AuthorEmail: "gateway@example.invalid"}}
	store := Store{Config: c}
	tx, err := store.Transact(context.Background(), base, "test: write", func(w string) ([]string, error) {
		path := "protocol/v4/test.json"
		return []string{path}, WriteJSON(w, path, map[string]any{"ok": true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.After == base {
		t.Fatal("commit did not advance")
	}
	data, err := store.ReadFile(context.Background(), "protocol/v4/test.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty")
	}
	status := testutil.Git(t, work, "status", "--porcelain")
	if status != "" {
		t.Fatalf("hub clone dirtied: %q", status)
	}
	if _, err := os.Stat(filepath.Join(work, "protocol")); !os.IsNotExist(err) {
		t.Fatalf("active clone modified")
	}
}
