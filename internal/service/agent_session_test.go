package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentSendUsesConfiguredSessionAndDoesNotMutateHub(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argsPath+"\"\nprintf 'delivered\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	before, err := s.Hub.RemoteRevision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.AgentSend(context.Background(), "example", "hello agent")
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Delivered || receipt.ExitCode != 0 || receipt.ProjectID != "example" || receipt.Stdout != "delivered\n" {
		t.Fatalf("unexpected delivery receipt: %#v", receipt)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "prompt\nexample_master\nhello agent\n" {
		t.Fatalf("unexpected Airelay argv: %q", args)
	}
	after, err := s.Hub.RemoteRevision(context.Background())
	if err != nil || before != after {
		t.Fatalf("agent send mutated durable hub: before=%s after=%s err=%v", before, after, err)
	}
}

func TestAgentSendSerializesPerConfiguredSession(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.2\nprintf 'ok\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	firstErr := make(chan error, 1)
	go func() {
		_, err := s.AgentSend(context.Background(), "example", "first")
		firstErr <- err
	}()
	time.Sleep(40 * time.Millisecond)
	_, second := s.AgentSend(context.Background(), "example", "second")
	if second == nil || !strings.Contains(second.Error(), "already in progress") {
		t.Fatalf("concurrent send was not serialized: %v", second)
	}
	if err := <-firstErr; err != nil {
		t.Fatal(err)
	}
}

func TestAgentTailSupportsDefaultAndSkip(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsPath + "\"\nprintf 'one\\ntwo\\nthree\\nfour\\nfive\\nsix\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	result, err := s.AgentTail(context.Background(), "example", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProjectID != "example" || result.Lines != 4 || result.Skip != 2 || result.Text != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("unexpected agent tail: %#v", result)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(args) != "tail\nexample_master\n--lines\n200\n" {
		t.Fatalf("unexpected tail argv: %q", args)
	}
}

func TestAgentTailCursorPagesAppendOnlyOutput(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	write := func(output string) {
		t.Helper()
		body := "#!/bin/sh\nprintf '" + strings.ReplaceAll(output, "\n", "\\n") + "\\n'\n"
		if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		s.Airelay.Command = script
	}
	write("one\ntwo\n")
	first, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{Lines: 2})
	if err != nil || first.Text != "one\ntwo\n" || first.NextCursor == "" || first.HasMore {
		t.Fatalf("initial tail=%#v err=%v", first, err)
	}
	write("one\ntwo\nthree\nfour\n")
	second, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		Lines:  1,
		Cursor: first.NextCursor,
	})
	if err != nil || second.Text != "three\n" || !second.HasMore {
		t.Fatalf("first delta=%#v err=%v", second, err)
	}
	third, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		Lines:  1,
		Cursor: second.NextCursor,
	})
	if err != nil || third.Text != "four\n" || third.HasMore {
		t.Fatalf("second delta=%#v err=%v", third, err)
	}
	empty, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		Lines:  1,
		Cursor: third.NextCursor,
	})
	if err != nil || empty.Text != "" || empty.NextCursor == "" || empty.HasMore {
		t.Fatalf("empty delta=%#v err=%v", empty, err)
	}
}

func TestAgentTailCursorRejectsStaleAndInvalidState(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	write := func(output string) {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '"+strings.ReplaceAll(output, "\n", "\\n")+"\\n'\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		s.Airelay.Command = script
	}
	write("one\ntwo\n")
	first, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{Lines: 1})
	if err != nil {
		t.Fatal(err)
	}
	write("replacement\n")
	if _, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		Lines:  1,
		Cursor: first.NextCursor,
	}); err == nil || !strings.Contains(err.Error(), "stale tail cursor") {
		t.Fatalf("replacement cursor was accepted: %v", err)
	}
	if _, err := s.AgentTailPage(context.Background(), "example", AgentTailInput{
		Lines:  1,
		Cursor: "not-a-cursor",
	}); err == nil || !strings.Contains(err.Error(), "invalid tail cursor") {
		t.Fatalf("invalid cursor was accepted: %v", err)
	}
}

func TestAgentStatusExposesStateAndCapacityWarnings(t *testing.T) {
	s, _, _ := testService(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "airelay")
	body := "#!/bin/sh\nprintf 'Controller: reachable (5ms)\\nAirelay version: 0.1.54\\nProtocol version: 1\\nState: busy\\n⚠ Selected model is at capacity.\\n⚠ weekly limit left\\n'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Airelay.Command = script
	result, err := s.AgentStatus(context.Background(), "example")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "running" || !result.ControllerReachable || result.AirelayVersion != "0.1.54" || result.ProtocolVersion != "1" || len(result.CapacityWarnings) != 2 || result.ExitCode != 0 {
		t.Fatalf("unexpected agent status: %#v", result)
	}
}

func TestAgentSessionRequiresConfiguredRegisteredProject(t *testing.T) {
	s, _, _ := testService(t)
	if _, err := s.AgentStatus(context.Background(), "caller_supplied_session"); err == nil || !strings.Contains(err.Error(), "unknown local project") {
		t.Fatalf("arbitrary session/project was accepted: %v", err)
	}
}
