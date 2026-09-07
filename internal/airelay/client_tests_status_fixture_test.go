package airelay

import (
	"context"
	"errors"
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
	c := Client{
		Command:         script,
		Timeout:         time.Second,
		MaxMessageBytes: 256,
	}
	if _, err := c.Prompt(context.Background(), "project_master", "Read task and execute it."); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"prompt", "project_master", "[GTW] Read task and execute it."}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d=%q", i, got[i])
		}
	}
}

func TestPromptBoundsChildOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec head -c 20000 /dev/zero\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command:         script,
		Timeout:         time.Second,
		MaxMessageBytes: 256,
	}
	result, err := c.Prompt(context.Background(), "project_master", "message")
	if err == nil || !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("unbounded prompt output was accepted: err=%v stdout=%d stderr=%d", err, len(result.Stdout), len(result.Stderr))
	}
}

func TestPromptHonorsCallerDeadline(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command: script,
		Timeout: time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := c.Prompt(ctx, "project_master", "message"); err == nil || !strings.Contains(err.Error(), "prompt timeout") {
		t.Fatalf("caller deadline was not propagated: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("prompt exceeded caller deadline by too much: %s", elapsed)
	}
}

func TestPromptWithProvenancePrefixesExactlyAtTransportBoundary(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "message")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s' \"$3\" > \"" + log + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command:         script,
		Timeout:         time.Second,
		MaxMessageBytes: 256,
	}
	if _, err := c.PromptWithProvenance(context.Background(), "project_master", "SP-ABCDEFGH", "[SD-ABCDEFGH] hello"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "[SP-ABCDEFGH] [SD-ABCDEFGH] hello" {
		t.Fatalf("message=%q", got)
	}
}

func TestPromptUsesUTF8ByteBoundAndPreservesProvenanceMarker(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "message")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$3\" > \""+log+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command:         script,
		Timeout:         time.Second,
		MaxMessageBytes: MaxTransportMessageBytes,
	}
	ascii := strings.Repeat("a", MaxPromptBytes)
	if _, err := c.PromptWithProvenance(context.Background(), "project_master", "SP-ABCDEFGH", ascii); err != nil {
		t.Fatalf("ASCII message at byte bound rejected: %v", err)
	}
	message := strings.Repeat("é", MaxPromptBytes/2)
	if got := len([]byte(message)); got != MaxPromptBytes {
		t.Fatalf("UTF-8 fixture bytes=%d want %d", got, MaxPromptBytes)
	}
	if _, err := c.PromptWithProvenance(context.Background(), "project_master", "SP-ABCDEFGH", message); err != nil {
		t.Fatalf("UTF-8 message at byte bound rejected: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "[SP-ABCDEFGH] "+message {
		t.Fatalf("marker or UTF-8 payload changed: %q", got)
	}
	var validation *MessageValidationError
	if _, err := c.PromptWithProvenance(context.Background(), "project_master", "SP-ABCDEFGH", message+"é"); !errors.As(err, &validation) || validation.Code != "CONTENT_TOO_LARGE" || validation.ActualBytes != MaxPromptBytes+2 {
		t.Fatalf("oversized UTF-8 message error=%T %v", err, err)
	}
}

func TestPromptPreservesEmptyAndNULValidationAsStructuredErrors(t *testing.T) {
	c := Client{MaxMessageBytes: MaxTransportMessageBytes}
	for _, test := range []struct {
		name string
		text string
		code string
	}{
		{name: "empty", code: "EMPTY"},
		{name: "nul", text: "before\x00after", code: "NUL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var validation *MessageValidationError
			_, err := c.PromptWithProvenance(context.Background(), "project_master", "SP-ABCDEFGH", test.text)
			if !errors.As(err, &validation) || validation.Code != test.code {
				t.Fatalf("validation=%T %v", err, err)
			}
			structured := validation.StructuredActionError()
			if structured["code"] != "AIRELAY_MESSAGE_INVALID" {
				t.Fatalf("structured=%#v", structured)
			}
		})
	}
}
