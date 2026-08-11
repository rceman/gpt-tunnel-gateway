package train

import (
	"strings"
	"testing"
	"time"
)

func TestOwnedRuntimeBindingUsesServerDerivedWorktree(t *testing.T) {
	stateDir := "/var/lib/gpt-tunnel"
	want := "/var/lib/gpt-tunnel/train-worktrees/gateway/GTW-TRN7"
	if got := ExpectedWorktreePath(stateDir, "gateway", "GTW-TRN7"); got != want {
		t.Fatalf("unexpected derived worktree: got %q want %q", got, want)
	}
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     "gateway",
		TrainID:       "GTW-TRN7",
		WorktreePath:  want,
		AgentID:       "agent-1",
		SessionKey:    "gateway_master",
		RunID:         "GTW-TSK179-RUN1",
		StartedAt:     time.Unix(1, 0).UTC(),
	}
	if err := ValidateRuntimeBinding(binding, stateDir); err != nil {
		t.Fatal(err)
	}
	binding.WorktreePath = "/tmp/arbitrary-worktree"
	if err := ValidateRuntimeBinding(binding, stateDir); err == nil {
		t.Fatal("arbitrary caller worktree path was accepted")
	}
}

func TestOwnedRuntimeBindingRejectsUnsafeTrainIdentity(t *testing.T) {
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     "gateway",
		TrainID:       "../escape",
		WorktreePath:  ExpectedWorktreePath("/state", "gateway", "../escape"),
		AgentID:       "agent-1",
		SessionKey:    "gateway_master",
		RunID:         "GTW-TSK179-RUN1",
		StartedAt:     time.Now().UTC(),
	}
	if err := ValidateRuntimeBinding(binding, "/state"); err == nil {
		t.Fatal("unsafe train identity was accepted")
	}
	if strings.Contains(ExpectedWorktreePath("/state", "gateway", "../escape"), "/train-worktrees/gateway/../") {
		t.Fatal("derived path retained traversal component")
	}
}
