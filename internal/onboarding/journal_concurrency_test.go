package onboarding

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

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
