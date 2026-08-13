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

func TestRuntimeBindingUsesTrainItemAttemptIdentity(t *testing.T) {
	stateDir := t.TempDir()
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     "gateway",
		TrainID:       "GTW-TRN7",
		WorktreePath:  ExpectedWorktreePath(stateDir, "gateway", "GTW-TRN7"),
		AgentID:       "agent-1",
		SessionKey:    "gateway_master",
		ItemPosition:  0,
		TaskID:        "GTW-TSK179",
		AttemptNumber: 1,
		StartedAt:     time.Now().UTC(),
	}
	if err := ValidateRuntimeBinding(binding, stateDir); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "run_id") {
		t.Fatalf("runtime binding retains RunID: %s", data)
	}
}

func TestRetireRuntimeForRestartRemovesDispatchReceipt(t *testing.T) {
	stateDir := t.TempDir()
	binding := RuntimeBinding{
		SchemaVersion: runtimeSchemaVersion,
		ProjectID:     "gateway",
		TrainID:       "GTW-TRN7",
		WorktreePath:  ExpectedWorktreePath(stateDir, "gateway", "GTW-TRN7"),
		AgentID:       "agent-1",
		SessionKey:    "gateway_master",
		ItemPosition:  0,
		TaskID:        "GTW-TSK179",
		AttemptNumber: 1,
		StartedAt:     time.Now().UTC(),
	}
	data, _ := json.Marshal(binding)
	if err := fsutil.WriteFileAtomic(RuntimePath(stateDir, binding.ProjectID, binding.TrainID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsutil.WriteFileAtomic(dispatchReceiptPath(stateDir, binding.ProjectID, binding.TrainID), []byte(`{"attempt_number":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	retired, err := RetireRuntimeForRestart(stateDir, binding.ProjectID, binding.TrainID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !retired.RestartRequired || retired.AttemptNumber != 1 {
		t.Fatalf("runtime generation was not retired: %#v", retired)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "train-runtime", binding.ProjectID, binding.TrainID+".json.dispatch.json")); !os.IsNotExist(err) {
		t.Fatalf("dispatch receipt survived retirement: %v", err)
	}
}
