package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedJournalStateDirMustMatchRequestWithoutMutation(t *testing.T) {
	stateDir := journalTestStateDir(t)
	request := receiptTestRequest(t)
	receipt := preparedReceiptForTest(t, request)
	request.GatewayStateDir = filepath.Join(stateDir, "different-state")
	if _, err := WritePreparedJournal(stateDir, request, receipt); err == nil || !strings.Contains(err.Error(), "does not match request") {
		t.Fatalf("state directory mismatch error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "onboarding")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state mismatch created onboarding directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "locks")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state mismatch created locks directory: %v", err)
	}
}

func TestPreparedJournalLoadRejectsCanonicalIntrinsicInvalidReceipts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Receipt)
	}{
		{name: "dirty worktree", mutate: func(receipt *Receipt) { receipt.WorktreeProof.Clean = false }},
		{name: "relative repository root", mutate: func(receipt *Receipt) { receipt.RepositoryProof.Root = "relative" }},
		{name: "invalid registry digest", mutate: func(receipt *Receipt) { receipt.RegistryDigests.PlanSHA256 = "invalid" }},
		{name: "foreign hub path", mutate: func(receipt *Receipt) { receipt.Hub.Paths[0] = "gpt-tunnel/v1/projects/other/project.json" }},
		{name: "invalid timestamp order", mutate: func(receipt *Receipt) { receipt.Timestamps.StartedAt = *receipt.Timestamps.PreparedAt }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := journalTestStateDir(t)
			_, receipt := journalTestReceipt(t, stateDir)
			test.mutate(&receipt)
			path := journalTestPath(t, stateDir, receipt.OperationID)
			writeJournalRaw(t, path, journalCanonicalFileBytes(t, receipt))
			if _, err := LoadPreparedJournal(stateDir, receipt.OperationID); err == nil {
				t.Fatalf("LoadPreparedJournal accepted intrinsic-invalid %s receipt", test.name)
			}
		})
	}
}

func TestPreparedJournalLoadRejectsForeignGatewayStateDir(t *testing.T) {
	stateDir := journalTestStateDir(t)
	_, receipt := journalTestReceipt(t, stateDir)
	receipt.RepositoryProof.GatewayStateDir = filepath.Join(t.TempDir(), "foreign-state")
	path := journalTestPath(t, stateDir, receipt.OperationID)
	writeJournalRaw(t, path, journalCanonicalFileBytes(t, receipt))
	if _, err := LoadPreparedJournal(stateDir, receipt.OperationID); err == nil || !strings.Contains(err.Error(), "does not match state directory") {
		t.Fatalf("foreign gateway state directory error = %v", err)
	}
}

func TestPreparedJournalOperationPathMismatch(t *testing.T) {
	stateDir := journalTestStateDir(t)
	_, receipt := journalTestReceipt(t, stateDir)
	path := journalTestPath(t, stateDir, receipt.OperationID)
	other := receipt
	other.OperationID = "22222222-2222-2222-2222-222222222222"
	writeJournalRaw(t, path, journalCanonicalFileBytes(t, other))
	if _, err := LoadPreparedJournal(stateDir, receipt.OperationID); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("operation/path mismatch error = %v", err)
	}
}

func TestPreparedJournalRejectsUnsafeFiles(t *testing.T) {
	_, receipt := journalTestReceipt(t, journalTestStateDir(t))
	base := journalCanonicalFileBytes(t, receipt)
	tests := []struct {
		name string
		make func(t *testing.T, path string, data []byte)
	}{
		{name: "symlink", make: func(t *testing.T, path string, data []byte) {
			target := filepath.Join(t.TempDir(), "target.json")
			if err := os.WriteFile(target, data, 0o600); err != nil {
				t.Fatalf("write symlink target: %v", err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("create symlink directory: %v", err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatalf("create symlink: %v", err)
			}
		}},
		{name: "directory", make: func(t *testing.T, path string, _ []byte) {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatalf("create journal directory: %v", err)
			}
		}},
		{name: "oversized", make: func(t *testing.T, path string, _ []byte) {
			writeJournalRaw(t, path, bytes.Repeat([]byte("x"), int(preparedJournalMaxBytes)+1))
		}},
		{name: "wrong mode", make: func(t *testing.T, path string, data []byte) {
			writeJournalRaw(t, path, data)
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatalf("chmod journal: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := journalTestStateDir(t)
			path := journalTestPath(t, stateDir, receipt.OperationID)
			test.make(t, path, base)
			if _, err := LoadPreparedJournal(stateDir, receipt.OperationID); err == nil {
				t.Fatalf("LoadPreparedJournal accepted %s", test.name)
			}
		})
	}
}

func TestPreparedJournalRejectsMalformedStrictJSON(t *testing.T) {
	_, receipt := journalTestReceipt(t, journalTestStateDir(t))
	base := journalCanonicalFileBytes(t, receipt)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "malformed", data: []byte("{")},
		{name: "unknown", data: func() []byte {
			object := receiptJSON(t, receiptObjectReceipt(t, receipt))
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(object, &fields); err != nil {
				t.Fatalf("unmarshal receipt: %v", err)
			}
			fields["unknown"] = json.RawMessage(`true`)
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatalf("marshal unknown receipt: %v", err)
			}
			return append(data, '\n')
		}()},
		{name: "duplicate", data: func() []byte {
			trimmed := bytes.TrimSuffix(base, []byte{'\n'})
			return append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(`,"state":"prepared"}`+"\n")...)
		}()},
		{name: "trailing", data: append(append([]byte(nil), base...), []byte("{}")...)},
		{name: "invalid utf8", data: append(append([]byte(nil), base...), 0xff)},
		{name: "null", data: bytes.Replace(base, []byte(`"project_id":"`+receipt.ProjectID+`"`), []byte(`"project_id":null`), 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := journalTestStateDir(t)
			path := journalTestPath(t, stateDir, receipt.OperationID)
			writeJournalRaw(t, path, test.data)
			if _, err := LoadPreparedJournal(stateDir, receipt.OperationID); err == nil {
				t.Fatalf("LoadPreparedJournal accepted %s", test.name)
			}
		})
	}
}
