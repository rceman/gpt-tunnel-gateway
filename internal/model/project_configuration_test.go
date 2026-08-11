package model

import (
	"strings"
	"testing"
	"time"
)

func TestProjectConfigurationDefaultsValidate(t *testing.T) {
	configuration := DefaultProjectConfiguration("example", time.Unix(10, 0).UTC())
	if err := ValidateProjectConfiguration(configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.AgentRouting.Fallback != ReasoningBestAvailable || configuration.Workflow.IntegrationBranch != "main" {
		t.Fatalf("unexpected defaults: %#v", configuration)
	}
}

func TestProjectConfigurationRejectsExecutionAndWatcherContractViolations(t *testing.T) {
	base := DefaultProjectConfiguration("example", time.Unix(10, 0).UTC())
	tests := map[string]func(*ProjectConfiguration){
		"invalid watcher mode":         func(v *ProjectConfiguration) { v.Watcher.Mode = "retry" },
		"invalid watcher bounds":       func(v *ProjectConfiguration) { v.Watcher.TailLines = WatcherMaxTailLines + 1 },
		"invalid fallback":             func(v *ProjectConfiguration) { v.AgentRouting.Fallback = ReasoningHigh },
		"invalid activation reference": func(v *ProjectConfiguration) { v.ActivationProfileRef = "../default" },
		"unsafe updater":               func(v *ProjectConfiguration) { v.UpdatedBy = strings.Repeat("x", 1) + "\n" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := ValidateProjectConfiguration(candidate); err == nil {
				t.Fatal("invalid project configuration was accepted")
			}
		})
	}
}
