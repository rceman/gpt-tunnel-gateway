package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func receiptObjectReceipt(t *testing.T, receipt Receipt) Receipt {
	t.Helper()
	return receipt
}

func TestPreparedJournalConcurrentSameAndConflictingWriters(t *testing.T) {
	stateDir := journalTestStateDir(t)
	request, receipt := journalTestReceipt(t, stateDir)
	const writers = 8
	type writeResult struct {
		candidate Receipt
		result    PreparedJournalWriteReceipt
		err       error
	}
	start := make(chan struct{})
	results := make(chan writeResult, writers)
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := WritePreparedJournal(stateDir, request, receipt)
			results <- writeResult{
				candidate: receipt,
				result:    result,
				err:       err,
			}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	created := 0
	succeeded := 0
	expectedPath := journalTestPath(t, stateDir, receipt.OperationID)
	expectedDigest, err := PreparedReceiptDigest(receipt, request)
	if err != nil {
		t.Fatalf("same-payload expected digest: %v", err)
	}
	for result := range results {
		if result.err != nil {
			t.Fatalf("same-payload writer failed: %v", result.err)
		}
		succeeded++
		if result.result.OperationID != receipt.OperationID || result.result.JournalPath != expectedPath || result.result.ReceiptSHA256 != expectedDigest {
			t.Fatalf("same-payload writer returned inconsistent identity: %#v", result.result)
		}
		if result.result.Created {
			created++
		}
	}
	if succeeded != writers {
		t.Fatalf("same-payload writers succeeded %d/%d", succeeded, writers)
	}
	if created != 1 {
		t.Fatalf("same-payload writers created %d journals, want 1", created)
	}

	conflictStateDir := journalTestStateDir(t)
	conflictRequest := request
	conflictRequest.GatewayStateDir = conflictStateDir
	conflictReceipt := preparedReceiptForTest(t, conflictRequest)
	conflict := conflictReceipt
	conflict.WorktreeProof.StatusSHA256 = strings.Repeat("9", 64)
	start = make(chan struct{})
	results = make(chan writeResult, 2)
	group = sync.WaitGroup{}
	for _, candidate := range []Receipt{conflictReceipt, conflict} {
		candidate := candidate
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, err := WritePreparedJournal(conflictStateDir, conflictRequest, candidate)
			results <- writeResult{
				candidate: candidate,
				result:    result,
				err:       err,
			}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	conflicts := 0
	var winner writeResult
	for result := range results {
		if result.err == nil {
			if successes != 0 {
				t.Fatalf("conflicting writers produced more than one success")
			}
			if !result.result.Created {
				t.Fatalf("conflicting writer success was not the created winner: %#v", result.result)
			}
			if result.result.OperationID != conflict.OperationID || result.result.JournalPath != journalTestPath(t, conflictStateDir, conflict.OperationID) {
				t.Fatalf("conflicting winner returned inconsistent identity: %#v", result.result)
			}
			winner = result
			successes++
		} else if result.err.Error() == "ONBOARDING_OPERATION_CONFLICT" {
			conflicts++
		} else {
			t.Fatalf("unexpected conflicting writer error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("conflicting writers successes=%d conflicts=%d, want one each", successes, conflicts)
	}
	winnerDigest, err := PreparedReceiptDigest(winner.candidate, conflictRequest)
	if err != nil {
		t.Fatalf("conflicting winner digest: %v", err)
	}
	if winner.result.ReceiptSHA256 != winnerDigest {
		t.Fatalf("conflicting winner digest = %q, want %q", winner.result.ReceiptSHA256, winnerDigest)
	}
	loaded, err := LoadPreparedJournal(conflictStateDir, conflict.OperationID)
	if err != nil {
		t.Fatalf("final conflicting journal is not loadable: %v", err)
	}
	loadedDigest, err := PreparedReceiptDigest(loaded, conflictRequest)
	if err != nil {
		t.Fatalf("final conflicting journal digest: %v", err)
	}
	if loadedDigest != winnerDigest || !bytes.Equal(mustJournalFile(t, winner.result.JournalPath), journalCanonicalFileBytes(t, winner.candidate)) {
		t.Fatal("final conflicting journal does not match the created winner")
	}
}
