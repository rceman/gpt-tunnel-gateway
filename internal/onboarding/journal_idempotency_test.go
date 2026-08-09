package onboarding

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedJournalSamePayloadIsIdempotentWithoutRewrite(t *testing.T) {
	stateDir := journalTestStateDir(t)
	request, receipt := journalTestReceipt(t, stateDir)
	first, err := WritePreparedJournal(stateDir, request, receipt)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	before, err := os.Stat(first.JournalPath)
	if err != nil {
		t.Fatalf("stat before retry: %v", err)
	}
	beforeBytes := mustJournalFile(t, first.JournalPath)
	second, err := WritePreparedJournal(stateDir, request, receipt)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if second.Created || second.JournalPath != first.JournalPath || second.ReceiptSHA256 != first.ReceiptSHA256 {
		t.Fatalf("unexpected idempotent result: %#v", second)
	}
	after, err := os.Stat(first.JournalPath)
	if err != nil {
		t.Fatalf("stat after retry: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("idempotent retry rewrote the journal inode")
	}
	if !bytes.Equal(beforeBytes, mustJournalFile(t, first.JournalPath)) {
		t.Fatal("idempotent retry changed journal bytes")
	}
}

func TestPreparedJournalConflictingPayloadPreservesBytes(t *testing.T) {
	stateDir := journalTestStateDir(t)
	request, receipt := journalTestReceipt(t, stateDir)
	first, err := WritePreparedJournal(stateDir, request, receipt)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	before := mustJournalFile(t, first.JournalPath)
	conflict := receipt
	conflict.WorktreeProof.StatusSHA256 = strings.Repeat("9", 64)
	if _, err := WritePreparedJournal(stateDir, request, conflict); err == nil || !strings.Contains(err.Error(), "ONBOARDING_OPERATION_CONFLICT") {
		t.Fatalf("conflicting write error = %v, want ONBOARDING_OPERATION_CONFLICT", err)
	}
	if !bytes.Equal(before, mustJournalFile(t, first.JournalPath)) {
		t.Fatal("conflicting write changed journal bytes")
	}
}

func TestPreparedJournalLoadValidationAndMissingSentinel(t *testing.T) {
	stateDir := journalTestStateDir(t)
	request, receipt := journalTestReceipt(t, stateDir)
	if _, err := LoadPreparedJournal(stateDir, receipt.OperationID); !errors.Is(err, ErrPreparedJournalNotFound) {
		t.Fatalf("missing journal error = %v, want ErrPreparedJournalNotFound", err)
	}
	if _, err := WritePreparedJournal(stateDir, request, Receipt{
		OperationID: receipt.OperationID,
		State:       StateHubCommitted,
	}); err == nil {
		t.Fatal("invalid prepared receipt was written")
	}
	if _, err := os.Stat(filepath.Join(stateDir, "onboarding")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid receipt created journal directory: %v", err)
	}
}
