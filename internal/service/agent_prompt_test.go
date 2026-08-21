package service

import (
	"context"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func TestAgentPromptResultIsPMTReferenceOnly(t *testing.T) {
	result := AgentPromptResult{ProjectID: "example", PMTID: "EXM-PMT1", Queued: true, Delivered: false}
	if result.ProjectID != "example" || result.PMTID == "" || !result.Queued || result.Delivered {
		t.Fatalf("unexpected PMT result=%#v", result)
	}
}

func TestPMTReadCLIFailsClosedWithoutExactCodingSession(t *testing.T) {
	if _, err := (&Service{}).PMTReadCLI(context.Background(), "EXM-PMT1"); err == nil {
		t.Fatal("PMT CLI read unexpectedly succeeded without local store")
	}
	if err := model.ValidateObjectIdentifier("EXM-PMT1"); err != nil {
		t.Fatalf("fixture PMT ID invalid: %v", err)
	}
}
