package train

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/fsutil"
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

func TestStartRejectsDifferentAgentSessionForExistingTrainRuntime(t *testing.T) {
	stateDir := t.TempDir()
	train := reviewedTrainForIntegration(t)
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     train.ProjectID,
		TrainID:       train.ID,
		WorktreePath:  ExpectedWorktreePath(stateDir, train.ProjectID, train.ID),
		AgentID:       "agent-1",
		SessionKey:    "gateway_master",
		RunID:         "GTW-TSK179-RUN1",
		StartedAt:     time.Now().UTC(),
	}
	if err := ValidateRuntimeBinding(binding, stateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(RuntimePath(stateDir, train.ProjectID, train.ID)), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteFileAtomic(RuntimePath(stateDir, train.ProjectID, train.ID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Start(t.Context(), StartInput{
		ProjectID:       train.ProjectID,
		TrainID:         train.ID,
		ResolvedAgentID: "agent-2",
		SessionKey:      "other_master",
	}, StartDependencies{
		Train:    train,
		StateDir: stateDir,
	})
	if err == nil || !strings.Contains(err.Error(), "different Agent/session binding") {
		t.Fatalf("different Train owner was accepted: %v", err)
	}
}

func TestRetireRuntimeForRestartKeepsLaneBindingAndRemovesDispatchReceipt(t *testing.T) {
	stateDir := t.TempDir()
	train := reviewedTrainForIntegration(t)
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     train.ProjectID,
		TrainID:       train.ID,
		WorktreePath:  ExpectedWorktreePath(stateDir, train.ProjectID, train.ID),
		AgentID:       "agent-1",
		SessionKey:    "gateway_master",
		RunID:         "GTW-TSK179-RUN1",
		StartedAt:     time.Now().UTC(),
	}
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteFileAtomic(RuntimePath(stateDir, train.ProjectID, train.ID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteFileAtomic(dispatchReceiptPath(stateDir, train.ProjectID, train.ID), []byte(`{"run_id":"GTW-TSK179-RUN1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	retired, err := RetireRuntimeForRestart(stateDir, train.ProjectID, train.ID, binding.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !retired.RestartRequired || retired.RunID != binding.RunID {
		t.Fatalf("runtime generation was not retired: %#v", retired)
	}
	current, err := ReadRuntime(stateDir, train.ProjectID, train.ID)
	if err != nil || !current.RestartRequired || current.WorktreePath != binding.WorktreePath {
		t.Fatalf("retired runtime binding was not preserved: %#v %v", current, err)
	}
	if _, err := os.Stat(dispatchReceiptPath(stateDir, train.ProjectID, train.ID)); !os.IsNotExist(err) {
		t.Fatalf("stale dispatch receipt survived retirement: %v", err)
	}
}
