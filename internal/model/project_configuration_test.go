package model

import (
	"encoding/json"
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
	if err := configuration.Workflow.GateCommands.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(configuration.Workflow.GateCommands.Test.Task.Command) == 0 || len(configuration.Workflow.GateCommands.Test.Train.Command) == 0 {
		t.Fatal("test task/train commands are not persisted in defaults")
	}
}

func TestProjectGateCommandsRoundTripAndRejectShellCommands(t *testing.T) {
	configuration := DefaultProjectConfiguration("example", time.Unix(10, 0).UTC())
	configuration.Workflow.GateCommands.Test.Task = ProjectGateCommand{Command: []string{"./scripts/test-task", "--affected"}}
	configuration.Workflow.GateCommands.Test.Train = ProjectGateCommand{Command: []string{"./scripts/test-train", "--full"}}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ProjectConfiguration
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProjectConfiguration(roundTrip); err != nil {
		t.Fatal(err)
	}
	if strings.Join(roundTrip.Workflow.GateCommands.Test.Task.Command, " ") != "./scripts/test-task --affected" || strings.Join(roundTrip.Workflow.GateCommands.Test.Train.Command, " ") != "./scripts/test-train --full" {
		t.Fatalf("gate command round trip lost task/train definitions: %#v", roundTrip.Workflow.GateCommands)
	}
	bad := configuration
	bad.Workflow.GateCommands.Check = ProjectGateCommand{Command: []string{"sh", "-c", "go test ./..."}}
	if err := ValidateProjectConfiguration(bad); err == nil {
		t.Fatal("shell gate command accepted")
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
