package airelay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPromptUsesFixedArgumentVector(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + log + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	if _, err := c.Prompt(context.Background(), "project_master", "Read task and execute it."); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"prompt", "project_master", "Read task and execute it."}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d=%q", i, got[i])
		}
	}
}
