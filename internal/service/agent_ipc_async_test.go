package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	upstream "github.com/rceman/go-sqlite-store/store"
	"github.com/rceman/gpt-tunnel-gateway/internal/sqlitestore"
)

func TestAgentIPCMutationsReturnBoundedReceipts(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	ctx := context.Background()
	inputs := []struct {
		kind string
		id   string
		call func() (string, error)
	}{
		{kind: "agent-prompt", call: func() (string, error) {
			receipt, err := s.AgentPromptAsync(ctx, AgentPromptInput{
				ProjectID: "example",
				Message:   "bounded test prompt",
			})
			return receipt.OperationID, err
		}},
		{kind: "agent-interrupt", call: func() (string, error) {
			receipt, err := s.AgentInterruptAsync(ctx, AgentInterruptInput{
				ProjectID:  "example",
				SessionKey: "example_master",
				AgentID:    "EXM-AGT1",
			})
			return receipt.OperationID, err
		}},
	}
	for _, input := range inputs {
		id, err := input.call()
		if err != nil {
			t.Fatal(err)
		}
		if id == "" {
			t.Fatalf("%s returned an empty operation id", input.kind)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			value, statusErr := s.AgentIPCOperationStatus(ctx, id, input.kind)
			if statusErr != nil {
				t.Fatal(statusErr)
			}
			if agentIPCReceiptTerminal(value) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRepeatableAgentCommandsCreateFreshTurnsAfterTerminalHistory(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	var executions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	ctx := context.Background()
	calls := []struct {
		kind string
		call func() (string, error)
	}{
		{kind: "agent-interrupt", call: func() (string, error) {
			receipt, err := s.AgentInterruptAsync(ctx, AgentInterruptInput{
				ProjectID:  "example",
				SessionKey: "example_master",
				AgentID:    "EXM-AGT1",
			})
			return receipt.OperationID, err
		}},
	}
	for _, test := range calls {
		t.Run(test.kind, func(t *testing.T) {
			first, err := test.call()
			if err != nil {
				t.Fatal(err)
			}
			waitAgentCommandTerminal(t, s, first, test.kind)
			second, err := test.call()
			if err != nil {
				t.Fatal(err)
			}
			if first == second {
				t.Fatalf("terminal %s reused historical operation %s", test.kind, first)
			}
			waitAgentCommandTerminal(t, s, second, test.kind)
		})
	}
	if got := executions.Load(); got != 2 {
		t.Fatalf("fresh interrupt turns=%d, want 2", got)
	}
}

func TestAgentPromptWorkerPreservesOriginatingSessionProvenance(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	messagePath := filepath.Join(t.TempDir(), "message")
	command := filepath.Join(t.TempDir(), "airelay")
	script := "#!/bin/sh\nif [ \"$1\" = prompt ]; then printf '%s' \"$3\" > \"" + messagePath + "\"; fi\nexit 0\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = command

	const sessionID = "HOM_EXM_P_abcde"
	receipt, err := s.AgentPromptAsync(WithAgentSessionID(context.Background(), sessionID), AgentPromptInput{
		ProjectID: "example",
		Message:   "preserve this provenance",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, statusErr := s.AgentIPCOperationStatus(WithAgentSessionID(context.Background(), sessionID), receipt.OperationID, "agent-prompt")
		if statusErr != nil {
			t.Fatal(statusErr)
		}
		if agentIPCReceiptTerminal(value) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	message, err := os.ReadFile(messagePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(message), "["+sessionID+"] ") {
		t.Fatalf("outbound message lost originating session: %q", message)
	}
	if strings.HasPrefix(string(message), "[GTW] ") {
		t.Fatalf("outbound message used Gateway provenance: %q", message)
	}
}

func TestAgentPromptAfterTerminalCreatesFreshTurn(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	var executions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "repeatable prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	second, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == second.OperationID {
		t.Fatalf("terminal prompt reused historical operation: first=%s second=%s", first.OperationID, second.OperationID)
	}
	waitAgentPromptTerminal(t, s, second.OperationID)
	third, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == third.OperationID || second.OperationID == third.OperationID {
		t.Fatalf("sequential prompt reused a terminal operation: first=%s second=%s third=%s", first.OperationID, second.OperationID, third.OperationID)
	}
	waitAgentPromptTerminal(t, s, third.OperationID)
	if got := executions.Load(); got != 3 {
		t.Fatalf("fresh prompt turns=%d, want 3", got)
	}
}

func TestAgentPromptEquivalentAdmissionReusesInFlightTurn(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var executions atomic.Int32
	s.durableMutationExecutor = func(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
		if operation.Kind != "agent-prompt" {
			t.Fatalf("unexpected operation kind %q", operation.Kind)
		}
		executions.Add(1)
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "in-flight prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Agent prompt worker did not start")
	}
	second, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID {
		t.Fatalf("in-flight prompt was not deduplicated: first=%s second=%s", first.OperationID, second.OperationID)
	}
	close(release)
	waitAgentPromptTerminal(t, s, first.OperationID)
	if got := executions.Load(); got != 1 {
		t.Fatalf("in-flight prompt executions=%d, want 1", got)
	}
}

func TestAgentPromptOutcomeUnknownRetryReusesOperation(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	var executions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		if executions.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "retry uncertain prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	firstTerminal := waitAgentPromptTerminal(t, s, first.OperationID)
	if firstTerminal.Status != "outcome_unknown" {
		t.Fatalf("uncertain prompt status=%q, want outcome_unknown", firstTerminal.Status)
	}
	second, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID {
		t.Fatalf("uncertain prompt retry changed operation: first=%s second=%s", first.OperationID, second.OperationID)
	}
	secondTerminal := waitAgentPromptTerminal(t, s, second.OperationID)
	if secondTerminal.Status != "completed" || executions.Load() != 2 {
		t.Fatalf("uncertain prompt retry result=%#v executions=%d", secondTerminal, executions.Load())
	}
}

func TestAgentPromptEquivalentAdmissionReusesFreshInFlightTurnAfterHistory(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	var executions atomic.Int32
	s.durableMutationExecutor = func(ctx context.Context, operation durableMutationOperation) (json.RawMessage, error) {
		if operation.Kind != "agent-prompt" {
			t.Fatalf("unexpected operation kind %q", operation.Kind)
		}
		n := executions.Add(1)
		if n == 2 {
			close(secondStarted)
			select {
			case <-releaseSecond:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "history then overlap",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	second, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("fresh Agent prompt worker did not start")
	}
	third, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID != third.OperationID || first.OperationID == third.OperationID {
		t.Fatalf("overlapping post-terminal prompt admission mismatch: first=%s second=%s third=%s", first.OperationID, second.OperationID, third.OperationID)
	}
	close(releaseSecond)
	waitAgentPromptTerminal(t, s, second.OperationID)
	if got := executions.Load(); got != 2 {
		t.Fatalf("overlapping post-terminal prompt executions=%d, want 2", got)
	}
}

func TestAgentPromptEquivalentTurnDiscoveryFailsClosedOnCorruptLocalOperation(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "corrupt equivalent prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	corrupt := seedAgentPromptTurn(t, s, input, "accepted")
	if err := os.WriteFile(durableMutationPath(s.Config.StateDir, corrupt), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentPromptAsync(context.Background(), input); err == nil || !strings.Contains(err.Error(), corrupt) {
		t.Fatalf("corrupt equivalent operation was not rejected closed: err=%v", err)
	}
}

func TestAgentPromptEquivalentDiscoveryPrefiltersSessionAndInput(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "prefilter prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	differentSession := seedAgentPromptTurn(t, s, input, "accepted")
	sessionOperation, err := s.readDurableMutation(differentSession)
	if err != nil {
		t.Fatal(err)
	}
	sessionOperation.SessionID = "other-session"
	if err := s.writeDurableMutation(sessionOperation); err != nil {
		t.Fatal(err)
	}
	differentInput := seedAgentPromptTurn(t, s, AgentPromptInput{
		ProjectID: "example",
		Message:   "different input",
	}, "accepted")
	second, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID == first.OperationID || second.OperationID == differentSession || second.OperationID == differentInput {
		t.Fatalf("equivalent discovery did not prefilter session/input: first=%s session=%s input=%s result=%s", first.OperationID, differentSession, differentInput, second.OperationID)
	}
	waitAgentPromptTerminal(t, s, second.OperationID)
}

func TestAgentPromptCrashAfterAtomicChildAllocationDoesNotAllocateOrExecute(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	var executions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "atomic child crash boundary",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	child, err := db.AllocateLocalOperationWithAdmissionCoordinate(context.Background(), "example", "EXM", strings.Repeat("f", 64), "agent-prompt", "", durableMutationInputSHA256(raw), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if child.AdmissionInputSHA256 != durableMutationInputSHA256(raw) {
		t.Fatalf("child admission coordinate=%q", child.AdmissionInputSHA256)
	}
	before, err := db.ListLocalOperations(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentPromptAsync(context.Background(), input); err == nil || !strings.Contains(err.Error(), child.OperationID) {
		t.Fatalf("crash-boundary child was not rejected closed: err=%v", err)
	}
	after, err := db.ListLocalOperations(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || executions.Load() != 1 {
		t.Fatalf("crash-boundary admission changed Local/execution state: before=%d after=%d executions=%d", len(before), len(after), executions.Load())
	}
}

func TestAgentPromptEquivalentTurnDiscoveryFailsClosedOnMissingLocalOperation(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	var executions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "missing equivalent prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	missing := seedAgentPromptTurn(t, s, input, "accepted")
	if err := os.Remove(durableMutationPath(s.Config.StateDir, missing)); err != nil {
		t.Fatal(err)
	}
	before, err := db.ListLocalOperations(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentPromptAsync(context.Background(), input); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing equivalent operation was not rejected closed: err=%v", err)
	}
	after, err := db.ListLocalOperations(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || executions.Load() != 1 {
		t.Fatalf("missing-operation admission changed Local/execution state: before=%d after=%d executions=%d", len(before), len(after), executions.Load())
	}
}

func TestAgentPromptEquivalentTurnDiscoveryFailsClosedOnCorruptLocalRecord(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	var executions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "corrupt Local record prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	corrupt := seedAgentPromptTurn(t, s, input, "accepted")
	beforeRows, err := db.Local.Query(context.Background(), `SELECT COUNT(*) FROM local_operations WHERE project_id=?`, "example")
	if err != nil {
		t.Fatal(err)
	}
	beforeCount, ok := beforeRows.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("invalid Local operation count=%#v", beforeRows.Rows)
	}
	_, err = s.Durability.Local.Batch(context.Background(), []upstream.Statement{{
		SQL:                 `UPDATE local_operations SET status=? WHERE operation_id=?`,
		Args:                []any{"corrupt", corrupt},
		RequireRowsAffected: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentPromptAsync(context.Background(), input); err == nil || !strings.Contains(err.Error(), "invalid local operation status") {
		t.Fatalf("corrupt Local record was not rejected closed: err=%v", err)
	}
	afterRows, err := db.Local.Query(context.Background(), `SELECT COUNT(*) FROM local_operations WHERE project_id=?`, "example")
	if err != nil {
		t.Fatal(err)
	}
	afterCount, ok := afterRows.Rows[0][0].(int64)
	if !ok {
		t.Fatalf("invalid Local operation count=%#v", afterRows.Rows)
	}
	if afterCount != beforeCount || executions.Load() != 1 {
		t.Fatalf("corrupt Local admission changed Local/execution state: before=%d after=%d executions=%d", beforeCount, afterCount, executions.Load())
	}
}

func TestAgentPromptUsesLatestEquivalentTurnAfterCompletedHistory(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	_ = testServiceWithDurability(t, s)
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "latest equivalent prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	older := seedAgentPromptTurn(t, s, input, "accepted")
	newer := seedAgentPromptTurn(t, s, input, "failed")
	second, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID != newer || second.OperationID == older {
		t.Fatalf("latest equivalent turn was not selected: got=%s older=%s newer=%s", second.OperationID, older, newer)
	}
	terminal := waitAgentPromptTerminal(t, s, second.OperationID)
	if terminal.Status != "completed" {
		t.Fatalf("latest equivalent retry status=%q", terminal.Status)
	}
}

func TestAgentPromptAfterRestartReusesLatestActiveTurnAfterCompletedHistory(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "restart active prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	latest := seedAgentPromptTurn(t, s, input, "accepted")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	restartedDB, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	restarted := NewWithDurabilityDeferredWorkers(s.Config, restartedDB)
	var executions atomic.Int32
	restarted.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	second, err := restarted.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if second.OperationID != latest {
		t.Fatalf("restart did not reuse latest active turn: got=%s want=%s", second.OperationID, latest)
	}
	terminal := waitAgentPromptTerminal(t, restarted, second.OperationID)
	if terminal.Status != "completed" || executions.Load() != 1 {
		t.Fatalf("restarted active turn result=%#v executions=%d", terminal, executions.Load())
	}
}

func TestAgentPromptAfterRestartReusesLatestFailedTurnAfterCompletedHistory(t *testing.T) {
	for _, status := range []string{"failed", "outcome_unknown"} {
		t.Run(status, func(t *testing.T) {
			s, _, _ := testServiceWithoutIdentifiers(t)
			db := testServiceWithDurability(t, s)
			s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
				return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
			}
			input := AgentPromptInput{
				ProjectID: "example",
				Message:   "restart failed prompt " + status,
			}
			first, err := s.AgentPromptAsync(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			waitAgentPromptTerminal(t, s, first.OperationID)
			latest := seedAgentPromptTurn(t, s, input, status)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			restartedDB, err := sqlitestore.Open(s.Config.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restartedDB.Close() })
			restarted := NewWithDurabilityDeferredWorkers(s.Config, restartedDB)
			var executions atomic.Int32
			restarted.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
				executions.Add(1)
				return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
			}
			second, err := restarted.AgentPromptAsync(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if second.OperationID != latest {
				t.Fatalf("restart did not reuse latest %s turn: got=%s want=%s", status, second.OperationID, latest)
			}
			terminal := waitAgentPromptTerminal(t, restarted, second.OperationID)
			if terminal.Status != "completed" || executions.Load() != 1 {
				t.Fatalf("restarted %s turn result=%#v executions=%d", status, terminal, executions.Load())
			}
		})
	}
}

func TestAgentPromptAfterRestartCreatesFreshTurnFromTerminalHistory(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	db := testServiceWithDurability(t, s)
	var firstExecutions atomic.Int32
	s.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		firstExecutions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	input := AgentPromptInput{
		ProjectID: "example",
		Message:   "restart prompt",
	}
	first, err := s.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	waitAgentPromptTerminal(t, s, first.OperationID)
	if got := firstExecutions.Load(); got != 1 {
		t.Fatalf("initial prompt executions=%d, want 1", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	restartedDB, err := sqlitestore.Open(s.Config.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartedDB.Close() })
	restarted := NewWithDurabilityDeferredWorkers(s.Config, restartedDB)
	var restartedExecutions atomic.Int32
	restarted.durableMutationExecutor = func(context.Context, durableMutationOperation) (json.RawMessage, error) {
		restartedExecutions.Add(1)
		return json.RawMessage(`{"project_id":"example","delivered":true}`), nil
	}
	second, err := restarted.AgentPromptAsync(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID == second.OperationID {
		t.Fatalf("restart reused terminal prompt operation: first=%s second=%s", first.OperationID, second.OperationID)
	}
	waitAgentPromptTerminal(t, restarted, second.OperationID)
	if got := restartedExecutions.Load(); got != 1 {
		t.Fatalf("restarted prompt executions=%d, want 1", got)
	}
}

func seedAgentPromptTurn(t *testing.T, s *Service, input AgentPromptInput, status string) string {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := freshDurableMutationDigest("agent-prompt", "", raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	allocated, err := s.Durability.AllocateLocalOperation(context.Background(), "example", "EXM", digest, "agent-prompt", now)
	if err != nil {
		t.Fatal(err)
	}
	operation := durableMutationOperation{
		SchemaVersion: durableMutationSchemaVersion,
		OperationID:   allocated.OperationID,
		MutationID:    digest,
		Kind:          "agent-prompt",
		RequestSHA256: digest,
		ProjectID:     "example",
		Input:         raw,
		Status:        status,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if status == "failed" {
		operation.Error = "seeded failed turn"
	} else if status == "outcome_unknown" {
		operation.Error = "seeded uncertain turn"
	}
	if err := s.writeDurableMutation(operation); err != nil {
		t.Fatal(err)
	}
	return operation.OperationID
}

func waitAgentPromptTerminal(t *testing.T, s *Service, operationID string) AgentPromptReceipt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := s.AgentIPCOperationStatus(context.Background(), operationID, "agent-prompt")
		if err != nil {
			t.Fatal(err)
		}
		receipt, ok := value.(AgentPromptReceipt)
		if !ok {
			t.Fatalf("unexpected Agent prompt receipt type %T", value)
		}
		if receipt.Status == "completed" || receipt.Status == "failed" || receipt.Status == "outcome_unknown" {
			return receipt
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Agent prompt operation %s did not reach terminal state", operationID)
	return AgentPromptReceipt{}
}

func waitAgentCommandTerminal(t *testing.T, s *Service, operationID, kind string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := s.AgentIPCOperationStatus(context.Background(), operationID, kind)
		if err != nil {
			t.Fatal(err)
		}
		if agentIPCReceiptTerminal(value) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("Agent %s operation %s did not reach terminal state", kind, operationID)
}

func agentIPCReceiptTerminal(value any) bool {
	switch receipt := value.(type) {
	case AgentPromptReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	case AgentInterruptReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	default:
		return false
	}
}
