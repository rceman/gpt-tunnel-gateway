package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/model"
)

func writeIntegrationHook(t *testing.T, output string) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '"+output+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return root, "./hook.sh"
}

func TestRunIntegrationHookParsesExactSourceEvidence(t *testing.T) {
	expected := strings.Repeat("a", 40)
	root, hook := writeIntegrationHook(t, `{"phase":"post","source_sha":"`+expected+`","tunnel_pid":123,"status":{"gateway_ready":true}}`)
	result, err := runIntegrationHook(context.Background(), model.ProjectGateCommand{Command: []string{hook}}, root, expected)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured || result.SourceHead != expected || !strings.Contains(result.Evidence, expected) {
		t.Fatalf("unexpected hook result: %#v", result)
	}
}

func TestRunIntegrationHookRejectsMismatchedSourceEvidence(t *testing.T) {
	expected := strings.Repeat("a", 40)
	actual := strings.Repeat("b", 40)
	root, hook := writeIntegrationHook(t, `{"phase":"pre","source_sha":"`+actual+`","tunnel_pid":123,"status":{}}`)
	if _, err := runIntegrationHook(context.Background(), model.ProjectGateCommand{Command: []string{hook}}, root, expected); err == nil || !strings.Contains(err.Error(), "does not match expected source") {
		t.Fatalf("expected exact-source rejection, got %v", err)
	}
}

func TestParseIntegrationHookEvidenceRejectsTrailingValues(t *testing.T) {
	expected := strings.Repeat("a", 40)
	if _, err := parseIntegrationHookEvidence(`{"source_sha":"`+expected+`"} {}`, expected); err == nil {
		t.Fatal("trailing hook JSON was accepted")
	}
}

func TestParseIntegrationHookEvidenceAllowsUnconfiguredHook(t *testing.T) {
	result, err := parseIntegrationHookEvidence("not_configured", strings.Repeat("a", 40))
	if err != nil || result.Configured || result.Evidence != "not_configured" {
		t.Fatalf("unexpected unconfigured hook result: %#v err=%v", result, err)
	}
}

func TestParseConfiguredIntegrationHookEvidenceRejectsUnconfiguredMarker(t *testing.T) {
	if _, err := parseConfiguredIntegrationHookEvidence("not_configured", strings.Repeat("a", 40)); err == nil {
		t.Fatal("configured hook accepted missing source evidence")
	}
}
