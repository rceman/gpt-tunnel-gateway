package debug

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func installFakeGit(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGitOutputFailsClosedAtOutputBound(t *testing.T) {
	installFakeGit(t, "dd if=/dev/zero bs=70000 count=1 2>/dev/null")
	_, err := gitOutput(context.Background(), t.TempDir(), "status")
	if !errors.Is(err, errGitOutputLimit) {
		t.Fatalf("git output error=%v want %v", err, errGitOutputLimit)
	}
}

func TestGitOutputHonorsProbeDeadline(t *testing.T) {
	installFakeGit(t, "sleep 2 >/dev/null 2>&1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := gitOutput(ctx, t.TempDir(), "status")
	if err == nil || ctx.Err() == nil {
		t.Fatalf("git output err=%v context=%v want deadline failure", err, ctx.Err())
	}
}
