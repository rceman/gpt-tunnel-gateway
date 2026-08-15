package service

import (
	"context"
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
			receipt, err := s.AgentPromptAsync(ctx, AgentPromptInput{ProjectID: "example", Message: "bounded test prompt"})
			if err == nil && time.Since(started) >= time.Second {
				t.Fatalf("agent/prompt initiation exceeded one second")
			}
			return receipt.OperationID, err
		}},
		{kind: "agent-recover", call: func() (string, error) {
			receipt, err := s.AgentRecoveryAsync(ctx, AgentRecoverInput{ProjectID: "example", TrainID: "invalid", TaskID: "EXM-TSK1", AgentID: "EXM-AGT1", AttemptNumber: 1})
			return receipt.OperationID, err
		}},
		{kind: "agent-interrupt", call: func() (string, error) {
			receipt, err := s.AgentInterruptAsync(ctx, AgentInterruptInput{OperationID: "interrupt-test", ProjectID: "example", TrainID: "invalid", TaskID: "EXM-TSK1", AgentID: "EXM-AGT1", AttemptNumber: 1})
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
