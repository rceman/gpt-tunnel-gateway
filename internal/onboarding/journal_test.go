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

func journalTestStateDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func journalTestReceipt(t *testing.T, stateDir string) (Request, Receipt) {
	t.Helper()
	request := receiptTestRequest(t)
	request.GatewayStateDir = stateDir
	return request, preparedReceiptForTest(t, request)
}

func journalTestPath(t *testing.T, stateDir, operationID string) string {
	t.Helper()
	path, err := PreparedJournalPath(stateDir, operationID)
	if err != nil {
		t.Fatalf("prepared journal path: %v", err)
	}
	return path
}

func writeJournalRaw(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create journal directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write raw journal: %v", err)
	}
}

func journalCanonicalFileBytes(t *testing.T, receipt Receipt) []byte {
	t.Helper()
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	return append(data, '\n')
}

func TestPreparedJournalPathExactAndStrict(t *testing.T) {
	stateDir := journalTestStateDir(t)
	operationID := "11111111-1111-1111-1111-111111111111"
	expected := filepath.Join(stateDir, "onboarding", operationID+".json")
	actual, err := PreparedJournalPath(stateDir, operationID)
	if err != nil {
		t.Fatalf("PreparedJournalPath: %v", err)
	}
	if actual != expected {
		t.Fatalf("journal path = %q, want %q", actual, expected)
	}

	for _, invalid := range []string{
		"",
		"relative/state",
		stateDir + "/.",
		stateDir + "/..",
		stateDir + "/nested/../state",
		stateDir + "\x00bad",
		stateDir + "\rbad",
		stateDir + "\nbad",
	} {
		t.Run("state_"+strings.NewReplacer("/", "_", "\x00", "nul", "\r", "cr", "\n", "lf").Replace(invalid), func(t *testing.T) {
			if _, err := PreparedJournalPath(invalid, operationID); err == nil {
				t.Fatalf("accepted invalid state directory %q", invalid)
			}
		})
	}
	for _, invalid := range []string{
		"11111111-1111-1111-1111-11111111111",
		"11111111-1111-1111-1111-111111111111X",
		"11111111-1111-1111-1111-11111111111A",
		"{11111111-1111-1111-1111-111111111111}",
		"../../outside",
	} {
		t.Run("operation_"+invalid, func(t *testing.T) {
			if _, err := PreparedJournalPath(stateDir, invalid); err == nil {
				t.Fatalf("accepted invalid operation ID %q", invalid)
			}
		})
	}
}

func TestPreparedJournalCreateLoadDigestAndMode(t *testing.T) {
	stateDir := journalTestStateDir(t)
	request, receipt := journalTestReceipt(t, stateDir)
	result, err := WritePreparedJournal(stateDir, request, receipt)
	if err != nil {
		t.Fatalf("WritePreparedJournal: %v", err)
	}
	if !result.Created || result.OperationID != receipt.OperationID {
		t.Fatalf("unexpected write result: %#v", result)
	}
	wantPath := journalTestPath(t, stateDir, receipt.OperationID)
	if result.JournalPath != wantPath {
		t.Fatalf("journal path = %q, want %q", result.JournalPath, wantPath)
	}
	wantDigest, err := PreparedReceiptDigest(receipt, request)
	if err != nil {
		t.Fatalf("PreparedReceiptDigest: %v", err)
	}
	if result.ReceiptSHA256 != wantDigest {
		t.Fatalf("receipt digest = %q, want %q", result.ReceiptSHA256, wantDigest)
	}
	loaded, err := LoadPreparedJournal(stateDir, receipt.OperationID)
	if err != nil {
		t.Fatalf("LoadPreparedJournal: %v", err)
	}
	if !bytes.Equal(journalCanonicalFileBytes(t, loaded), mustJournalFile(t, wantPath)) {
		t.Fatalf("loaded journal is not canonical")
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode = %o, want 600", info.Mode().Perm())
	}
	onboardingInfo, err := os.Stat(filepath.Dir(wantPath))
	if err != nil {
		t.Fatalf("stat onboarding directory: %v", err)
	}
	if onboardingInfo.Mode().Perm() != 0o700 {
		t.Fatalf("onboarding directory mode = %o, want 700", onboardingInfo.Mode().Perm())
	}
	locksInfo, err := os.Stat(filepath.Join(stateDir, "locks"))
	if err != nil {
		t.Fatalf("stat locks directory: %v", err)
	}
	if locksInfo.Mode().Perm() != 0o700 {
		t.Fatalf("locks directory mode = %o, want 700", locksInfo.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(stateDir, "projects")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected managed-project directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "hub")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected hub directory: %v", err)
	}
}

func mustJournalFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	return data
}

func receiptObjectReceipt(t *testing.T, receipt Receipt) Receipt {
	t.Helper()
	return receipt
}
