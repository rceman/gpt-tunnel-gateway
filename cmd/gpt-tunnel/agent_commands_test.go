package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

func TestAgentTailCLIRequiresAndPassesExplicitSession(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "session")
	command := filepath.Join(dir, "airelay")
	contents := "#!/bin/sh\nif [ \"$1\" = tail ]; then printf '%s' \"$2\" > " + seen + "; printf 'line\\n'; fi\n"
	if err := os.WriteFile(command, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	s := service.New(config.Config{StateDir: dir, AirelayCommand: command, DispatchTimeoutSeconds: 1})
	agent(context.Background(), s, []string{"tail", "example", "--session", "SA-GTW-BEYB", "--lines", "1"})
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "SA-GTW-BEYB" {
		t.Fatalf("CLI passed session %q, want exact session", got)
	}
}
