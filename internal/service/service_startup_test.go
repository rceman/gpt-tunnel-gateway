package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
)

func TestStartupServiceDefersDurableRecoveryWorkers(t *testing.T) {
	stateDir := t.TempDir()
	operationID := "task-create-startup-recovery"
	operation := TaskCreateOperation{
		SchemaVersion: taskCreateOperationSchemaVersion,
		OperationID:   operationID,
		Status:        "running",
		CreatedAt:     time.Unix(1, 0).UTC(),
		UpdatedAt:     time.Unix(1, 0).UTC(),
	}
	path := taskCreateOperationPath(stateDir, operationID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(operation)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_ = NewWithDurabilityDeferredWorkers(config.Config{StateDir: stateDir}, nil)
	var got TaskCreateOperation
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.RecoveryReason != "" {
		t.Fatalf("startup constructor replayed durable work before Hub validation: %#v", got)
	}
}
