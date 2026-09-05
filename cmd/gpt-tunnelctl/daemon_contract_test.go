package main

import (
	"os"
	"strings"
	"testing"
)

func TestCtlLifecycleBoundaryKeepsOnlyHiddenDaemonPrimitives(t *testing.T) {
	startup, err := os.ReadFile("main_startup.go")
	if err != nil {
		t.Fatal(err)
	}
	startupSource := string(startup)
	for _, hidden := range []string{"daemon-start", "daemon-stop", "runtime-status"} {
		if !strings.Contains(startupSource, "\""+hidden+"\"") {
			t.Fatalf("hidden internal primitive %q is absent", hidden)
		}
	}
	for _, legacy := range []string{"case \"start\":", "case \"stop\":", "case \"restart\":", "case \"restart-gateway\":", "case \"status\":"} {
		if strings.Contains(startupSource, legacy) {
			t.Fatalf("obsolete public lifecycle dispatch remains: %s", legacy)
		}
	}
	usage, err := os.ReadFile("main_utilities.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(usage), "|status|doctor") {
		t.Fatal("gpt-tunnelctl status remains in public usage")
	}
}

func TestActivationUsesHiddenRuntimeStatus(t *testing.T) {
	data, err := os.ReadFile("../../scripts/integration_activate.py")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[ctl, \"runtime-status\"]") {
		t.Fatal("activation does not use hidden runtime-status")
	}
	if strings.Contains(string(data), "[ctl, \"status\"]") {
		t.Fatal("activation still uses removed public ctl status")
	}
}
