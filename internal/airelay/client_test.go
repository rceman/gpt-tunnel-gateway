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

func TestTailUsesExactArgumentsAndNormalizesFixture(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + log + "\"\nprintf '\\033[31mWarning: Controller (0.1.35) is older than CLI (0.1.36). Consider restarting the session.\\033[0m\\n⚠ Selected model is at capacity. Please try a different model.\\n⚠ Heads up, you have less than 25%% of your weekly limit left. Run /status for a breakdown.\\ngpt-5.6-luna medium · Context 23%% left · ~/git/gpt-review-planner · weekly 18%% left\\000\\001\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	result, err := c.Tail(context.Background(), "project_master", 4)
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := "tail\nproject_master\n--lines\n4\n"
	if string(args) != wantArgs {
		t.Fatalf("argv=%q want %q", args, wantArgs)
	}
	for _, want := range []string{"Controller (0.1.35)", "Selected model is at capacity", "weekly limit left", "gpt-5.6-luna medium", "~/git/gpt-review-planner"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("normalized tail omitted %q: %q", want, result.Stdout)
		}
	}
	if strings.ContainsAny(result.Stdout, "\x00\x01\x1b") {
		t.Fatalf("unsafe controls remained: %q", result.Stdout)
	}
}

func TestTailExplicitLinesAndFailuresAreBounded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'tail output\\n'; exit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	if _, err := c.Tail(context.Background(), "project_master", 7); err == nil || !strings.Contains(err.Error(), "tail failed") {
		t.Fatalf("non-zero tail not rejected: %v", err)
	}
	if _, err := c.Tail(context.Background(), "project_master", 0); err == nil {
		t.Fatal("invalid line count accepted")
	}
}

func TestTailMinusOneUsesCanonicalViewportRows(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + log + "\"\nprintf 'viewport\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	if _, err := c.Tail(context.Background(), "project_master", -1); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "tail\nproject_master\n--lines\n30\n" {
		t.Fatalf("viewport argv=%q", args)
	}
}

func TestTranscriptUsesNativeBoundedPagination(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + log + "\"\nprintf 'one\\ntwo\\nthree\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	result, err := c.Transcript(context.Background(), "project_master", 50, 7)
	if err != nil || result.Stdout != "one\ntwo\nthree\n" {
		t.Fatalf("transcript window=%q err=%v", result.Stdout, err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "transcript\nproject_master\n--lines\n50\n--skip\n7\n--order\ndesc\n" {
		t.Fatalf("argv=%q", args)
	}
}

func TestTranscriptRejectsLineOverflowButAllowsLargeSkip(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'history\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	if _, err := c.Transcript(context.Background(), "project_master", 51, 0); err == nil {
		t.Fatal("transcript line overflow accepted")
	}
	if _, err := c.Transcript(context.Background(), "project_master", 50, -1); err == nil {
		t.Fatal("negative transcript skip accepted")
	}
	if result, err := c.Transcript(context.Background(), "project_master", 50, 1000000); err != nil || result.Stdout != "history\n" {
		t.Fatalf("large positive transcript skip rejected: result=%#v err=%v", result, err)
	}
}

func TestTranscriptRetainedOutputIsBounded(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nyes x | head -c 20000\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	result, err := c.Transcript(context.Background(), "project_master", 50, 0)
	if err == nil || !strings.Contains(err.Error(), "output exceeds limit") {
		t.Fatalf("oversized transcript was not rejected: len=%d err=%v", len(result.Stdout), err)
	}
}

func TestStatusParsesStateAndCapacityWarnings(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf 'Controller: reachable (5ms)\\nAirelay version: 0.1.54\\nProtocol version: 1\\nState: busy\\n⚠ Selected model is at capacity.\\n⚠ weekly limit left\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	status, err := c.Status(context.Background(), "project_master")
	if err != nil || status.State != "running" || !status.ControllerReachable || status.AirelayVersion != "0.1.54" || status.ProtocolVersion != "1" || len(status.CapacityWarnings) != 2 || status.ExitCode != 0 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestStatusPreservesNonZeroExitAsErrorState(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'State: idle\\n'; exit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
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
	c := Client{Command: script, Timeout: 10 * time.Millisecond, MaxMessageBytes: 256}
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
	c := Client{Command: script, Timeout: time.Second, MaxMessageBytes: 256}
	if _, err := c.Tail(context.Background(), "empty", 4); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("empty output not rejected: %v", err)
	}
	var buffer tailBuffer
	buffer.max = 8192
	if _, err := buffer.Write(make([]byte, 20000)); err != nil || !buffer.exceeded || buffer.Len() != 8192 {
		t.Fatalf("bounded capture failed: err=%v exceeded=%v len=%d", err, buffer.exceeded, buffer.Len())
	}
}
