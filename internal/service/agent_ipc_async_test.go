package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentIPCMutationsReturnBoundedReceipts(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	ctx := context.Background()
	inputs := []struct {
		kind string
		id   string
		call func() (string, error)
	}{
		{kind: "agent-prompt", call: func() (string, error) {
			started := time.Now()
			receipt, err := s.AgentPromptAsync(ctx, AgentPromptInput{
				ProjectID: "example",
				Message:   "bounded test prompt",
			})
			if err == nil && time.Since(started) >= time.Second {
				t.Fatalf("agent/prompt initiation exceeded one second")
			}
			return receipt.OperationID, err
		}},
		{kind: "agent-recover", call: func() (string, error) {
			receipt, err := s.AgentRecoveryAsync(ctx, AgentRecoverInput{
				ProjectID:     "example",
				TrainID:       "invalid",
				TaskID:        "EXM-TSK1",
				AgentID:       "EXM-AGT1",
				AttemptNumber: 1,
			})
			return receipt.OperationID, err
		}},
		{kind: "agent-interrupt", call: func() (string, error) {
			receipt, err := s.AgentInterruptAsync(ctx, AgentInterruptInput{
				OperationID: "interrupt-test",
				ProjectID:   "example",
				SessionKey:  "example_master",
				AgentID:     "EXM-AGT1",
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

func TestAgentPromptWorkerPreservesOriginatingSessionProvenance(t *testing.T) {
	s, _, _ := testServiceWithoutIdentifiers(t)
	messagePath := filepath.Join(t.TempDir(), "message")
	command := filepath.Join(t.TempDir(), "airelay")
	script := "#!/bin/sh\nif [ \"$1\" = prompt ]; then printf '%s' \"$3\" > \"" + messagePath + "\"; fi\nexit 0\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = command

	const sessionID = "SP-ABCDEFGH"
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

func agentIPCReceiptTerminal(value any) bool {
	switch receipt := value.(type) {
	case AgentPromptReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	case AgentRecoveryReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	case AgentInterruptReceipt:
		return receipt.Status == "completed" || receipt.Status == "failed"
	default:
		return false
	}
}
