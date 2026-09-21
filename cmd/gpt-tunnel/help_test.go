package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHelpQuickStartPrecedesCommandInventoryAndAvoidsStaleOnboardingUX(t *testing.T) {
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	help(nil)
	_ = writer.Close()
	os.Stdout = original
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	text := string(data)
	markers := []string{
		"New project quick start",
		"git clone <repository-url> <folder>",
		"cd <folder>",
		"Save the existing airelay session as <folder>_worker",
		"gpt-tunnel project onboard AIR agentir_worker",
		"AIR-WORKER",
		"printed token into the project chat",
		"session_start with {token}",
		"Commands",
	}
	previous := -1
	for _, marker := range markers {
		position := strings.Index(text, marker)
		if position <= previous {
			t.Fatalf("help marker %q is missing or out of order: %q", marker, text)
		}
		previous = position
	}
	for _, stale := range []string{"--code", "--worker-relay", "--lead-relay", "admin/project/onboard"} {
		if strings.Contains(text, stale) {
			t.Fatalf("help advertises stale or frozen syntax %q: %q", stale, text)
		}
	}
	for _, capability := range []string{"trust", "bypass", "model selection", "native-session", "runtime startup"} {
		if !strings.Contains(text, capability) {
			t.Fatalf("help does not state the boundary for %q: %q", capability, text)
		}
	}
}

func TestHelpCommandExitsSuccessfullyWithoutConfiguration(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help command failed: %v\n%s", err, output)
	}
	if !strings.HasPrefix(string(output), "New project quick start\n") {
		t.Fatalf("help output did not start with quick start: %q", output)
	}
}
