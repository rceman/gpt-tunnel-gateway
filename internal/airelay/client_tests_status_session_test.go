package airelay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusPreservesNonZeroExitAsErrorState(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'State: idle\\n'; exit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command:         script,
		Timeout:         time.Second,
		MaxMessageBytes: 256,
	}
	status, err := c.Status(context.Background(), "project_master")
	if err != nil || status.State != "error" || status.ExitCode != 7 || status.Error == "" {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestTailTimeoutDoesNotExposeSession(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command:         script,
		Timeout:         10 * time.Millisecond,
		MaxMessageBytes: 256,
	}
	_, err := c.Tail(context.Background(), "secret_session", 4)
	if err == nil || !strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "secret_session") {
		t.Fatalf("bad timeout error: %v", err)
	}
}

func TestTailRejectsEmptyAndOversizedOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$2\" = empty ]; then exit 0; fi\nyes x | head -c 20000\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{
		Command:         script,
		Timeout:         time.Second,
		MaxMessageBytes: 256,
	}
	if _, err := c.Tail(context.Background(), "empty", 4); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("empty output not rejected: %v", err)
	}
	var buffer tailBuffer
	buffer.max = 8192
	if _, err := buffer.Write(make([]byte, 20000)); err != nil || !buffer.exceeded || buffer.Len() != 8192 {
		t.Fatalf("bounded capture failed: err=%v exceeded=%v len=%d", err, buffer.exceeded, buffer.Len())
	}
}
