package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
	"github.com/rceman/gpt-tunnel-gateway/internal/testutil"
)

func TestProjectTokenStaticGrantIsStableAndIdentityBound(t *testing.T) {
	remote, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	stateDir := t.TempDir()
	cfg := config.Config{
		GatewayID:    "HOM",
		StateDir:     stateDir,
		MaxReadBytes: 1 << 20,
		Projects: map[string]config.ProjectConfig{
			"example": {Root: root, Mirror: filepath.Join(stateDir, "example.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "EXM", AirelaySessionKey: "example_worker"},
		},
	}
	db, err := sqlitestore.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := NewWithDurabilityDeferredWorkers(cfg, db)
	ctx := context.Background()
	first, err := svc.ProjectToken(ctx, ProjectTokenInput{
		Root:   root,
		Remote: remote,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ProjectToken(ctx, ProjectTokenInput{
		Root:    root,
		Remotes: map[string]string{"origin": remote},
	})
	if err != nil || second.Token != first.Token {
		t.Fatalf("repeat static token=%#v err=%v", second, err)
	}
	byCode, err := svc.ProjectToken(ctx, ProjectTokenInput{
		Project: "EXM",
		Root:    filepath.Join(stateDir, "not-a-repository"),
		Remote:  "ignored",
	})
	if err != nil || byCode.ProjectID != "example" || byCode.Token != first.Token {
		t.Fatalf("explicit project-code token=%#v err=%v", byCode, err)
	}
	if _, err := svc.ProjectToken(ctx, ProjectTokenInput{
		Root:   root,
		Remote: "ssh://conflicting.invalid/example.git",
	}); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting repository identity was accepted: %v", err)
	}
	if _, err := svc.ProjectToken(ctx, ProjectTokenInput{
		Root:   filepath.Join(stateDir, "unregistered"),
		Remote: remote,
	}); err == nil || !strings.Contains(err.Error(), "not a registered project") {
		t.Fatalf("unregistered repository identity was accepted: %v", err)
	}
}

func TestProjectTokenAmbiguousRegisteredRootFailsClosed(t *testing.T) {
	remote, root, _ := testutil.RepoWithBareRemote(t)
	testutil.Git(t, root, "remote", "set-head", "origin", "main")
	svc := New(config.Config{
		GatewayID: "HOM",
		StateDir:  t.TempDir(),
		Projects: map[string]config.ProjectConfig{
			"first":  {Root: root, Mirror: filepath.Join(t.TempDir(), "first.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "ONE", AirelaySessionKey: "first_worker"},
			"second": {Root: root, Mirror: filepath.Join(t.TempDir(), "second.git"), Remote: "origin", DefaultBranch: "main", ProjectCode: "TWO", AirelaySessionKey: "second_worker"},
		},
	})
	if _, err := svc.ProjectToken(context.Background(), ProjectTokenInput{
		Root:   root,
		Remote: remote,
	}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous registered root was not rejected: %v", err)
	}
}
