package service

import (
	"context"
	"testing"
)

func TestAgentInterruptCurrentAgentDoesNotRequireTrainAttempt(t *testing.T) {
	s, _, _ := testService(t)
	result, err := s.AgentInterrupt(context.Background(), AgentInterruptInput{
		OperationID: "interrupt-current-agent",
		ProjectID:   "example",
		SessionKey:  "example_master",
		AgentID:     "coder-example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "already_idle" || result.AgentID != "coder-example" {
		t.Fatalf("current-Agent interrupt was not routed to Airelay: %#v", result)
	}
}
